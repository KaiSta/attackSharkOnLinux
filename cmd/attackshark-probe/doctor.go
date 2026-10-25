package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"attackshark-x11/internal/hidraw"
	"attackshark-x11/internal/mouse"
	"attackshark-x11/internal/notify"
	"attackshark-x11/internal/trayapp"
)

// Check statuses. A failure is something that stops the app from working; a
// warning is something worth knowing that the app copes with.
const (
	statusOK   = "ok"
	statusWarn = "warn"
	statusFail = "FAIL"
)

type doctorCheck struct {
	label   string
	status  string
	details []string
}

// doctor accumulates a check report and prints it as one aligned block rather
// than printing as it goes: the counts at the end are part of the answer.
type doctor struct {
	out    io.Writer
	checks []doctorCheck
}

func newDoctor(out io.Writer) *doctor { return &doctor{out: out} }

func (d *doctor) add(label, status string, details ...string) {
	d.checks = append(d.checks, doctorCheck{label: label, status: status, details: details})
}

func (d *doctor) counts() (ok, warn, fail int) {
	for _, check := range d.checks {
		switch check.status {
		case statusOK:
			ok++
		case statusWarn:
			warn++
		default:
			fail++
		}
	}
	return ok, warn, fail
}

// finish prints the report and returns the error the command exits on. Only
// failures do that: a warning is not a reason to refuse to run, which is the
// line the app itself draws.
func (d *doctor) finish() error {
	for _, check := range d.checks {
		for i, detail := range check.details {
			status, label := "", ""
			if i == 0 {
				status, label = check.status, check.label
			}
			fmt.Fprintf(d.out, "%-4s %-14s %s\n", status, label, detail)
		}
	}

	ok, warn, fail := d.counts()
	fmt.Fprintf(d.out, "\n%d checks: %d ok, %d warning(s), %d failure(s)\n",
		len(d.checks), ok, warn, fail)
	if fail > 0 {
		return fmt.Errorf("doctor found %d failing check(s): see the report above", fail)
	}
	return nil
}

// runDoctor checks the pieces a working installation needs and names the one
// that is missing. It is read-only: it opens the receiver exactly the way the
// tray app does, and never writes to the mouse.
func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	wait := fs.Duration("wait", 3*time.Second,
		"how long to wait for a report when checking device access")
	dpiConfig := fs.String("dpi-config", trayapp.DefaultDPIConfigPath(),
		"the per-stage DPI values file to check")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "%s\n\nusage: attackshark-probe doctor [flags]\n\n",
			"Check the udev rule, device access, DPI config, tray binary and session bus.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	fmt.Printf("checking the installation for %04x:%04x, read-only\n\n",
		hidraw.VendorID, hidraw.ProductID)

	d := newDoctor(os.Stdout)
	checkUdev(d)
	checkDeviceAccess(d, *wait)
	checkDPIConfig(d, *dpiConfig)
	checkTrayBinary(d)
	checkSessionBus(d)
	return d.finish()
}

// checkUdev reports on the rule and on the symlink that rule creates. Neither is
// a failure on its own: the symlink is a convenience, and whether the rule is
// actually granting access is decided by opening the device.
func checkUdev(d *doctor) {
	const rule = "/etc/udev/rules.d/99-attackshark-x11.rules"
	if _, err := os.Stat(rule); err == nil {
		d.add("udev rule", statusOK, rule)
	} else {
		d.add("udev rule", statusWarn,
			rule+" is missing",
			"install it once with:  sudo tools/install-udev.sh")
	}

	switch _, err := os.Stat(hidraw.SymlinkPath); {
	case err == nil:
		d.add("udev symlink", statusOK, symlinkDescription())
	case os.IsNotExist(err):
		d.add("udev symlink", statusWarn,
			hidraw.SymlinkPath+" is missing",
			"not fatal: the app scans sysfs for the receiver instead.",
			"sudo tools/install-udev.sh creates it, and needs the receiver plugged in.")
	default:
		d.add("udev symlink", statusWarn, fmt.Sprintf("%s: %v", hidraw.SymlinkPath, err))
	}
}

// symlinkDescription names the node the udev symlink points at. The raw target is
// not worth printing: udev writes it relative to /dev, so it reads like a bare
// filename. A symlink left dangling by an unplugged receiver is reported rather
// than failing the check.
func symlinkDescription() string {
	resolved, err := filepath.EvalSymlinks(hidraw.SymlinkPath)
	if err == nil {
		return fmt.Sprintf("%s -> %s", hidraw.SymlinkPath, resolved)
	}
	target, err := os.Readlink(hidraw.SymlinkPath)
	if err != nil {
		target = "(unresolvable)"
	}
	return fmt.Sprintf("%s -> %s (dangling)", hidraw.SymlinkPath, target)
}

// checkDeviceAccess is the check with teeth: it discovers the receiver and opens
// it the way the app does, then waits for one report. That is the only way to
// prove the udev rule, the group membership and the device all line up.
func checkDeviceAccess(d *doctor, wait time.Duration) {
	candidates, err := hidraw.Devices()
	if err != nil {
		d.add("discovery", statusFail, err.Error(),
			"plug in the 2.4 GHz receiver: the charging cable alone does not create it")
		return
	}

	first := candidates[0]
	if first.Interface != hidraw.ConfigInterface {
		d.add("discovery", statusFail,
			fmt.Sprintf("%d hidraw node(s), none on USB interface %d (best: %s, interface %d)",
				len(candidates), hidraw.ConfigInterface, first.Path, first.Interface),
			"the vendor channel is not exposed, so there is nothing to read")
		return
	}
	d.add("discovery", statusOK,
		fmt.Sprintf("%d hidraw node(s); vendor channel on %s (interface %d)",
			len(candidates), first.Path, first.Interface))

	dev, err := hidraw.Open()
	if err != nil {
		d.add("device access", statusFail, err.Error(), accessHint(err))
		return
	}
	defer dev.Close()

	buf := make([]byte, hidraw.ReadBufferSize)
	n, err := dev.ReadReport(buf, wait)
	switch {
	case errors.Is(err, os.ErrDeadlineExceeded):
		d.add("device access", statusWarn,
			fmt.Sprintf("opened %s, but no report arrived within %s", dev.Path(), wait),
			"the mouse sends a heartbeat every ~2 s while it is awake, so it may be asleep,",
			"switched off or out of range - nothing is wrong with the installation")
	case err != nil:
		d.add("device access", statusFail, fmt.Sprintf("read from %s: %v", dev.Path(), err))
	default:
		d.add("device access", statusOK, decodedRead(dev.Path(), buf[:n], n))
	}
}

// decodedRead describes the first report in the terms the tray shows it, so a
// successful check reads like the tooltip the user is about to see.
func decodedRead(path string, report []byte, n int) string {
	if battery, ok := mouse.ParseBattery(report); ok {
		return fmt.Sprintf("opened %s: battery %s", path, battery)
	}
	if stage, ok := mouse.ParseStage(report); ok {
		return fmt.Sprintf("opened %s: DPI %s", path, stage)
	}
	return fmt.Sprintf("opened %s: %d byte(s) that are neither battery nor DPI: % x",
		path, n, report)
}

// accessHint names the one thing that usually fixes a failed open, taken from
// the failure itself rather than guessed at.
func accessHint(err error) string {
	if errors.Is(err, os.ErrPermission) || strings.Contains(err.Error(), "permission denied") {
		return "access is denied: the udev rule grants it to the plugdev group. Run" +
			" 'sudo tools/install-udev.sh' and check that 'id -nG' lists plugdev."
	}
	return "run 'attackshark-probe info' to see what was discovered"
}

// checkDPIConfig reads the file the tray reads at startup. A malformed file is a
// failure rather than a warning, because the tray refuses to start rather than
// quietly discard values the user deliberately wrote.
func checkDPIConfig(d *doctor, path string) {
	if path == "" {
		d.add("dpi config", statusWarn, "no path could be determined (no home directory?)")
		return
	}

	stages, ok, err := trayapp.LoadDPIStages(path)
	switch {
	case err != nil:
		d.add("dpi config", statusFail, err.Error(),
			"the tray refuses to start with a file like this; fix it or delete it")
	case ok:
		d.add("dpi config", statusOK, fmt.Sprintf("%s: %s", path, stages))
	default:
		d.add("dpi config", statusOK, path+": no values set",
			"the tray shows the stage with no DPI number attached, which is the honest default")
	}
}

// checkTrayBinary looks for the installed tray app and the autostart entry that
// runs it. The probe works without either, so both are warnings.
func checkTrayBinary(d *doctor) {
	home, err := os.UserHomeDir()
	if err != nil {
		d.add("tray binary", statusWarn, "no home directory, so nothing to check")
		return
	}

	installed := filepath.Join(home, ".local", "bin", "attackshark-x11-tray")
	switch found, err := exec.LookPath("attackshark-x11-tray"); {
	case isExecutable(installed):
		d.add("tray binary", statusOK, installed)
	case err == nil:
		// A .deb install puts the binary in /usr/bin, which is on PATH, so
		// this is also how a package install is recognised as present.
		d.add("tray binary", statusOK, found+" (from PATH)")
	default:
		d.add("tray binary", statusWarn, "not installed",
			"install it with:  tools/install.sh   (or package it:  make package)")
	}

	// An autostart entry can live in two places: the per-user one that
	// tools/install.sh writes, or the system-wide one the .deb installs, which
	// applies to every user of the machine. Both start the tray, so both count.
	userEntry := filepath.Join(home, ".config", "autostart", "attackshark-x11-tray.desktop")
	systemEntry := "/etc/xdg/autostart/attackshark-x11-tray.desktop"
	switch {
	case isRegularFile(userEntry):
		d.add("autostart", statusOK, userEntry)
	case isRegularFile(systemEntry):
		d.add("autostart", statusOK, systemEntry+" (system-wide)")
	default:
		d.add("autostart", statusWarn, userEntry+" is missing",
			"the tray will not start with the session; tools/install.sh writes it, or install the .deb from make package")
	}
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

// isRegularFile is used for the autostart entry, which the desktop only has to
// read: requiring the executable bit there would warn about a file that works.
func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// checkSessionBus reports whether low-battery notifications have anywhere to go.
// A session without a bus is normal when the app is started from a terminal, and
// the tray runs fine without one - it just cannot warn.
func checkSessionBus(d *doctor) {
	conn, err := dbus.SessionBus()
	if err != nil {
		d.add("session bus", statusWarn, "no session bus: "+err.Error(),
			"notifications are unavailable; expected outside a desktop session")
		return
	}

	var hasOwner bool
	err = conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0,
		"org.freedesktop.Notifications").Store(&hasOwner)
	switch {
	case err != nil:
		d.add("session bus", statusWarn, "the bus could not be asked: "+err.Error())
	case !hasOwner:
		d.add("session bus", statusWarn,
			"the session bus has no org.freedesktop.Notifications owner",
			fmt.Sprintf("low-battery warnings will fail (the tray retries once after %s);",
				notify.RetryInterval),
			"everything else in the tray works without a notification daemon")
	default:
		d.add("session bus", statusOK, "org.freedesktop.Notifications is on the bus")
	}
}

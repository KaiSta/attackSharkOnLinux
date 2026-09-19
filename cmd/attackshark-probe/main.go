// Command attackshark-probe inspects the Attack Shark X11 receiver.
//
// Every subcommand is read-only: the receiver volunteers the battery level and
// the DPI stage, so nothing has to be written to the mouse to see them. Access
// needs no privileges once the udev rule is installed.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"attackshark-x11/internal/hidraw"
	"attackshark-x11/internal/mouse"
)

const usage = `attackshark-probe inspects the Attack Shark X11 2.4 GHz receiver, read-only.

usage: attackshark-probe <command> [flags]

commands:
  info        discovered hidraw nodes, report descriptor size, permissions
  reportmap   every input/output/feature report the descriptor declares
  battery     print the battery level (waits for the next heartbeat)
  stage       print the current DPI stage
  stream      print every status report as it arrives
  dump        hexdump every report, for reverse engineering
  doctor      check the installation: udev, device access, config, tray, session

flags are per-command; run "attackshark-probe <command> -h" for details.

notes:
  The battery heartbeat arrives every ~2s. The DPI stage is only announced
  when it changes, so "stage" returns nothing unless you tap the mouse's DPI
  button while it runs.
  The DPI value behind a stage is not readable from Linux, so "stage" prints
  the stage number only. See the README.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "info":
		err = runInfo(os.Args[2:])
	case "reportmap":
		err = runReportMap(os.Args[2:])
	case "battery":
		err = runBattery(os.Args[2:])
	case "stage":
		err = runStage(os.Args[2:])
	case "stream":
		err = runStream(os.Args[2:])
	case "dump":
		err = runDump(os.Args[2:])
	case "doctor":
		err = runDoctor(os.Args[2:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		fmt.Print(usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func newFlagSet(name, blurb string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "%s\n\nusage: attackshark-probe %s [flags]\n\n", blurb, name)
		fs.PrintDefaults()
	}
	path := fs.String("path", "", "open a specific hidraw node instead of discovering the receiver")
	return fs, path
}

func openFlagged(path string) (*hidraw.Device, error) {
	if path != "" {
		return hidraw.OpenPath(path)
	}
	return hidraw.Open()
}

func runInfo(args []string) error {
	fs, path := newFlagSet("info", "Show what is discovered about the receiver, and whether it can be opened.")
	if err := fs.Parse(args); err != nil {
		return err
	}

	fmt.Printf("receiver      %04x:%04x\n", hidraw.VendorID, hidraw.ProductID)
	fmt.Printf("sensor        %s, up to %d DPI, %d stages\n", mouse.SensorName, mouse.MaxDPI, mouse.MaxStage)

	if _, err := os.Stat(hidraw.SymlinkPath); err == nil {
		fmt.Printf("udev symlink  %s (present)\n", hidraw.SymlinkPath)
	} else {
		fmt.Printf("udev symlink  %s (missing - install udev/99-attackshark-x11.rules)\n", hidraw.SymlinkPath)
	}

	candidates, err := hidraw.Devices()
	if err != nil {
		return err
	}
	fmt.Println("hidraw nodes:")
	for _, c := range candidates {
		mark := " "
		if c.Interface == hidraw.ConfigInterface {
			mark = "*"
		}
		fmt.Printf("  %s %-18s interface %d  %s\n", mark, c.Path, c.Interface, c.Phys)
	}
	fmt.Println("  * = vendor configuration channel (battery and DPI stage reports)")

	dev, err := openFlagged(*path)
	if err != nil {
		fmt.Printf("\nopen failed: %v\n", err)
		return nil
	}
	defer dev.Close()

	fmt.Printf("\nopened        %s\n", dev.Path())
	if size, err := dev.DescriptorSize(); err == nil {
		fmt.Printf("descriptor    %d bytes\n", size)
	} else {
		fmt.Printf("descriptor    unavailable (%v)\n", err)
	}
	return nil
}

// runReportMap prints the reports the device's own HID report descriptor
// declares. It answers a question that a vendor's web tool usually decides
// silently: whether the vendor channel is an output report (sendable by any
// WebHID app) or a feature report (which WebHID tools that send output reports
// cannot use).
func runReportMap(args []string) error {
	fs, path := newFlagSet("reportmap", "List every input, output and feature report the descriptor declares.")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dev, err := openFlagged(*path)
	if err != nil {
		return err
	}
	defer dev.Close()

	desc, err := dev.Descriptor()
	if err != nil {
		return err
	}
	reports, err := hidraw.ParseReportDescriptor(desc)
	if err != nil {
		return fmt.Errorf("parsing the report descriptor of %s: %w", dev.Path(), err)
	}

	fmt.Printf("device          %s\n", dev.Path())
	fmt.Printf("descriptor      %d bytes\n", len(desc))
	if reports.HasReportIDs {
		fmt.Printf("report ids      explicit (declared with 0x85 items)\n")
	} else {
		fmt.Printf("report ids      none declared; every report uses id 0\n")
	}
	if reports.LongItems > 0 {
		fmt.Printf("long items      %d skipped (they never describe reports)\n", reports.LongItems)
	}

	for _, kind := range []hidraw.ReportKind{hidraw.Input, hidraw.Output, hidraw.Feature} {
		fields := reports.Of(kind)
		if len(fields) == 0 {
			fmt.Printf("%s reports   none\n", kind)
			continue
		}
		fmt.Printf("%s reports\n", kind)
		for _, f := range fields {
			fmt.Printf("  id 0x%02x  %8d bits  %3d bytes\n", f.ReportID, f.Bits, f.Bytes())
		}
	}

	// The distinction that decides vendor-software compatibility, spelled out.
	if _, isInput := reports.Find(hidraw.Input, 8); isInput {
		if field, isOutput := reports.Find(hidraw.Output, 8); isOutput {
			fmt.Printf("\nreport id 8     input + output (%d bytes each): an output-report tool can bind\n", field.Bytes())
		} else {
			fmt.Printf("\nreport id 8     input only, no output report: an output-report tool cannot bind\n")
		}
	}
	return nil
}

// waitFor reads reports until one parses with parse, or the timeout expires.
func waitFor(dev *hidraw.Device, timeout time.Duration, parse func([]byte) bool) ([]byte, bool) {
	buf := make([]byte, hidraw.ReadBufferSize)
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, false
		}
		n, err := dev.ReadReport(buf, remaining)
		if err != nil {
			return nil, false
		}
		report := make([]byte, n)
		copy(report, buf[:n])
		if parse(report) {
			return report, true
		}
	}
}

func runBattery(args []string) error {
	fs, path := newFlagSet("battery", "Print the battery level, waiting for the next heartbeat.")
	wait := fs.Duration("wait", 5*time.Second, "how long to wait for a heartbeat")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dev, err := openFlagged(*path)
	if err != nil {
		return err
	}
	defer dev.Close()

	report, ok := waitFor(dev, *wait, func(r []byte) bool {
		_, isBattery := mouse.ParseBattery(r)
		return isBattery
	})
	if !ok {
		return fmt.Errorf("no battery report within %s (is the mouse switched on and paired?)", *wait)
	}

	battery, _ := mouse.ParseBattery(report)
	fmt.Printf("%s\n", battery)
	fmt.Printf("raw           % x\n", report)
	return nil
}

func runStage(args []string) error {
	fs, path := newFlagSet("stage", "Print the current DPI stage.")
	wait := fs.Duration("wait", 5*time.Second, "how long to wait for a stage announcement")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dev, err := openFlagged(*path)
	if err != nil {
		return err
	}
	defer dev.Close()

	report, ok := waitFor(dev, *wait, func(r []byte) bool {
		_, isStage := mouse.ParseStage(r)
		return isStage
	})
	if !ok {
		return fmt.Errorf("no DPI stage announcement within %s.\n"+
			"       The mouse only sends one when the stage changes, so tap its DPI button\n"+
			"       while this runs. Nothing is wrong if this stays quiet.", *wait)
	}

	stage, _ := mouse.ParseStage(report)
	fmt.Printf("%s\n", stage)
	fmt.Printf("raw           % x\n", report)
	fmt.Printf("note          the DPI value behind this stage is not reported by the device\n")
	return nil
}

func runStream(args []string) error {
	fs, path := newFlagSet("stream", "Print every status report as it arrives.")
	duration := fs.Duration("duration", 0, "stop after this long (0 runs until interrupted)")
	changesOnly := fs.Bool("changes-only", false, "only print reports that differ from the previous one")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dev, err := openFlagged(*path)
	if err != nil {
		return err
	}
	defer dev.Close()

	fmt.Printf("streaming from %s (ctrl-c to stop)\n", dev.Path())
	start := time.Now()
	var lastBattery *mouse.Battery
	var lastStage *mouse.Stage
	buf := make([]byte, hidraw.ReadBufferSize)
	other := 0

	for {
		if *duration > 0 && time.Since(start) >= *duration {
			break
		}
		n, err := dev.ReadReport(buf, time.Second)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				continue
			}
			return err
		}
		report := buf[:n]
		stamp := time.Since(start).Round(10 * time.Millisecond)

		if battery, ok := mouse.ParseBattery(report); ok {
			if *changesOnly && lastBattery != nil && *lastBattery == battery {
				continue
			}
			lastBattery = &battery
			verb := "battery"
			if battery.Charge == mouse.ChargeCharging {
				verb = "battery (charging)"
			}
			fmt.Printf("[%8s] %-18s %d %%\n", stamp, verb, battery.Percent)
			continue
		}
		if stage, ok := mouse.ParseStage(report); ok {
			if *changesOnly && lastStage != nil && lastStage.Stage == stage.Stage {
				continue
			}
			lastStage = &stage
			note := ""
			if !stage.Valid() {
				note = "  (outside the expected 1-6 range)"
			}
			fmt.Printf("[%8s] %-18s %s%s\n", stamp, "dpi stage", stage, note)
			continue
		}
		other++
	}

	if other > 0 {
		fmt.Printf("\n%d frames on this channel were neither battery nor stage reports (use dump to see them)\n", other)
	}
	return nil
}

func runDump(args []string) error {
	fs, path := newFlagSet("dump", "Hexdump every report arriving on the configuration channel.")
	duration := fs.Duration("duration", 5*time.Second, "how long to listen")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dev, err := openFlagged(*path)
	if err != nil {
		return err
	}
	defer dev.Close()

	fmt.Printf("hexdumping %s for %s\n", dev.Path(), *duration)
	start := time.Now()
	buf := make([]byte, hidraw.ReadBufferSize)
	count := 0

	for {
		remaining := *duration - time.Since(start)
		if remaining <= 0 {
			break
		}
		n, err := dev.ReadReport(buf, remaining)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				break
			}
			return err
		}
		count++
		fmt.Printf("[%8s] len=%d  % x\n",
			time.Since(start).Round(10*time.Millisecond), n, buf[:n])
	}
	fmt.Printf("\n%d reports\n", count)
	return nil
}

package trayapp

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// defaultDPIConf is what "Open DPI config" writes when there is no config file
// yet, so a click always has something to open. It is the annotated template
// shipped as examples/dpi.conf: TestDefaultDPIConfMatchesTheShippedTemplate
// compares the two byte for byte, so the copy the app writes cannot drift from
// the copy tools/install.sh installs.
const defaultDPIConf = `# DPI value of each of the mouse's six stages, in stage order.
#
# Copy this to  ~/.config/attackshark-x11/dpi.conf  and replace the numbers with
# the ones your mouse is actually set to. The tray reads it at startup and
# labels the stage the mouse reports with the matching value here.
#
# There are NO factory numbers in this file on purpose. The mouse never reports
# the DPI behind a stage, and the vendor publishes only the stage count and the
# sensor's ceiling. The vendor's browser-based tool (the "ATTACK SHARK HUB") does
# NOT support this mouse - it requires an output report this receiver does not
# have - so the only trustworthy source is your own mouse, read with the
# vendor's Windows software. See the "DPI values" section of README.md and
# docs/vendor-hub.md.
#
# Reference, from the vendor's product page for the X11:
#   sensor      PixArt PAW3311
#   maximum     22,000 DPI
#   stages      6, all adjustable
# Values must be between 1 and 22000, and all six stages are required; the tray
# refuses to start rather than guess at a partially written file.
#
# Example shape (replace with your real values):
#
# 1=800
# 2=1600
# 3=2400
# 4=3200
# 5=6400
# 6=22000
`

// seedDPIConfig writes the annotated template if path does not exist yet. An
// existing file is left exactly as it is, however empty: it holds the numbers
// the user set for their own mouse, and those cannot be recovered from anywhere
// else.
func seedDPIConfig(path string) error {
	switch _, err := os.Stat(path); {
	case err == nil:
		return nil
	case !errors.Is(err, os.ErrNotExist):
		return err
	}

	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(defaultDPIConf), 0o600)
}

// openDPIConfig opens the per-stage DPI values for editing: the menu action
// behind "Open DPI config". The file is created from the template first if it is
// not there, so the item works on a fresh installation rather than opening an
// editor on a path that does not exist.
func (a *app) openDPIConfig() {
	path := a.opt.DPIConfigPath
	if path == "" {
		a.log("no DPI config path is configured")
		return
	}

	if err := seedDPIConfig(path); err != nil {
		// Reported, but not fatal to the click: the file may be there and
		// merely unstattable, in which case the editor is still the right
		// thing to run.
		a.reportConfigFailure(fmt.Sprintf("Could not create %s: %v", path, err))
	}
	if err := openInEditor(path); err != nil {
		a.reportConfigFailure(fmt.Sprintf("Could not open %s: %v", path, err))
	}
}

// reportConfigFailure tells the user something the menu item could not do. A
// tray has no terminal to print to and a click gives no other feedback, so a
// notification is the reliable channel; the log line is for whoever started the
// app with -v. a.notifier is written once before the event loop starts, so
// reading it here needs no lock.
func (a *app) reportConfigFailure(message string) {
	a.log("%s", message)
	_ = a.notifier.Notify("Attack Shark X11", message)
}

// openInEditor hands path to whatever the desktop opens text files with. A tray
// app has no terminal, so $VISUAL/$EDITOR is only a fallback for a session
// without xdg-utils.
func openInEditor(path string) error {
	if exe, err := exec.LookPath("xdg-open"); err == nil {
		return startDetached(exe, path)
	}
	if editor := editorCommand(); len(editor) > 0 {
		return startDetached(editor[0], append(editor[1:], path)...)
	}
	return errors.New("no xdg-open on PATH and neither $VISUAL nor $EDITOR is set")
}

// startDetached starts a command that outlives the tray app and is not its to
// wait for. The streams are left unset, which sends them to /dev/null: an
// editor that writes to stderr is not the tray's output.
func startDetached(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	// Reaped in the background, so a finished editor cannot linger as a zombie
	// for as long as the tray runs.
	go func() { _ = cmd.Wait() }()
	return nil
}

// editorCommand splits $VISUAL or $EDITOR into a command and its arguments.
// Values like "code -w" are common, and nothing is resolved through a shell:
// the value comes from the user's environment and is passed as an argument
// list, as exec expects.
func editorCommand() []string {
	for _, name := range []string{"VISUAL", "EDITOR"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return strings.Fields(value)
		}
	}
	return nil
}

// Package pointerspeed changes the desktop's pointer speed: the slider the
// desktop's Mouse panel exposes, which the system applies to every pointing
// device through libinput.
//
// This is deliberately not the mouse's DPI. The sensor decides how far it
// travels per inch; how far that moves the cursor is the desktop's decision,
// and on GNOME-family desktops it is one double in GSettings. Writing that key
// changes what the desktop already applies, so the value survives a login and
// does not have to be pushed again whenever the receiver is replugged.
//
// The value is a plain number from -1 (slowest) to 1 (fastest), the same range
// libinput's acceleration speed uses. The tray offers it in steps; this package
// accepts anything inside the range, because the desktop's own slider does too.
package pointerspeed

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const (
	// Min and Max are the bounds the settings schemas allow, and the bounds of
	// libinput's acceleration speed.
	Min = -1.0
	Max = 1.0
	// Step is how far apart the values the tray offers are.
	Step = 0.25
)

// Steps are the values the tray menu offers, slowest first: -1.00 in 0.25
// steps up to 1.00. Every one of them is inside the schema's range and prints
// in two decimals, so the menu and the stored value always agree.
var Steps = func() []float64 {
	steps := make([]float64, 0, 9)
	for i := -4; i <= 4; i++ {
		steps = append(steps, float64(i)/4)
	}
	return steps
}()

// desktop names a settings schema that carries the pointer speed as a double
// called "speed".
type desktop struct {
	schema string
	name   string
}

// desktops are tried in order. Cinnamon's key is first because this app is
// verified there; GNOME's carries the same name and means the same thing, and
// both are applied by the desktop's own pointer handling.
var desktops = []desktop{
	{schema: "org.cinnamon.desktop.peripherals.mouse", name: "Cinnamon"},
	{schema: "org.gnome.desktop.peripherals.mouse", name: "GNOME"},
}

// run invokes gsettings and returns its combined output. It is a variable so
// tests can answer without a session bus or a desktop.
var run = func(args ...string) (string, error) {
	out, err := exec.Command("gsettings", args...).CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), err
	}
	return strings.TrimSpace(string(out)), nil
}

// Backend is one desktop's pointer-speed setting. The zero value is not usable;
// use Detect.
type Backend struct {
	desktop desktop
}

// Detect returns the first desktop whose mouse schema is installed. ok is false
// when neither is, which is the honest answer on a desktop that keeps its
// pointer speed somewhere else: the app then offers no setting rather than
// writing a key nobody reads.
func Detect() (Backend, bool) {
	for _, d := range desktops {
		if _, err := run("get", d.schema, "speed"); err == nil {
			return Backend{desktop: d}, true
		}
	}
	return Backend{}, false
}

// Desktop names where the value comes from, for tooltips: "Cinnamon" or "GNOME".
func (b Backend) Desktop() string { return b.desktop.name }

// Read returns the speed the desktop is currently set to.
func (b Backend) Read() (float64, error) {
	out, err := run("get", b.desktop.schema, "speed")
	if err != nil {
		if out != "" {
			return 0, fmt.Errorf("reading the %s pointer speed: %w: %s", b.desktop.name, err, out)
		}
		return 0, fmt.Errorf("reading the %s pointer speed: %w", b.desktop.name, err)
	}
	value, err := Parse(out)
	if err != nil {
		return 0, fmt.Errorf("reading the %s pointer speed: %w", b.desktop.name, err)
	}
	return value, nil
}

// Set writes the speed. A value outside Min-Max is refused instead of clamped:
// the schema would reject it anyway, and silently turning a request into a
// different number is worse than saying no.
func (b Backend) Set(value float64) error {
	if value < Min || value > Max {
		return fmt.Errorf("%.2f is outside %.2f-%.2f", value, Min, Max)
	}
	if out, err := run("set", b.desktop.schema, "speed", Format(value)); err != nil {
		if out != "" {
			return fmt.Errorf("setting the %s pointer speed: %w: %s", b.desktop.name, err, out)
		}
		return fmt.Errorf("setting the %s pointer speed: %w", b.desktop.name, err)
	}
	return nil
}

// Parse turns what gsettings printed into a number. gsettings prints a GVariant
// double, so the text is a plain decimal such as "0.0" or "-0.5".
func Parse(text string) (float64, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0, errors.New("no value")
	}
	value, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", trimmed)
	}
	if value < Min || value > Max {
		return 0, fmt.Errorf("%.2f is outside %.2f-%.2f", value, Min, Max)
	}
	return value, nil
}

// Format renders a value the way the menu and the logs show it, and the way it
// is written: two decimals, which every step produces exactly.
func Format(value float64) string {
	return strconv.FormatFloat(value, 'f', 2, 64)
}

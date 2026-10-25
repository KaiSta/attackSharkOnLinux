package trayapp

import (
	"fmt"

	"fyne.io/systray"

	"attackshark-x11/internal/pointerspeed"
)

// pointerSpeedBackend is the part of pointerspeed.Backend the tray uses. It is
// an interface so a test can answer without gsettings or a desktop.
type pointerSpeedBackend interface {
	Desktop() string
	Read() (float64, error)
	Set(float64) error
}

// pointerMenuTooltipText explains what the setting is, because "pointer speed"
// is easy to confuse with the mouse's DPI: one is the desktop's cursor, the
// other is the sensor.
func pointerMenuTooltipText(desktop string, value float64, known bool) string {
	base := fmt.Sprintf("How far the cursor moves for a given mouse movement. This is %s's "+
		"own pointer-speed setting: it is shared by every pointing device and is remembered "+
		"across logins. It does not change the mouse's DPI.", desktop)
	if known {
		return fmt.Sprintf("Last read from %s: %s. %s", desktop, pointerspeed.Format(value), base)
	}
	return base
}

func pointerItemTooltip(value float64) string {
	return fmt.Sprintf("Set the desktop's pointer speed to %s. Negative is slower than the default, "+
		"positive is faster.", pointerspeed.Format(value))
}

// startPointerSpeed reads the value the desktop already holds and redraws, so
// the tick describes the desktop rather than a guess from the built menu.
func (a *app) startPointerSpeed() {
	a.readPointerSpeed()
	a.refresh()
}

// readPointerSpeed records the current value. A failure leaves it unknown, which
// is what the menu then says.
func (a *app) readPointerSpeed() {
	if a.pointer == nil {
		return
	}
	value, err := a.pointer.Read()
	if err != nil {
		a.log("could not read the pointer speed: %v", err)
		return
	}
	a.mu.Lock()
	a.pointerValue = value
	a.havePointer = true
	a.mu.Unlock()
	a.log("pointer speed read from %s: %s", a.pointer.Desktop(), pointerspeed.Format(value))
}

// watchPointerSpeedClicks turns a click on one step into a write. Each entry has
// its own goroutine for the same reason the device menus do: a systray menu has
// no single event stream.
func (a *app) watchPointerSpeedClicks() {
	for i, item := range a.pointerItems {
		go a.watchPointerClick(item, pointerspeed.Steps[i])
	}
}

func (a *app) watchPointerClick(item *systray.MenuItem, value float64) {
	for {
		select {
		case <-a.done:
			return
		case _, ok := <-item.ClickedCh:
			if !ok {
				return
			}
			if a.setPointerSpeed(value) {
				a.refresh()
			}
		}
	}
}

// setPointerSpeed writes the value and reads it back, so the tick follows the
// desktop rather than the click. It reports whether anything changed and leaves
// the redraw to the caller, which keeps it testable without a tray.
//
// Writes are serialised: two gsettings calls at once would make the read-back
// ambiguous, exactly as two feature reports would for the mouse.
func (a *app) setPointerSpeed(value float64) bool {
	if a.pointer == nil {
		return false
	}
	a.pointerMu.Lock()
	defer a.pointerMu.Unlock()

	a.mu.Lock()
	current, known := a.pointerValue, a.havePointer
	a.mu.Unlock()
	if known && current == value {
		// Nothing to do, and nothing to report: the tick already says this.
		return false
	}

	if err := a.pointer.Set(value); err != nil {
		a.log("could not set the pointer speed to %s: %v", pointerspeed.Format(value), err)
		a.notifyChange("Cannot change the pointer speed: " + err.Error())
		return false
	}

	readBack, err := a.pointer.Read()
	if err != nil {
		// The write was accepted but cannot be confirmed, so the menu must not
		// claim it: an unknown value is better than a wrong one.
		a.mu.Lock()
		a.havePointer = false
		a.mu.Unlock()
		a.log("set the pointer speed to %s, but could not read it back: %v",
			pointerspeed.Format(value), err)
		a.notifyChange(fmt.Sprintf(
			"Changed the pointer speed to %s, but could not read it back from the desktop",
			pointerspeed.Format(value)))
		return true
	}

	a.mu.Lock()
	a.pointerValue = readBack
	a.havePointer = true
	a.mu.Unlock()
	if readBack != value {
		a.log("asked for pointer speed %s, %s reports %s",
			pointerspeed.Format(value), a.pointer.Desktop(), pointerspeed.Format(readBack))
	} else {
		a.log("pointer speed set to %s (%s)", pointerspeed.Format(readBack), a.pointer.Desktop())
	}
	return true
}

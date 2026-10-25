package trayapp

import (
	"errors"
	"strings"
	"testing"

	"attackshark-x11/internal/pointerspeed"
)

// fakePointerSpeed stands in for gsettings: it records what was written and can
// be told to fail, or to report a different value than the one it was given.
type fakePointerSpeed struct {
	desktop  string
	value    float64
	readBack *float64
	setErr   error
	readErr  error
	writes   []float64
}

func (f *fakePointerSpeed) Desktop() string {
	if f.desktop == "" {
		return "Cinnamon"
	}
	return f.desktop
}

func (f *fakePointerSpeed) Read() (float64, error) {
	if f.readErr != nil {
		return 0, f.readErr
	}
	if f.readBack != nil {
		return *f.readBack, nil
	}
	return f.value, nil
}

func (f *fakePointerSpeed) Set(value float64) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.writes = append(f.writes, value)
	f.value = value
	return nil
}

// pointerApp is the state a running tray would hold once the desktop value has
// been read.
func pointerApp(backend *fakePointerSpeed, value float64) *app {
	return &app{opt: Options{}, pointer: backend, pointerValue: value, havePointer: true}
}

func TestPointerSpeedReadAtStartup(t *testing.T) {
	backend := &fakePointerSpeed{value: -0.25}
	a := &app{opt: Options{}, pointer: backend}

	a.readPointerSpeed()

	if !a.havePointer || a.pointerValue != -0.25 {
		t.Errorf("pointer speed = %v (known %t), want -0.25", a.pointerValue, a.havePointer)
	}
}

// A desktop that cannot be read leaves the value unknown, so the menu says so
// instead of ticking a guess.
func TestPointerSpeedReadFailureLeavesItUnknown(t *testing.T) {
	backend := &fakePointerSpeed{readErr: errors.New("no session bus")}
	a := &app{opt: Options{}, pointer: backend}

	a.readPointerSpeed()

	if a.havePointer {
		t.Errorf("pointer speed = %v, want unknown after a failed read", a.pointerValue)
	}
}

func TestPointerSpeedClickWritesAndFollowsTheReadBack(t *testing.T) {
	backend := &fakePointerSpeed{value: 0}
	a := pointerApp(backend, 0)

	if !a.setPointerSpeed(-0.5) {
		t.Fatal("setPointerSpeed reported no change for -0.50")
	}
	if len(backend.writes) != 1 || backend.writes[0] != -0.5 {
		t.Errorf("writes = %v, want [-0.5]", backend.writes)
	}
	if !a.havePointer || a.pointerValue != -0.5 {
		t.Errorf("pointer speed = %v (known %t), want -0.5", a.pointerValue, a.havePointer)
	}
}

// The tick must describe the desktop, not the click: a desktop that reports
// something else wins.
func TestPointerSpeedShowsWhatTheDesktopReports(t *testing.T) {
	clamped := -0.75
	backend := &fakePointerSpeed{readBack: &clamped}
	a := pointerApp(backend, 0)

	if !a.setPointerSpeed(-0.5) {
		t.Fatal("setPointerSpeed reported no change after a write")
	}
	if a.pointerValue != clamped {
		t.Errorf("pointer speed = %v, want the reported %v", a.pointerValue, clamped)
	}
}

func TestPointerSpeedClickOnTheCurrentValueDoesNothing(t *testing.T) {
	backend := &fakePointerSpeed{value: -0.5}
	a := pointerApp(backend, -0.5)

	if a.setPointerSpeed(-0.5) {
		t.Error("setPointerSpeed changed the menu for a value already set")
	}
	if len(backend.writes) != 0 {
		t.Errorf("wrote %v for a value already set", backend.writes)
	}
}

func TestPointerSpeedRefusedWriteKeepsTheReportedValue(t *testing.T) {
	backend := &fakePointerSpeed{setErr: errors.New("gsettings: permission denied")}
	a := pointerApp(backend, 0.25)

	if a.setPointerSpeed(-0.5) {
		t.Error("setPointerSpeed reported a change after a refused write")
	}
	if a.pointerValue != 0.25 || !a.havePointer {
		t.Errorf("pointer speed = %v (known %t), want the unchanged 0.25", a.pointerValue, a.havePointer)
	}
}

// A write that cannot be read back is not claimed: the menu goes back to
// "unknown" rather than ticking the value that was asked for.
func TestPointerSpeedUnverifiedWriteIsNotClaimed(t *testing.T) {
	backend := &fakePointerSpeed{readErr: errors.New("no session bus")}
	a := pointerApp(backend, 0)

	if !a.setPointerSpeed(-0.5) {
		t.Fatal("setPointerSpeed did not report the write")
	}
	if len(backend.writes) != 1 {
		t.Errorf("writes = %v, want one write", backend.writes)
	}
	if a.havePointer {
		t.Errorf("pointer speed = %v, want unknown after an unverified write", a.pointerValue)
	}
}

func TestPointerSpeedWithoutABackend(t *testing.T) {
	a := &app{opt: Options{}}
	if a.setPointerSpeed(-0.5) {
		t.Error("setPointerSpeed changed something without a backend")
	}
	if a.havePointer {
		t.Error("a backend-less app reports a known pointer speed")
	}
}

func TestPointerMenuTooltipDistinguishesDPIFromPointerSpeed(t *testing.T) {
	known := pointerMenuTooltipText("Cinnamon", -0.5, true)
	for _, want := range []string{"Last read from Cinnamon: -0.50", "DPI", "every pointing device", "remembered across logins"} {
		if !strings.Contains(known, want) {
			t.Errorf("tooltip = %q, want it to contain %q", known, want)
		}
	}

	unknown := pointerMenuTooltipText("GNOME", 0, false)
	if strings.Contains(unknown, "Currently") {
		t.Errorf("tooltip = %q, claims a value that was not read", unknown)
	}
	if !strings.Contains(unknown, "GNOME") {
		t.Errorf("tooltip = %q, want it to name the desktop", unknown)
	}

	if got, want := pointerItemTooltip(0.25), "0.25"; !strings.Contains(got, want) {
		t.Errorf("item tooltip = %q, want it to name %.2f", got, 0.25)
	}
}

// The menu and the package must agree: every step is one the backend accepts.
func TestPointerSpeedStepsAreWritableValues(t *testing.T) {
	for _, step := range pointerspeed.Steps {
		if step < pointerspeed.Min || step > pointerspeed.Max {
			t.Errorf("step %.2f is outside %.2f-%.2f", step, pointerspeed.Min, pointerspeed.Max)
		}
	}
}

package pointerspeed

import (
	"errors"
	"strings"
	"testing"
)

// stub replaces the gsettings call for one test and returns the calls it saw.
func stub(t *testing.T, answer func(args ...string) (string, error)) *[][]string {
	t.Helper()
	var calls [][]string
	previous := run
	run = func(args ...string) (string, error) {
		calls = append(calls, args)
		return answer(args...)
	}
	t.Cleanup(func() { run = previous })
	return &calls
}

func TestStepsCoverTheRange(t *testing.T) {
	if len(Steps) != 9 {
		t.Fatalf("%d steps, want 9", len(Steps))
	}
	if Steps[0] != Min || Steps[len(Steps)-1] != Max {
		t.Errorf("steps run %.2f to %.2f, want %.2f to %.2f",
			Steps[0], Steps[len(Steps)-1], Min, Max)
	}
	for i := 1; i < len(Steps); i++ {
		if got := Steps[i] - Steps[i-1]; got < Step-1e-9 || got > Step+1e-9 {
			t.Errorf("steps %.2f and %.2f are %.3f apart, want %.2f",
				Steps[i-1], Steps[i], got, Step)
		}
	}
	// Every step must survive the round trip through the written form, or the
	// menu tick would not match the value it just wrote.
	for _, step := range Steps {
		parsed, err := Parse(Format(step))
		if err != nil {
			t.Fatalf("Format(%.2f) = %q does not parse back: %v", step, Format(step), err)
		}
		if parsed != step {
			t.Errorf("%.2f formatted as %q and parsed back as %.2f",
				step, Format(step), parsed)
		}
	}
}

func TestFormat(t *testing.T) {
	tests := []struct {
		value float64
		want  string
	}{
		{-1, "-1.00"},
		{-0.5, "-0.50"},
		{-0.25, "-0.25"},
		{0, "0.00"},
		{0.25, "0.25"},
		{1, "1.00"},
	}
	for _, tc := range tests {
		if got := Format(tc.value); got != tc.want {
			t.Errorf("Format(%v) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    float64
		wantErr string
	}{
		{name: "the default", in: "0.0\n", want: 0},
		{name: "slower", in: "-0.5\n", want: -0.5},
		{name: "faster", in: "0.25\n", want: 0.25},
		{name: "padded", in: "  -0.75  ", want: -0.75},
		{name: "bounds", in: "1.0", want: 1},
		{name: "lower bound", in: "-1.0", want: -1},

		{name: "empty", in: "", wantErr: "no value"},
		{name: "not a number", in: "fast", wantErr: "is not a number"},
		{name: "above the range", in: "1.5", wantErr: "outside -1.00-1.00"},
		{name: "below the range", in: "-2", wantErr: "outside -1.00-1.00"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.in)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("Parse(%q) = %v, want an error mentioning %q", tc.in, got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Parse(%q) error = %q, want it to mention %q", tc.in, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) failed: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("Parse(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// Cinnamon is preferred when both schemas exist, because that is the desktop
// this app is verified on, and both keys mean the same thing.
func TestDetectPrefersTheFirstInstalledSchema(t *testing.T) {
	calls := stub(t, func(args ...string) (string, error) {
		if args[1] == "org.cinnamon.desktop.peripherals.mouse" {
			return "0.0\n", nil
		}
		return "", errors.New("exit status 1")
	})

	backend, ok := Detect()
	if !ok {
		t.Fatal("Detect found no backend although Cinnamon's answers")
	}
	if got := backend.Desktop(); got != "Cinnamon" {
		t.Errorf("Desktop() = %q, want Cinnamon", got)
	}
	if len(*calls) != 1 {
		t.Errorf("Detect made %d gsettings call(s), want 1: %v", len(*calls), *calls)
	}
}

func TestDetectFallsBackToGNOME(t *testing.T) {
	stub(t, func(args ...string) (string, error) {
		if args[1] == "org.gnome.desktop.peripherals.mouse" {
			return "0.0\n", nil
		}
		return "", errors.New("exit status 1")
	})

	backend, ok := Detect()
	if !ok {
		t.Fatal("Detect did not fall back to GNOME")
	}
	if got := backend.Desktop(); got != "GNOME" {
		t.Errorf("Desktop() = %q, want GNOME", got)
	}
}

// A desktop that keeps its pointer speed elsewhere must get no backend at all,
// rather than a menu entry that writes a key nothing reads.
func TestDetectWithoutASupportedDesktop(t *testing.T) {
	stub(t, func(args ...string) (string, error) {
		return "No such schema", errors.New("exit status 1")
	})

	if backend, ok := Detect(); ok {
		t.Fatalf("Detect found %q on a desktop with neither schema", backend.Desktop())
	}
}

func TestReadAndSetUseTheDetectedSchema(t *testing.T) {
	calls := stub(t, func(args ...string) (string, error) {
		switch args[0] {
		case "get":
			return "-0.5\n", nil
		case "set":
			return "", nil
		}
		return "", errors.New("unexpected command")
	})

	backend, ok := Detect()
	if !ok {
		t.Fatal("Detect failed with a stubbed gsettings")
	}

	got, err := backend.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != -0.5 {
		t.Errorf("Read() = %v, want -0.5", got)
	}

	if err := backend.Set(-0.25); err != nil {
		t.Fatalf("Set: %v", err)
	}

	last := (*calls)[len(*calls)-1]
	want := []string{"set", "org.cinnamon.desktop.peripherals.mouse", "speed", "-0.25"}
	if len(last) != len(want) {
		t.Fatalf("Set called gsettings with %v, want %v", last, want)
	}
	for i := range want {
		if last[i] != want[i] {
			t.Fatalf("Set called gsettings with %v, want %v", last, want)
		}
	}
}

// Refusing is better than clamping: a click that asked for 2.00 must not quietly
// become the fastest value.
func TestSetRefusesValuesOutsideTheRange(t *testing.T) {
	calls := stub(t, func(args ...string) (string, error) { return "0.0\n", nil })
	backend, _ := Detect()
	before := len(*calls)

	for _, value := range []float64{-1.5, 1.5, 2} {
		if err := backend.Set(value); err == nil {
			t.Errorf("Set(%v) succeeded", value)
		}
	}
	if len(*calls) != before {
		t.Errorf("Set called gsettings for a refused value: %v", (*calls)[before:])
	}
}

func TestReadReportsWhatGsettingsSaid(t *testing.T) {
	stub(t, func(args ...string) (string, error) {
		if args[0] == "get" {
			return "0.0\n", nil
		}
		return "", nil
	})
	backend, _ := Detect()
	if value, err := backend.Read(); err != nil || value != 0 {
		t.Fatalf("Read() = %v, %v; want 0, nil", value, err)
	}

	stub(t, func(args ...string) (string, error) {
		return "No such schema", errors.New("exit status 1")
	})
	broken := Backend{desktop: desktops[0]}
	_, err := broken.Read()
	if err == nil {
		t.Fatal("Read succeeded after gsettings failed")
	}
	if !strings.Contains(err.Error(), "Cinnamon") || !strings.Contains(err.Error(), "No such schema") {
		t.Errorf("Read error = %q, want it to name the desktop and carry gsettings' message", err)
	}
}

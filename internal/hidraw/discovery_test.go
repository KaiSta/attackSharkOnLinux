package hidraw

import (
	"errors"
	"testing"
)

func TestMatchHIDID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{"receiver, kernel uppercase hex", "0003:00001D57:0000FA60", true},
		{"receiver, lowercase hex", "0003:00001d57:0000fa60", true},
		{"receiver, surrounding space", " 0003:00001D57:0000FA60 ", true},
		{"wrong vendor", "0003:00001D58:0000FA60", false},
		{"wrong product", "0003:00001D57:0000FA61", false},
		{"bluetooth bus, same ids", "0005:00001D57:0000FA60", true},
		{"empty", "", false},
		{"garbage", "not-a-hid-id", false},
		{"missing product", "0003:00001D57", false},
		{"too many fields", "0003:00001D57:0000FA60:00", false},
		{"non-hex", "0003:zzzz:0000FA60", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchHIDID(tc.id); got != tc.want {
				t.Errorf("matchHIDID(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}

func TestInterfaceFromPhys(t *testing.T) {
	tests := []struct {
		phys      string
		want      int
		wantOK    bool
		extraNote string
	}{
		{phys: "usb-0000:07:00.0-3/input2", want: 2, wantOK: true},
		{phys: "usb-0000:07:00.0-3/input0", want: 0, wantOK: true},
		{phys: "usb-0000:00:14.0-1/input4", want: 4, wantOK: true},
		{phys: "usb-0000:07:00.0-3/input2/input0", want: 0, wantOK: true, extraNote: "nested input nodes report the innermost"},
		{phys: "usb-0000:07:00.0-3", want: 0, wantOK: false, extraNote: "no input suffix"},
		{phys: "", want: 0, wantOK: false},
		{phys: "input", want: 0, wantOK: false, extraNote: "no number"},
		{phys: "inputX", want: 0, wantOK: false},
	}
	for _, tc := range tests {
		name := tc.phys
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			got, ok := interfaceFromPhys(tc.phys)
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("interfaceFromPhys(%q) = (%d, %v), want (%d, %v)",
					tc.phys, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestNodeNumber(t *testing.T) {
	tests := map[string]int{
		"/sys/class/hidraw/hidraw0":  0,
		"/sys/class/hidraw/hidraw12": 12,
		"/sys/class/hidraw/hidraw":   -1,
		"/sys/class/hidraw/":         -1,
		"hidraw3":                    3,
	}
	for path, want := range tests {
		if got := nodeNumber(path); got != want {
			t.Errorf("nodeNumber(%q) = %d, want %d", path, got, want)
		}
	}
}

// TestDevicesShapeOnly keeps the sysfs scan honest without assuming the
// receiver is plugged in: if it finds nodes they must carry a path, and the
// configuration interface must be sorted first when it is present.
func TestDevicesShapeOnly(t *testing.T) {
	candidates, err := Devices()
	if err != nil {
		// Expected when the receiver is unplugged; the error must still be
		// recognisable as "not found" rather than a sysfs failure.
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("Devices() failed for a reason other than a missing receiver: %v", err)
		}
		t.Skipf("receiver not present: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("Devices returned no candidates and no error")
	}
	for _, c := range candidates {
		if c.Path == "" {
			t.Errorf("candidate %+v has an empty path", c)
		}
		if c.Interface < -1 {
			t.Errorf("candidate %+v has an invalid interface number", c)
		}
	}
	if len(candidates) > 1 && candidates[0].Interface != ConfigInterface {
		t.Errorf("configuration interface not sorted first: %+v", candidates)
	}
}

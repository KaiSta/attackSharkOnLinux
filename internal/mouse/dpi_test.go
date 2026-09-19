package mouse

import (
	"strings"
	"testing"
)

func TestParseDPIStages(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr string
	}{
		{name: "comma separated", in: "800,1600,2400,3200,6400,22000", want: "800/1600/2400/3200/6400/22000"},
		{name: "spaces", in: "800 1600 2400 3200 6400 22000", want: "800/1600/2400/3200/6400/22000"},
		{name: "slashes", in: "800/1600/2400/3200/6400/22000", want: "800/1600/2400/3200/6400/22000"},
		{name: "mixed and padded", in: " 800, 1600;2400 3200\t6400\n22000 ", want: "800/1600/2400/3200/6400/22000"},
		{name: "all equal is allowed", in: "1600,1600,1600,1600,1600,1600", want: "1600/1600/1600/1600/1600/1600"},
		{name: "descending is allowed", in: "22000,6400,3200,2400,1600,800", want: "22000/6400/3200/2400/1600/800"},
		{name: "lowest accepted", in: "1,1,1,1,1,1", want: "1/1/1/1/1/1"},
		{name: "highest accepted", in: "22000,22000,22000,22000,22000,22000", want: "22000/22000/22000/22000/22000/22000"},

		{name: "too few", in: "800,1600,2400", wantErr: "got 3"},
		{name: "too many", in: "800,1600,2400,3200,6400,22000,44000", wantErr: "got 7"},
		{name: "empty", in: "", wantErr: "got 0"},
		{name: "not a number", in: "800,1600,x,3200,6400,22000", wantErr: "is not a number"},
		{name: "zero rejected", in: "0,1600,2400,3200,6400,22000", wantErr: "outside 1-22000"},
		{name: "negative rejected", in: "-800,1600,2400,3200,6400,22000", wantErr: "outside 1-22000"},
		{name: "above sensor maximum", in: "800,1600,2400,3200,6400,44000", wantErr: "outside 1-22000"},
		{name: "float rejected", in: "800.5,1600,2400,3200,6400,22000", wantErr: "is not a number"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDPIStages(tc.in)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("ParseDPIStages(%q) succeeded with %q, want an error mentioning %q",
						tc.in, got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ParseDPIStages(%q) error = %q, want it to mention %q",
						tc.in, err, tc.wantErr)
				}
				// A rejected line must not come back half applied: a caller
				// that ignored the error would show invented numbers.
				if got.Configured() {
					t.Fatalf("ParseDPIStages(%q) returned %q alongside its error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDPIStages(%q) failed: %v", tc.in, err)
			}
			if got.String() != tc.want {
				t.Errorf("ParseDPIStages(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDPIStagesZeroValueIsUnconfigured(t *testing.T) {
	var none DPIStages
	if none.Configured() {
		t.Error("zero DPIStages reports itself as configured")
	}
	if got := none.String(); got != "" {
		t.Errorf("zero DPIStages String() = %q, want empty", got)
	}
	for stage := 1; stage <= MaxStage; stage++ {
		if value, ok := none.At(stage); ok {
			t.Errorf("At(%d) = (%d, true) on an unconfigured mapping", stage, value)
		}
		if got := none.StageDescription(stage); got != "" {
			t.Errorf("StageDescription(%d) = %q, want empty", stage, got)
		}
	}
}

func TestDPIStagesLookups(t *testing.T) {
	stages, err := ParseDPIStages("800,1600,2400,3200,6400,22000")
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if !stages.Configured() {
		t.Fatal("parsed mapping reports itself as unconfigured")
	}

	for stage := 1; stage <= MaxStage; stage++ {
		want := []int{800, 1600, 2400, 3200, 6400, 22000}[stage-1]
		got, ok := stages.At(stage)
		if !ok || got != want {
			t.Errorf("At(%d) = (%d, %v), want (%d, true)", stage, got, ok, want)
		}
		if desc := stages.StageDescription(stage); desc != itoa(want)+" DPI" {
			t.Errorf("StageDescription(%d) = %q, want %q", stage, desc, itoa(want)+" DPI")
		}
	}

	for _, stage := range []int{0, -1, MaxStage + 1, 99} {
		if value, ok := stages.At(stage); ok {
			t.Errorf("At(%d) = (%d, true), want no value", stage, value)
		}
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}

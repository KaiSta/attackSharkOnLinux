package mouse

import (
	"strconv"
	"strings"
	"testing"
)

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

func TestNewDPIStages(t *testing.T) {
	tests := []struct {
		name    string
		in      [MaxStage]int
		want    string
		wantErr string
	}{
		{name: "the vendor's spread", in: [MaxStage]int{800, 1600, 2400, 3200, 6400, 22000}, want: "800/1600/2400/3200/6400/22000"},
		{name: "all equal is allowed", in: [MaxStage]int{1600, 1600, 1600, 1600, 1600, 1600}, want: "1600/1600/1600/1600/1600/1600"},
		{name: "descending is allowed", in: [MaxStage]int{22000, 6400, 3200, 2400, 1600, 800}, want: "22000/6400/3200/2400/1600/800"},
		{name: "lowest accepted", in: [MaxStage]int{1, 1, 1, 1, 1, 1}, want: "1/1/1/1/1/1"},
		{name: "highest accepted", in: [MaxStage]int{22000, 22000, 22000, 22000, 22000, 22000}, want: "22000/22000/22000/22000/22000/22000"},

		{name: "zero rejected", in: [MaxStage]int{0, 1600, 2400, 3200, 6400, 22000}, wantErr: "outside 1-22000"},
		{name: "negative rejected", in: [MaxStage]int{-800, 1600, 2400, 3200, 6400, 22000}, wantErr: "outside 1-22000"},
		{name: "above sensor maximum", in: [MaxStage]int{800, 1600, 2400, 3200, 6400, 44000}, wantErr: "outside 1-22000"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NewDPIStages(tc.in)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("NewDPIStages(%v) succeeded with %q, want an error mentioning %q",
						tc.in, got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("NewDPIStages(%v) error = %q, want it to mention %q",
						tc.in, err, tc.wantErr)
				}
				// A rejected set must not come back half applied: a caller
				// that ignored the error would show invented numbers.
				if got.Configured() {
					t.Fatalf("NewDPIStages(%v) returned %q alongside its error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewDPIStages(%v) failed: %v", tc.in, err)
			}
			if got.String() != tc.want {
				t.Errorf("NewDPIStages(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDPIStagesLookups(t *testing.T) {
	values := [MaxStage]int{800, 1600, 2400, 3200, 6400, 22000}
	stages, err := NewDPIStages(values)
	if err != nil {
		t.Fatalf("building the mapping: %v", err)
	}
	if !stages.Configured() {
		t.Fatal("a complete mapping reports itself as unconfigured")
	}

	for stage := 1; stage <= MaxStage; stage++ {
		want := values[stage-1]
		got, ok := stages.At(stage)
		if !ok || got != want {
			t.Errorf("At(%d) = (%d, %v), want (%d, true)", stage, got, ok, want)
		}
		if desc := stages.StageDescription(stage); desc != strconv.Itoa(want)+" DPI" {
			t.Errorf("StageDescription(%d) = %q, want %q", stage, desc, strconv.Itoa(want)+" DPI")
		}
	}

	for _, stage := range []int{0, -1, MaxStage + 1, 99} {
		if value, ok := stages.At(stage); ok {
			t.Errorf("At(%d) = (%d, true), want no value", stage, value)
		}
	}
}

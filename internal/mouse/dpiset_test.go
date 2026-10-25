package mouse

import (
	"fmt"
	"testing"
)

// Every value the menu offers has to have a code, and that code has to read back
// as the value that was asked for.
func TestEncodeDPIRoundTripsTheMenusSteps(t *testing.T) {
	for value := 2000; value <= MaxDPI; value += 2000 {
		x, y, double, ok := EncodeDPI(value)
		if !ok {
			t.Fatalf("EncodeDPI(%d) found no code", value)
		}
		got, decodes := DecodeDPI(x, y, double)
		if !decodes || got != value {
			t.Fatalf("EncodeDPI(%d) = %#x, %#x, double %t, which decodes to %d (%t)",
				value, x, y, double, got, decodes)
		}
	}
}

// The encoding must reproduce the codes this receiver was found holding, so that
// setting a value back to what it was writes the same bytes.
func TestEncodeDPIMatchesTheStoredCodes(t *testing.T) {
	tests := []struct {
		value  int
		x, y   byte
		double bool
	}{
		{800, 0x12, 0x00, false},
		{1600, 0x25, 0x00, false},
		{2400, 0x38, 0x00, false},
		{3200, 0x4b, 0x00, false},
		{5000, 0x75, 0x00, false},
		{22000, 0x81, 0x01, true},
	}
	for _, tt := range tests {
		x, y, double, ok := EncodeDPI(tt.value)
		if !ok {
			t.Errorf("EncodeDPI(%d) found no code", tt.value)
			continue
		}
		if x != tt.x || y != tt.y || double != tt.double {
			t.Errorf("EncodeDPI(%d) = %#x, %#x, double %t; want %#x, %#x, double %t",
				tt.value, x, y, double, tt.x, tt.y, tt.double)
		}
	}
}

func TestEncodeDPIRejectsValuesOutsideTheSensor(t *testing.T) {
	for _, value := range []int{0, -2000, MaxDPI + 2000, 100000} {
		if _, _, _, ok := EncodeDPI(value); ok {
			t.Errorf("EncodeDPI(%d) found a code", value)
		}
	}
}

func TestBuildDPIChangeOnlyChangesThatStage(t *testing.T) {
	before := dpiReport(defaultStages(), 4)
	cfg, err := ParseDPIConfig(before)
	if err != nil {
		t.Fatalf("ParseDPIConfig: %v", err)
	}

	frame, err := BuildDPIChange(cfg, 2, 6000)
	if err != nil {
		t.Fatalf("BuildDPIChange: %v", err)
	}

	if got, want := frame[8+1], dpi3311Table[(6000-50)/50]; got != want {
		t.Errorf("stage 2 code byte = %#x, want %#x", got, want)
	}
	if got := frame[16+1]; got != 0x00 {
		t.Errorf("stage 2 second byte = %#x, want 0x00", got)
	}
	if frame[6]&(1<<1) != 0 {
		t.Error("stage 2 double-speed flag was set for 6000 DPI")
	}
	for i := range frame {
		switch i {
		case 9, 17, 50, 51: // stage 2's two code bytes and the checksum
			continue
		}
		if frame[i] != before[i] {
			t.Errorf("byte %d changed from %#x to %#x", i, before[i], frame[i])
		}
	}
	if cfg.Raw[9] != before[9] {
		t.Error("BuildDPIChange modified the source report")
	}

	after, err := ParseDPIConfig(frame)
	if err != nil {
		t.Fatalf("the frame the device would receive does not parse: %v", err)
	}
	if got, _ := after.Stages.At(2); got != 6000 {
		t.Errorf("written report has stage 2 = %d, want 6000", got)
	}
	if got, _ := after.Stages.At(4); got != 3200 {
		t.Errorf("written report changed stage 4 to %d", got)
	}
	if after.ActiveStage != 4 {
		t.Errorf("written report selects stage %d, want 4", after.ActiveStage)
	}
}

func TestBuildDPIChangeSetsAndClearsTheDoubleFlag(t *testing.T) {
	cfg, err := ParseDPIConfig(dpiReport(defaultStages(), 6))
	if err != nil {
		t.Fatalf("ParseDPIConfig: %v", err)
	}
	if cfg.Stages.values[5] != 22000 {
		t.Fatalf("test fixture stage 6 = %d, want 22000", cfg.Stages.values[5])
	}

	// Leaving 22,000 must clear the bit, not leave it set over a new code.
	frame, err := BuildDPIChange(cfg, 6, 2000)
	if err != nil {
		t.Fatalf("BuildDPIChange: %v", err)
	}
	if frame[6]&(1<<5) != 0 {
		t.Error("the double-speed flag survived lowering stage 6 to 2000 DPI")
	}
	after, err := ParseDPIConfig(frame)
	if err != nil {
		t.Fatalf("ParseDPIConfig after lowering: %v", err)
	}
	if got, _ := after.Stages.At(6); got != 2000 {
		t.Errorf("stage 6 = %d, want 2000", got)
	}

	// And setting it back must set the bit again.
	frame, err = BuildDPIChange(after, 6, 22000)
	if err != nil {
		t.Fatalf("BuildDPIChange back to 22000: %v", err)
	}
	if frame[6]&(1<<5) == 0 {
		t.Error("the double-speed flag was not set for 22000 DPI")
	}
}

func TestBuildDPIChangeRejectsBadInput(t *testing.T) {
	cfg, err := ParseDPIConfig(dpiReport(defaultStages(), 4))
	if err != nil {
		t.Fatalf("ParseDPIConfig: %v", err)
	}

	for _, stage := range []int{0, -1, MaxStage + 1} {
		if _, err := BuildDPIChange(cfg, stage, 4000); err == nil {
			t.Errorf("BuildDPIChange accepted stage %d", stage)
		}
	}
	for _, value := range []int{0, MaxDPI + 1} {
		if _, err := BuildDPIChange(cfg, 1, value); err == nil {
			t.Errorf("BuildDPIChange accepted %d DPI", value)
		}
	}

	short := cfg
	short.Raw = cfg.Raw[:DPIReportPayload]
	if _, err := BuildDPIChange(short, 1, 4000); err == nil {
		t.Errorf("BuildDPIChange accepted a %d-byte report", len(short.Raw))
	}
}

func TestApplyDPIValueWritesAndReturnsTheNewConfiguration(t *testing.T) {
	zeroDelay(t)
	before := dpiReport(defaultStages(), 4)
	dev := &fakeFeature{grant: true, dpi: before}

	cfg, err := ApplyDPIValue(dev, 4, 4000)
	if err != nil {
		t.Fatalf("ApplyDPIValue: %v", err)
	}
	if got, _ := cfg.Stages.At(4); got != 4000 {
		t.Errorf("returned configuration has stage 4 = %d, want 4000", got)
	}
	if got, _ := cfg.Stages.At(1); got != 800 {
		t.Errorf("returned configuration has stage 1 = %d, want the original 800", got)
	}

	last := dev.writes[len(dev.writes)-1]
	if got, want := fmt.Sprintf("% x", last), fmt.Sprintf("% x", cfg.Raw); got != want {
		t.Errorf("last write = %s, want the returned report %s", got, want)
	}
	written, err := ParseDPIConfig(last)
	if err != nil {
		t.Fatalf("the written frame does not parse: %v", err)
	}
	if got, _ := written.Stages.At(4); got != 4000 {
		t.Errorf("written frame has stage 4 = %d, want 4000", got)
	}
	if written.ActiveStage != 4 || written.Profile != 1 {
		t.Errorf("written frame selects stage %d of profile %d, want 4 of 1",
			written.ActiveStage, written.Profile)
	}
}

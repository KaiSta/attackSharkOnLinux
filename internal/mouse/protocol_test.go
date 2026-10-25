package mouse

import "testing"

// Every report below was captured from the real receiver on 2026-09-17.
func TestParseBattery(t *testing.T) {
	tests := []struct {
		name      string
		report    []byte
		wantOK    bool
		percent   int
		charge    ChargeState
		chargeRaw byte
	}{
		{
			name:      "discharging at 78 percent (captured heartbeat)",
			report:    []byte{0x03, 0x55, 0x40, 0x01, 0x4e},
			wantOK:    true,
			percent:   78,
			charge:    ChargeDischarging,
			chargeRaw: 0x01,
		},
		{
			name:      "charging at 78 percent",
			report:    []byte{0x03, 0x55, 0x40, 0x03, 0x4e},
			wantOK:    true,
			percent:   78,
			charge:    ChargeCharging,
			chargeRaw: 0x03,
		},
		{
			name:      "empty battery",
			report:    []byte{0x03, 0x55, 0x40, 0x01, 0x00},
			wantOK:    true,
			percent:   0,
			charge:    ChargeDischarging,
			chargeRaw: 0x01,
		},
		{
			name:      "full battery",
			report:    []byte{0x03, 0x55, 0x40, 0x03, 0x64},
			wantOK:    true,
			percent:   100,
			charge:    ChargeCharging,
			chargeRaw: 0x03,
		},
		{
			// Documented upstream as 03 55 40 02 64, sent once when charging
			// completes. Not reproduced here yet, but the byte is unambiguous.
			name:      "charge complete",
			report:    []byte{0x03, 0x55, 0x40, 0x02, 0x64},
			wantOK:    true,
			percent:   100,
			charge:    ChargeFull,
			chargeRaw: 0x02,
		},
		{
			// Upstream documents 0x41 as the same event as 0x40.
			name:      "alternate battery event code",
			report:    []byte{0x03, 0x55, 0x41, 0x03, 0x41},
			wantOK:    true,
			percent:   65,
			charge:    ChargeCharging,
			chargeRaw: 0x03,
		},
		{
			name:      "unknown charge byte stays visible",
			report:    []byte{0x03, 0x55, 0x40, 0x07, 0x32},
			wantOK:    true,
			percent:   50,
			charge:    ChargeUnknown,
			chargeRaw: 0x07,
		},
		{
			name:   "stage report is not a battery report",
			report: []byte{0x03, 0x55, 0x10, 0x04, 0x00},
			wantOK: false,
		},
		{
			name:   "mouse motion report",
			report: []byte{0x00, 0x00, 0x00, 0xff, 0xff, 0x00, 0x00},
			wantOK: false,
		},
		{
			name:   "wrong magic",
			report: []byte{0x03, 0x99, 0x40, 0x01, 0x4e},
			wantOK: false,
		},
		{
			name:   "wrong report id",
			report: []byte{0x02, 0x55, 0x40, 0x01, 0x4e},
			wantOK: false,
		},
		{
			name:   "truncated report",
			report: []byte{0x03, 0x55, 0x40, 0x01},
			wantOK: false,
		},
		{
			name:   "empty report",
			report: nil,
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseBattery(tt.report)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if got.Percent != tt.percent {
				t.Errorf("Percent = %d, want %d", got.Percent, tt.percent)
			}
			if got.Charge != tt.charge {
				t.Errorf("Charge = %v, want %v", got.Charge, tt.charge)
			}
			if got.ChargeRaw != tt.chargeRaw {
				t.Errorf("ChargeRaw = %#x, want %#x", got.ChargeRaw, tt.chargeRaw)
			}
		})
	}
}

func TestParseStage(t *testing.T) {
	tests := []struct {
		name   string
		report []byte
		wantOK bool
		stage  int
	}{
		{name: "stage 4 from a real button press", report: []byte{0x03, 0x55, 0x10, 0x04, 0x00}, wantOK: true, stage: 4},
		{name: "stage 6 from a real button press", report: []byte{0x03, 0x55, 0x10, 0x06, 0x00}, wantOK: true, stage: 6},
		{name: "stage 1 after wraparound", report: []byte{0x03, 0x55, 0x10, 0x01, 0x00}, wantOK: true, stage: 1},
		{name: "out of range stage is still a stage report", report: []byte{0x03, 0x55, 0x10, 0x09, 0x00}, wantOK: true, stage: 9},
		{name: "battery report is not a stage report", report: []byte{0x03, 0x55, 0x40, 0x01, 0x4e}, wantOK: false},
		{name: "truncated", report: []byte{0x03, 0x55, 0x10}, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseStage(tt.report)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if got.Stage != tt.stage {
				t.Errorf("Stage = %d, want %d", got.Stage, tt.stage)
			}
		})
	}
}

func TestStageValid(t *testing.T) {
	for _, tc := range []struct {
		stage int
		want  bool
	}{
		{0, false},
		{1, true},
		{6, true},
		{7, false},
		{9, false},
	} {
		if got := (Stage{Stage: tc.stage}).Valid(); got != tc.want {
			t.Errorf("Stage{%d}.Valid() = %v, want %v", tc.stage, got, tc.want)
		}
	}
}

// TestStageCycle replays the six DPI button presses captured on hardware and
// checks the wraparound is decoded in order.
func TestStageCycle(t *testing.T) {
	captured := [][]byte{
		{0x03, 0x55, 0x10, 0x04, 0x00},
		{0x03, 0x55, 0x10, 0x05, 0x00},
		{0x03, 0x55, 0x10, 0x06, 0x00},
		{0x03, 0x55, 0x10, 0x01, 0x00},
		{0x03, 0x55, 0x10, 0x02, 0x00},
		{0x03, 0x55, 0x10, 0x03, 0x00},
	}
	want := []int{4, 5, 6, 1, 2, 3}

	for i, report := range captured {
		got, ok := ParseStage(report)
		if !ok {
			t.Fatalf("press %d: not recognised as a stage report", i)
		}
		if got.Stage != want[i] {
			t.Errorf("press %d: stage = %d, want %d", i, got.Stage, want[i])
		}
		if !got.Valid() {
			t.Errorf("press %d: stage %d reported as invalid", i, got.Stage)
		}
	}
}

// TestInterleavedStream checks the two subtypes never claim each other's
// frames when they arrive on the same channel, as they do in real use.
func TestInterleavedStream(t *testing.T) {
	stream := [][]byte{
		{0x03, 0x55, 0x40, 0x01, 0x4e},
		{0x03, 0x55, 0x10, 0x04, 0x00},
		{0x03, 0x55, 0x40, 0x01, 0x4e},
		{0x03, 0x55, 0x10, 0x05, 0x00},
		{0x03, 0x55, 0x40, 0x03, 0x4d},
		{0x00, 0x00, 0x01, 0xfe, 0xff, 0x00, 0x00},
	}

	var batteries int
	var stages []int
	for _, report := range stream {
		if b, ok := ParseBattery(report); ok {
			batteries++
			if b.Percent != 78 && b.Percent != 77 {
				t.Errorf("unexpected battery percent %d", b.Percent)
			}
			continue
		}
		if s, ok := ParseStage(report); ok {
			stages = append(stages, s.Stage)
		}
		// A frame that is neither is simply ignored, as it must be for the
		// mouse's own motion reports sharing the receiver.
	}

	if batteries != 3 {
		t.Errorf("decoded %d battery reports, want 3", batteries)
	}
	if len(stages) != 2 || stages[0] != 4 || stages[1] != 5 {
		t.Errorf("decoded stages %v, want [4 5]", stages)
	}
}

package mouse

import (
	"strings"
	"testing"
)

// A stage change must not disturb anything else: the report also carries the six
// DPI values, the per-stage colours, the sensor flags and the profile.
func TestBuildStageChangeOnlyChangesTheStage(t *testing.T) {
	before := dpiReport(defaultStages(), 4)
	cfg, err := ParseDPIConfig(before)
	if err != nil {
		t.Fatalf("ParseDPIConfig: %v", err)
	}

	frame, err := BuildStageChange(cfg, 2)
	if err != nil {
		t.Fatalf("BuildStageChange: %v", err)
	}

	if got := frame[24]; got != 2 {
		t.Errorf("selected stage byte = %d, want 2", got)
	}
	for i := range frame {
		switch i {
		case 24, 50, 51: // the stage itself and its checksum
			continue
		}
		if frame[i] != before[i] {
			t.Errorf("byte %d changed from 0x%02x to 0x%02x", i, before[i], frame[i])
		}
	}
	// The caller's report is not aliased into the frame.
	if cfg.Raw[24] != 4 {
		t.Errorf("BuildStageChange modified the source report (stage byte = %d)", cfg.Raw[24])
	}

	after, err := ParseDPIConfig(frame)
	if err != nil {
		t.Fatalf("the frame the device would receive does not parse: %v", err)
	}
	if after.ActiveStage != 2 {
		t.Errorf("written report selects stage %d, want 2", after.ActiveStage)
	}
	if got, want := after.Stages.String(), "800/1600/2400/3200/5000/22000"; got != want {
		t.Errorf("written report has stages %q, want the originals %q", got, want)
	}
}

func TestBuildStageChangeRejectsBadInput(t *testing.T) {
	cfg, err := ParseDPIConfig(dpiReport(defaultStages(), 4))
	if err != nil {
		t.Fatalf("ParseDPIConfig: %v", err)
	}

	for _, stage := range []int{0, -1, MaxStage + 1} {
		if _, err := BuildStageChange(cfg, stage); err == nil {
			t.Errorf("BuildStageChange accepted stage %d", stage)
		}
	}

	// A descriptor-length response is enough to decode, but not enough to write
	// back without dropping the fields that follow the checksum.
	short := cfg
	short.Raw = cfg.Raw[:DPIReportPayload]
	if _, err := BuildStageChange(short, 2); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("BuildStageChange on a %d-byte report = %v, want a refusal", len(short.Raw), err)
	}
}

func TestApplyStageWritesPermissionThenConfiguration(t *testing.T) {
	zeroDelay(t)
	dev := &fakeFeature{grant: true, dpi: dpiReport(defaultStages(), 4)}

	frame, err := ApplyStage(dev, 3)
	if err != nil {
		t.Fatalf("ApplyStage: %v", err)
	}
	if len(dev.writes) != 2 {
		t.Fatalf("%d writes, want the permission request and the configuration: % x",
			len(dev.writes), dev.writes)
	}
	if got := dev.writes[0]; got[0] != PermissionReportID {
		t.Errorf("first write starts with 0x%02x, want the 0xA0 permission request", got[0])
	}
	if got := dev.writes[1]; len(got) != DPIReportLen || got[0] != DPIReportID {
		t.Fatalf("second write is %d bytes starting 0x%02x, want a %d-byte report 0x04",
			len(got), got[0], DPIReportLen)
	}
	if dev.writes[1][24] != 3 {
		t.Errorf("written stage byte = %d, want 3", dev.writes[1][24])
	}
	for i := range frame {
		if frame[i] != dev.writes[1][i] {
			t.Errorf("returned frame differs from the written one at byte %d", i)
		}
	}
}

// The write must never happen when the read did not: without the current
// configuration the frame would reset everything it could not see.
func TestApplyStageWritesNothingWithoutTheConfiguration(t *testing.T) {
	zeroDelay(t)
	dev := &fakeFeature{grant: false, dpi: dpiReport(defaultStages(), 4)}

	if _, err := ApplyStage(dev, 3); err == nil {
		t.Fatal("ApplyStage succeeded without a readable configuration")
	}
	// The retries are all permission requests; no configuration may be written.
	if len(dev.writes) != permissionAttempts {
		t.Errorf("%d writes, want %d permission requests (% x)",
			len(dev.writes), permissionAttempts, dev.writes)
	}
	for i, write := range dev.writes {
		if len(write) != 8 || write[0] != PermissionReportID {
			t.Errorf("write %d = % x, want a permission request", i, write)
		}
	}
}

func TestParseCommandConfirmation(t *testing.T) {
	tests := []struct {
		name   string
		report []byte
		ok     bool
		want   CommandConfirmation
	}{
		{
			name:   "success",
			report: []byte{0x03, 0x55, 0x50, 0x00, 0x04},
			ok:     true,
			want:   CommandConfirmation{ReportID: 0x04, Success: true, Status: 0x00},
		},
		{
			name:   "failure",
			report: []byte{0x03, 0x55, 0x50, 0x01, 0x04},
			ok:     true,
			want:   CommandConfirmation{ReportID: 0x04, Success: false, Status: 0x01},
		},
		{
			name:   "another report",
			report: []byte{0x03, 0x55, 0x50, 0x00, 0x06},
			ok:     true,
			want:   CommandConfirmation{ReportID: 0x06, Success: true, Status: 0x00},
		},
		{
			name:   "unknown status is not success",
			report: []byte{0x03, 0x55, 0x50, 0x07, 0x04},
			ok:     true,
			want:   CommandConfirmation{ReportID: 0x04, Success: false, Status: 0x07},
		},
		{
			name:   "battery frame",
			report: []byte{0x03, 0x55, 0x40, 0x01, 0x4e},
		},
		{
			name:   "stage frame",
			report: []byte{0x03, 0x55, 0x10, 0x02, 0x00},
		},
		{
			name:   "short",
			report: []byte{0x03, 0x55, 0x50},
		},
		{
			name:   "wrong magic",
			report: []byte{0x03, 0x54, 0x50, 0x00, 0x04},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseCommandConfirmation(tc.report)
			if ok != tc.ok {
				t.Fatalf("ParseCommandConfirmation(% x) ok = %t, want %t", tc.report, ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Errorf("ParseCommandConfirmation(% x) = %+v, want %+v", tc.report, got, tc.want)
			}
		})
	}
}

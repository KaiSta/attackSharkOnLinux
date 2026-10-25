package mouse

import (
	"encoding/binary"
	"testing"
)

// The parsers are the only thing standing between a byte stream the mouse
// controls and the number shown on screen, so they are fuzzed for two kinds of
// mistake: decoding a frame that is not one of the two subtypes, and decoding a
// real frame into something other than what it says.

func FuzzParseBattery(f *testing.F) {
	// Captured from the receiver, plus the shapes that must not decode.
	f.Add([]byte{0x03, 0x55, 0x40, 0x01, 0x4e})
	f.Add([]byte{0x03, 0x55, 0x40, 0x03, 0x64})
	f.Add([]byte{0x03, 0x55, 0x40, 0x02, 0x64})
	f.Add([]byte{0x03, 0x55, 0x41, 0x01, 0x4e})
	f.Add([]byte{0x03, 0x55, 0x40, 0x07, 0x32})
	f.Add([]byte{0x03, 0x55, 0x10, 0x04, 0x00})
	f.Add([]byte{0x03, 0x55})
	f.Add([]byte{0x00, 0x00, 0x01, 0xfe, 0xff, 0x00, 0x00})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, report []byte) {
		battery, ok := ParseBattery(report)
		if !ok {
			return
		}
		if !IsStatus(report) {
			t.Fatalf("ParseBattery accepted % x, which IsStatus rejects", report)
		}
		if report[2] != KindBattery && report[2] != KindBatteryAlt {
			t.Fatalf("ParseBattery accepted kind %#x", report[2])
		}
		if battery.Percent != int(report[4]) {
			t.Fatalf("Percent = %d, want the raw byte %d", battery.Percent, report[4])
		}
		if battery.ChargeRaw != report[3] {
			t.Fatalf("ChargeRaw = %#x, want the raw byte %#x", battery.ChargeRaw, report[3])
		}
		// However strange the charge byte, it decodes to exactly one of the
		// four states, and the known values are never interchangeable.
		switch report[3] {
		case 0x01:
			if battery.Charge != ChargeDischarging {
				t.Fatalf("charge byte %#x decoded as %v", report[3], battery.Charge)
			}
		case 0x02:
			if battery.Charge != ChargeFull {
				t.Fatalf("charge byte %#x decoded as %v", report[3], battery.Charge)
			}
		case 0x03:
			if battery.Charge != ChargeCharging {
				t.Fatalf("charge byte %#x decoded as %v", report[3], battery.Charge)
			}
		default:
			if battery.Charge != ChargeUnknown {
				t.Fatalf("unknown charge byte %#x decoded as %v", report[3], battery.Charge)
			}
		}
		if _, isStage := ParseStage(report); isStage {
			t.Fatalf("% x decoded as both a battery and a stage report", report)
		}
	})
}

func FuzzParseStage(f *testing.F) {
	f.Add([]byte{0x03, 0x55, 0x10, 0x04, 0x00})
	f.Add([]byte{0x03, 0x55, 0x10, 0x09, 0x00})
	f.Add([]byte{0x03, 0x55, 0x40, 0x01, 0x4e})
	f.Add([]byte{0x03, 0x55, 0x10})
	f.Add([]byte{0x03, 0x55})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, report []byte) {
		stage, ok := ParseStage(report)
		if !ok {
			return
		}
		if !IsStatus(report) {
			t.Fatalf("ParseStage accepted % x, which IsStatus rejects", report)
		}
		if report[2] != KindStage {
			t.Fatalf("ParseStage accepted kind %#x", report[2])
		}
		if stage.Stage != int(report[3]) {
			t.Fatalf("Stage = %d, want the raw byte %d", stage.Stage, report[3])
		}
		// An out-of-range stage stays visible rather than being dropped, which
		// is what Valid is for - so Valid must agree with the range.
		if want := stage.Stage >= 1 && stage.Stage <= MaxStage; stage.Valid() != want {
			t.Fatalf("Stage{%d}.Valid() = %v, want %v", stage.Stage, stage.Valid(), want)
		}
		if _, isBattery := ParseBattery(report); isBattery {
			t.Fatalf("% x decoded as both a stage and a battery report", report)
		}
	})
}

// IsStatus is the gate both parsers go through, so its contract is worth
// pinning down exactly: it accepts a report if and only if it is long enough to
// hold the five bytes the two subtypes occupy, with the report ID and magic the
// device uses.
func FuzzIsStatus(f *testing.F) {
	f.Add([]byte{0x03, 0x55, 0x40, 0x01, 0x4e})
	f.Add([]byte{0x03, 0x55, 0x10, 0x04, 0x00})
	f.Add([]byte{0x03, 0x55})
	f.Add([]byte{0x02, 0x55, 0x40, 0x01, 0x4e})
	f.Add([]byte{0x03, 0x99, 0x40, 0x01, 0x4e})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, report []byte) {
		want := len(report) >= ReportLen && report[0] == ReportID && report[1] == Magic
		if got := IsStatus(report); got != want {
			t.Fatalf("IsStatus(% x) = %v, want %v", report, got, want)
		}
		if _, ok := ParseBattery(report); ok && !want {
			t.Fatalf("ParseBattery accepted % x, which is not a status report", report)
		}
		if _, ok := ParseStage(report); ok && !want {
			t.Fatalf("ParseStage accepted % x, which is not a status report", report)
		}
	})
}

// EncodeDPI is the one place a sensor code is chosen rather than copied, so its
// contract is exact: a code is returned for a value if and only if that code
// decodes back to it, inside the sensor's range.
func FuzzEncodeDPI(f *testing.F) {
	for value := 0; value <= MaxDPI; value += 2000 {
		f.Add(value)
	}
	f.Add(50)
	f.Add(2400)
	f.Add(20100)
	f.Add(-1)

	f.Fuzz(func(t *testing.T, value int) {
		x, y, double, ok := EncodeDPI(value)
		if !ok {
			// Refusing is fine: the sensor's table does not cover every value
			// between 1 and MaxDPI. What must never happen is a code that
			// decodes to something other than what was asked for.
			return
		}
		if value < 1 || value > MaxDPI {
			t.Fatalf("EncodeDPI(%d) returned a code outside the sensor's range", value)
		}
		got, decodes := DecodeDPI(x, y, double)
		if !decodes || got != value {
			t.Fatalf("EncodeDPI(%d) = %#x, %#x, double %t, which decodes to %d (%t)",
				value, x, y, double, got, decodes)
		}
	})
}

// The DPI configuration report comes from the device and decides numbers shown
// as facts, so it is fuzzed for the failures that matter: accepting a report
// whose checksum does not cover it, inventing a stage value, and leaving a
// half-decoded mapping behind when it refuses one.
func FuzzParseDPIConfig(f *testing.F) {
	f.Add(dpiReport(defaultStages(), 4))
	f.Add(dpiReport(defaultStages(), 1)[:DPIReportPayload])
	f.Add(dpiReport(defaultStages(), 1)[:DPIReportPayload-1])
	f.Add(func() []byte { b := dpiReport(defaultStages(), 1); b[30]++; return b }())
	f.Add(append([]byte{0x05}, dpiReport(defaultStages(), 1)[1:]...))
	f.Add([]byte{})
	f.Add([]byte{0x04, 0x38, 0x01, 0x00})

	f.Fuzz(func(t *testing.T, buf []byte) {
		cfg, err := ParseDPIConfig(buf)
		if err != nil {
			if cfg.Stages.Configured() || cfg.Raw != nil {
				t.Fatalf("ParseDPIConfig(% x) returned %+v with error %v", buf, cfg, err)
			}
			return
		}

		if len(buf) < DPIReportPayload {
			t.Fatalf("accepted %d bytes, fewer than the %d needed", len(buf), DPIReportPayload)
		}
		if buf[0] != DPIReportID {
			t.Fatalf("accepted report ID 0x%02x", buf[0])
		}
		var sum uint16
		for _, b := range buf[3:50] {
			sum += uint16(b)
		}
		if want := binary.BigEndian.Uint16(buf[50:52]); sum != want {
			t.Fatalf("accepted a report whose checksum is 0x%04x, not 0x%04x", sum, want)
		}
		if !cfg.Stages.Configured() {
			t.Fatal("parsed a configuration that reports nothing configured")
		}
		for stage := 1; stage <= MaxStage; stage++ {
			value, ok := cfg.Stages.At(stage)
			if !ok {
				t.Fatalf("stage %d is missing", stage)
			}
			if value < 1 || value > MaxDPI {
				t.Fatalf("stage %d = %d, outside 1-%d", stage, value, MaxDPI)
			}
		}
		if len(cfg.Raw) != len(buf) {
			t.Fatalf("Raw is %d bytes, want the %d that were parsed", len(cfg.Raw), len(buf))
		}
		// A decoded report is itself a valid report: re-parsing the copy kept
		// for debugging must agree, or the menu would show something else than
		// what was accepted.
		again, err := ParseDPIConfig(cfg.Raw)
		if err != nil {
			t.Fatalf("re-parsing the accepted report failed: %v", err)
		}
		if again.Stages != cfg.Stages || again.ActiveStage != cfg.ActiveStage {
			t.Fatalf("re-parsing gave %+v, first parse gave %+v", again, cfg)
		}
	})
}

// The confirmation frame decides whether a write is reported as applied, so the
// parser must never report success for a status it does not understand, and must
// never accept a frame that is not a confirmation at all.
func FuzzParseCommandConfirmation(f *testing.F) {
	f.Add([]byte{0x03, 0x55, 0x50, 0x00, 0x04})
	f.Add([]byte{0x03, 0x55, 0x50, 0x01, 0x04})
	f.Add([]byte{0x03, 0x55, 0x50, 0x07, 0x04})
	f.Add([]byte{0x03, 0x55, 0x40, 0x01, 0x4e})
	f.Add([]byte{0x03, 0x55, 0x10, 0x02, 0x00})
	f.Add([]byte{0x03, 0x55, 0x50})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, report []byte) {
		conf, ok := ParseCommandConfirmation(report)
		if !ok {
			return
		}
		if !IsStatus(report) || report[2] != KindCommandConfirmation {
			t.Fatalf("accepted % x, which is not a confirmation", report)
		}
		if conf.ReportID != report[4] || conf.Status != report[3] {
			t.Fatalf("decoded %+v from % x", conf, report)
		}
		if want := report[3] == 0x00; conf.Success != want {
			t.Fatalf("status %#x decoded as success=%t", report[3], conf.Success)
		}
	})
}

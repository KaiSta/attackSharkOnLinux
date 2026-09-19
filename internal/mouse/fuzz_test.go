package mouse

import "testing"

// The parsers are the only thing standing between a byte stream the mouse
// controls and the number shown on screen, so they are fuzzed for two kinds of
// mistake: decoding a frame that is not one of the two subtypes, and decoding a
// real frame into something other than what it says.

func FuzzParseBattery(f *testing.F) {
	// Captured from the receiver, plus the shapes that must not decode.
	f.Add([]byte{0x03, 0x55, 0x40, 0x01, 0x4e})
	f.Add([]byte{0x03, 0x55, 0x40, 0x03, 0x64})
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
		if report[2] != KindBattery {
			t.Fatalf("ParseBattery accepted kind %#x", report[2])
		}
		if battery.Percent != int(report[4]) {
			t.Fatalf("Percent = %d, want the raw byte %d", battery.Percent, report[4])
		}
		if battery.ChargeRaw != report[3] {
			t.Fatalf("ChargeRaw = %#x, want the raw byte %#x", battery.ChargeRaw, report[3])
		}
		// However strange the charge byte, it decodes to exactly one of the
		// three states, and the two known values are never interchangeable.
		switch report[3] {
		case 0x01:
			if battery.Charge != ChargeDischarging {
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

// The six DPI values are typed by hand on a command line or written by hand in a
// config file, so this parser is fuzzed for the same two failures: inventing a
// value it was not given, and refusing one it was given.
func FuzzParseDPIStages(f *testing.F) {
	f.Add("800,1600,2400,3200,6400,22000")
	f.Add("1,2,3,4,5,6")
	f.Add("800;1600;2400;3200;6400;22000")
	f.Add("800 1600 2400 3200 6400 22000")
	f.Add("800/1600/2400/3200/6400/22000")
	f.Add("22000,22000,22000,22000,22000,22000")
	f.Add("")
	f.Add(",")
	f.Add("800,1600,2400,3200,6400")
	f.Add("800,1600,2400,3200,6400,0")
	f.Add("800,1600,2400,3200,6400,22001")
	f.Add("-1,2,3,4,5,6")
	f.Add("eight,1600,2400,3200,6400,22000")

	f.Fuzz(func(t *testing.T, text string) {
		stages, err := ParseDPIStages(text)
		if err != nil {
			// A rejected line must not leave a partly filled mapping behind.
			if stages.Configured() {
				t.Fatalf("ParseDPIStages(%q) = %q with error %v", text, stages, err)
			}
			return
		}

		if !stages.Configured() {
			t.Fatalf("ParseDPIStages accepted %q but reported nothing configured", text)
		}
		for stage := 1; stage <= MaxStage; stage++ {
			value, ok := stages.At(stage)
			if !ok {
				t.Fatalf("ParseDPIStages(%q): stage %d is missing", text, stage)
			}
			if value < 1 || value > MaxDPI {
				t.Fatalf("ParseDPIStages(%q): stage %d = %d, outside 1-%d",
					text, stage, value, MaxDPI)
			}
		}

		// What the tray prints has to be what this parser reads back: String is
		// used for display and for logging, and the menu labels come from At.
		rendered := stages.String()
		again, err := ParseDPIStages(rendered)
		if err != nil {
			t.Fatalf("ParseDPIStages(%q) rendered %q, which does not parse: %v",
				text, rendered, err)
		}
		if again != stages {
			t.Fatalf("%q rendered %q, which parsed back as %q", text, rendered, again)
		}
	})
}

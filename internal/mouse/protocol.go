// Package mouse decodes and encodes the vendor HID protocol spoken by the
// Attack Shark X11 2.4 GHz receiver (VID 1d57 / PID fa60).
//
// The receiver exposes four HID interfaces. USB interface #2 is the vendor
// configuration channel; it pushes HID report ID 0x03, five bytes long, with
// two subtypes observed on real hardware:
//
//	03 55 40 <charge> <percent>    battery level, pushed every ~2.00s
//	03 55 10 <stage>  00           DPI stage, pushed when the stage changes
//
// Both are read-only: the host never has to ask for them, and the mouse keeps
// reporting while it is awake.
//
// The per-stage DPI *values* (400, 800, 1600, ...) are NOT exposed on this
// channel, and could not be read from Linux by any method tried. See README.md
// for the evidence behind that claim before attempting it again.
package mouse

import "fmt"

const (
	// ReportID is the HID report ID carrying every status subtype.
	ReportID = 0x03
	// ReportLen is the total length of a status report, including the ID byte.
	ReportLen = 5
	// Magic is the second byte of every status report observed on this device.
	Magic = 0x55

	// KindBattery marks a battery report: 03 55 40 <charge> <percent>.
	KindBattery = 0x40
	// KindStage marks a DPI stage report: 03 55 10 <stage> 00.
	KindStage = 0x10

	// MaxStage is the number of DPI stages the mouse cycles through with its
	// DPI button. Confirmed by six consecutive presses producing
	// 04 05 06 01 02 03 - one full cycle with wraparound.
	MaxStage = 6

	chargeDischarging = 0x01
	chargeCharging    = 0x03
)

// ChargeState is the charging state reported alongside the battery level.
type ChargeState uint8

const (
	// ChargeUnknown means the device sent a charge byte we do not understand.
	ChargeUnknown ChargeState = iota
	// ChargeDischarging means running on battery.
	ChargeDischarging
	// ChargeCharging means the cable is connected and the mouse is charging.
	ChargeCharging
)

func (c ChargeState) String() string {
	switch c {
	case ChargeDischarging:
		return "discharging"
	case ChargeCharging:
		return "charging"
	default:
		return "unknown"
	}
}

// Battery is a decoded battery report.
type Battery struct {
	// Percent is the level exactly as the mouse reported it. It is not
	// clamped, so a nonsensical value stays visible rather than being hidden.
	Percent int
	// Charge is the decoded charging state.
	Charge ChargeState
	// ChargeRaw is the raw byte Charge was decoded from, kept for unknown
	// values so they can be reported verbatim.
	ChargeRaw byte
}

func (b Battery) String() string {
	return fmt.Sprintf("%d %% (%s)", b.Percent, b.Charge)
}

// Stage is a decoded DPI stage report. The stage number is 1-based.
type Stage struct {
	Stage int
}

// Valid reports whether the stage number is within the range the mouse uses.
func (s Stage) Valid() bool { return s.Stage >= 1 && s.Stage <= MaxStage }

func (s Stage) String() string {
	if !s.Valid() {
		return fmt.Sprintf("stage %d (outside 1-%d)", s.Stage, MaxStage)
	}
	return fmt.Sprintf("stage %d of %d", s.Stage, MaxStage)
}

// IsStatus reports whether report is a well-formed status report, without
// committing to a particular subtype.
func IsStatus(report []byte) bool {
	return len(report) >= ReportLen && report[0] == ReportID && report[1] == Magic
}

// ParseBattery decodes a battery report. ok is false for any other report,
// including the DPI stage subtype.
func ParseBattery(report []byte) (Battery, bool) {
	if !IsStatus(report) || report[2] != KindBattery {
		return Battery{}, false
	}

	b := Battery{
		Percent:   int(report[4]),
		ChargeRaw: report[3],
	}
	switch report[3] {
	case chargeDischarging:
		b.Charge = ChargeDischarging
	case chargeCharging:
		b.Charge = ChargeCharging
	default:
		b.Charge = ChargeUnknown
	}
	return b, true
}

// ParseStage decodes a DPI stage report. ok reports only that the frame is a
// stage notification; use Stage.Valid for the range check, so an out-of-range
// value from the device stays visible instead of being silently dropped.
func ParseStage(report []byte) (Stage, bool) {
	if !IsStatus(report) || report[2] != KindStage {
		return Stage{}, false
	}
	return Stage{Stage: int(report[3])}, true
}

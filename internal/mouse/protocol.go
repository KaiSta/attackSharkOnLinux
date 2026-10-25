// Package mouse decodes and encodes the vendor HID protocol spoken by the
// Attack Shark X11 2.4 GHz receiver (VID 1d57 / PID fa60).
//
// The receiver exposes four HID interfaces. USB interface #2 is the vendor
// configuration channel; it pushes HID report ID 0x03, five bytes long, with
// these subtypes observed on real hardware:
//
//	03 55 40 <charge> <percent>    battery level, pushed every ~2.00s
//	03 55 10 <stage>  00           DPI stage, pushed when the stage changes
//	03 55 50 <status> <report>     reply to a feature report, see dpistage.go
//
// The battery frame is documented upstream as also arriving with 0x41 in place
// of 0x40, which is why both are accepted. The charge byte is 0x01 on battery,
// 0x03 while charging and 0x02 once charging completes. All of these are
// read-only: the host never has to ask for them, and the mouse keeps reporting
// while it is awake.
//
// The per-stage DPI *values* (400, 800, 1600, ...) are not on this channel: they
// live in the device's feature reports and are read with the permission
// handshake in dpiread.go, as is the polling rate in polling.go. The only other
// feature writes are the stage-change report in dpistage.go and the polling-rate
// report in polling.go. See the "DPI values" section of README.md.
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
	// KindBatteryAlt is documented by the attack-shark-x11-driver project as the
	// same event as KindBattery, differing only in this byte. It has not been
	// seen on this unit, so it is accepted rather than relied on: the frame is
	// otherwise identical, and ignoring it would drop a reading for no reason.
	KindBatteryAlt = 0x41
	// KindStage marks a DPI stage report: 03 55 10 <stage> 00.
	KindStage = 0x10
	// KindCommandConfirmation marks the reply to a feature report:
	// 03 55 50 <status> <report ID>, where status 0x00 is success.
	KindCommandConfirmation = 0x50

	// MaxStage is the number of DPI stages the mouse cycles through with its
	// DPI button. Confirmed by six consecutive presses producing
	// 04 05 06 01 02 03 - one full cycle with wraparound.
	MaxStage = 6

	chargeDischarging = 0x01
	chargeFull        = 0x02
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
	// ChargeFull means the cable is connected and the charge is complete. The
	// firmware sends it once, when charging finishes; upstream's captures record
	// it as 03 55 40 02 64, and it has not been reproduced here yet.
	ChargeFull
)

func (c ChargeState) String() string {
	switch c {
	case ChargeDischarging:
		return "discharging"
	case ChargeCharging:
		return "charging"
	case ChargeFull:
		return "fully charged"
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
	if !IsStatus(report) || (report[2] != KindBattery && report[2] != KindBatteryAlt) {
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
	case chargeFull:
		b.Charge = ChargeFull
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

// CommandConfirmation is the device's reply after it has processed a feature
// report. It names the report it refers to, so a pending write can be matched.
type CommandConfirmation struct {
	// ReportID is the feature report that was processed, 0x04 for the DPI
	// configuration.
	ReportID byte
	// Success is true only for status 0x00. A status this build does not know
	// is deliberately reported as not successful rather than guessed at.
	Success bool
	// Status is the raw status byte, kept so an unknown value can be logged.
	Status byte
}

// ParseCommandConfirmation decodes the reply the firmware sends on the status
// channel after a feature report: 03 55 50 <status> <report ID>. Status 0x00 is
// success and 0x01 failure; the protocol carries no further detail, and no
// status at all if the write never landed.
func ParseCommandConfirmation(report []byte) (CommandConfirmation, bool) {
	if !IsStatus(report) || report[2] != KindCommandConfirmation {
		return CommandConfirmation{}, false
	}
	return CommandConfirmation{
		ReportID: report[4],
		Success:  report[3] == 0x00,
		Status:   report[3],
	}, true
}

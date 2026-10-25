package mouse

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// Reading the DPI configuration the mouse actually stores.
//
// The per-stage values are not pushed on the status channel, so they have to be
// asked for. Feature reads on their own return garbage on this receiver: the
// firmware only answers after a two-step handshake, recovered by the
// attack-shark-x11-driver project and confirmed against the vendor software's
// captures:
//
//	SET_FEATURE  a0 <report> <length> 00 <parameter> 00 00 00
//	GET_FEATURE  a0            -> byte 1 == 0x01 means one read is permitted
//	GET_FEATURE  <report>      -> the configuration itself
//
// The permission is good for exactly one read, and report 0xA0 is the mailbox
// that grants it. That explains an earlier negative result in this repository:
// feature reads performed without the mailbox return unaddressable data, which
// is what led to the conclusion that DPI values could not be read at all.
//
// Reading changes no setting: the only frame sent here is the permission
// request above. Writing a configuration back happens only when the user asks
// the tray to switch stages (dpistage.go), or to change the polling rate, which
// is a separate nine-byte report (polling.go).

const (
	// PermissionReportID is the feature report the firmware uses as its
	// one-shot read-permission mailbox.
	PermissionReportID = 0xA0
	// DPIReportID is the feature report carrying the stored DPI configuration.
	DPIReportID = 0x04
	// DPIReportLen is the length of that report including its report ID byte.
	// The device's descriptor declares a 55-byte payload for report 0x04, which
	// is the 56 bytes a GET_FEATURE carries once the ID byte is counted.
	DPIReportLen = 56
	// DPIReportPayload is the shortest response that still holds the checksum at
	// bytes 50-51. Anything shorter cannot be validated and is refused.
	DPIReportPayload = 52

	permissionResponseLen = 8
	permissionGranted     = 0x01
	// permissionAttempts bounds how often the mailbox is asked. Back-to-back
	// reads are refused roughly one time in two with a single attempt, because
	// the firmware needs a quiet moment before granting another read; so a
	// refusal is retried, with a pause longer than that quiet moment, rather
	// than reported as the device being unreadable.
	permissionAttempts = 3
)

// PermissionDelay is how long the firmware needs between the permission request
// and reading the mailbox. The driver project measured 250 ms; a shorter wait
// reads the stale reply and looks like a refusal.
const PermissionDelay = 250 * time.Millisecond

// permissionWait is PermissionDelay in a variable so tests do not have to sleep.
var permissionWait = PermissionDelay

// permissionRetryWait is how long to wait after a refused permission request
// before asking again. Measured on hardware: reads spaced by about half a second
// succeed every time, while retries 150-400 ms apart were refused every time,
// because each request appears to restart the firmware's cooldown. The wait is
// therefore deliberately longer than one read takes.
var permissionRetryWait = 700 * time.Millisecond

// ErrNoReadPermission means the firmware acknowledged the request but did not
// grant a read of the report that was asked for.
var ErrNoReadPermission = errors.New("the device did not grant permission to read its configuration")

// featureDevice is the part of a hidraw device this file needs. Keeping it an
// interface means the handshake can be tested without hardware.
type featureDevice interface {
	// SetFeature writes one feature report. buf[0] is the report ID.
	SetFeature(buf []byte) error
	// GetFeature reads size bytes of a feature report; the slice it returns
	// starts with the report ID.
	GetFeature(reportID byte, size int) ([]byte, error)
}

// PermissionRequest builds the mailbox frame that asks for one read of reportID.
// responseLen is the length of the reply that will be read, and parameter is the
// profile (or, for a macro, the macro ID) the read applies to.
func PermissionRequest(reportID byte, responseLen int, parameter byte) []byte {
	return []byte{
		PermissionReportID,
		reportID,
		byte(responseLen),
		0x00,
		parameter,
		0x00,
		0x00,
		0x00,
	}
}

// ReadDPIConfig grants itself one read and asks the device for report 0x04.
//
// It returns an error rather than a zero value whenever anything is off: a
// refusal, a short report, a wrong report ID, a failed checksum or a stage coded
// with a byte this build does not understand. The caller is expected to fall
// back to its own settings, because a plausible-looking number read from the
// wrong place is worse than no number at all.
func ReadDPIConfig(dev featureDevice) (DPIConfig, error) {
	if err := requestReadPermission(dev, DPIReportID, DPIReportLen, 0x01); err != nil {
		return DPIConfig{}, err
	}

	buf, err := dev.GetFeature(DPIReportID, DPIReportLen)
	if err != nil {
		return DPIConfig{}, fmt.Errorf("reading the DPI configuration: %w", err)
	}
	return ParseDPIConfig(buf)
}

// requestReadPermission asks the 0xA0 mailbox for one read, retrying a refusal.
// The firmware occasionally declines a request that arrives while it is still
// finishing the previous transfer; asking again shortly afterwards succeeds.
// Exhausting the attempts is still reported as the original refusal.
func requestReadPermission(dev featureDevice, reportID byte, responseLen int, parameter byte) error {
	var last error
	for attempt := 1; attempt <= permissionAttempts; attempt++ {
		if err := dev.SetFeature(PermissionRequest(reportID, responseLen, parameter)); err != nil {
			return fmt.Errorf("asking the device for read permission: %w", err)
		}
		time.Sleep(permissionWait)

		reply, err := dev.GetFeature(PermissionReportID, permissionResponseLen)
		switch {
		case err != nil:
			last = fmt.Errorf("reading the permission mailbox: %w", err)
		case len(reply) >= 2 && reply[1] == permissionGranted:
			return nil
		default:
			last = ErrNoReadPermission
		}

		if attempt < permissionAttempts {
			time.Sleep(permissionRetryWait)
		}
	}
	return last
}

// DPIConfig is the stored mouse configuration as read back from the device.
type DPIConfig struct {
	// Stages is the DPI value of each of the six stages, in stage order.
	Stages DPIStages
	// ActiveStage is the stage the device says is selected, 1-based. It is
	// reported verbatim: the tray takes the live stage from the mouse's own
	// announcements instead, so an unexpected value here is not an error.
	ActiveStage int
	// ActiveMask has one bit per enabled stage, stage 1 in bit 0.
	ActiveMask byte
	// Profile is the profile the values belong to, 1-based.
	Profile byte
	// Raw is the response exactly as it arrived, for anyone debugging a
	// disagreement between this decoder and the device.
	Raw []byte
}

func (c DPIConfig) String() string {
	return fmt.Sprintf("profile %d, stage %d, %s", c.Profile, c.ActiveStage, c.Stages)
}

// ParseDPIConfig decodes the 0x04 feature report: stage codes at bytes 8-13,
// the double-speed flags at byte 6, the second code byte at bytes 16-21, the
// active stage at byte 24, and a 16-bit big-endian checksum over bytes 3-49 at
// bytes 50-51.
//
// The checksum is what separates a real configuration from the leftover bytes
// this receiver returns for an ungranted read, so it is checked before anything
// is decoded.
func ParseDPIConfig(buf []byte) (DPIConfig, error) {
	if len(buf) < DPIReportPayload {
		return DPIConfig{}, fmt.Errorf("DPI configuration is %d bytes, need at least %d",
			len(buf), DPIReportPayload)
	}
	if buf[0] != DPIReportID {
		return DPIConfig{}, fmt.Errorf("DPI configuration starts with report ID 0x%02x, not 0x%02x",
			buf[0], DPIReportID)
	}

	if sum, want := dpiChecksum(buf), binary.BigEndian.Uint16(buf[50:52]); sum != want {
		return DPIConfig{}, fmt.Errorf("DPI configuration checksum is 0x%04x, the report says 0x%04x",
			sum, want)
	}

	cfg := DPIConfig{
		Profile:     buf[2],
		ActiveMask:  buf[5],
		ActiveStage: int(buf[24]),
		Raw:         append([]byte(nil), buf...),
	}
	for i := 0; i < MaxStage; i++ {
		x, y := buf[8+i], buf[16+i]
		double := buf[6]&(1<<i) != 0
		value, ok := DecodeDPI(x, y, double)
		if !ok {
			return DPIConfig{}, fmt.Errorf("stage %d uses a DPI code this build does not know (x 0x%02x, y 0x%02x, double %t)",
				i+1, x, y, double)
		}
		if value < 1 || value > MaxDPI {
			return DPIConfig{}, fmt.Errorf("stage %d decodes to %d, outside 1-%d", i+1, value, MaxDPI)
		}
		cfg.Stages.values[i] = value
	}
	return cfg, nil
}

// dpiChecksum is the 16-bit sum over bytes 3-49 that the device stores
// big-endian at bytes 50-51 of the DPI configuration report.
func dpiChecksum(buf []byte) uint16 {
	var sum uint16
	for _, b := range buf[3:50] {
		sum += uint16(b)
	}
	return sum
}

// DecodeDPI turns the sensor's two code bytes and its double-speed flag back
// into a DPI value.
//
// The encoding is the PAW3311's, not a plain integer: a second byte of 0x00
// means 50 DPI steps, 0x01 means 100 DPI steps, and anything larger packs an
// index above 10000 DPI into two bytes. Reporting whether the code is known
// matters more than returning a number, so ok is false for a code this build
// cannot place.
func DecodeDPI(x, y byte, double bool) (int, bool) {
	if x == 0 && y == 0 {
		return 0, false
	}

	var dpi int
	switch {
	case y == 0 || y == 1:
		index := indexOfDPICode(x)
		if index < 0 {
			return 0, false
		}
		if y == 0 {
			dpi = index*50 + 50
		} else {
			dpi = index*100 + 100
		}
	default:
		// A second byte of 2 or more packs an index at or above 199 into two
		// bytes, which is how the range above 10,000 DPI is stored.
		combined := int(y)<<8 | int(x)
		dpi = 10100 + (combined-199)*100
	}

	if double {
		// 20,100 DPI is stored as a 0xEB code that cannot be derived from the
		// table, so it is the one hard-coded exception.
		if x == 0xeb {
			return 20100, true
		}
		dpi *= 2
	}
	return dpi, true
}

// EncodeDPI turns a DPI value into the code bytes the device stores for it. It
// is the inverse of DecodeDPI and exists only for setting a value the user
// picked; every other write in this package copies the codes the device itself
// supplied.
//
// Each candidate encoding is decoded back before it is returned, so a value can
// never be written with a code that reads back as something else. The 50-DPI
// form is preferred, because that is what this receiver uses for the values it
// was found holding (800 = 0x12 in byte 8, 3200 = 0x4b, and so on).
func EncodeDPI(value int) (x, y byte, double bool, ok bool) {
	if value < 1 || value > MaxDPI {
		return 0, 0, false, false
	}

	type candidate struct {
		x, y   byte
		double bool
	}
	var candidates []candidate

	// A 50 DPI step, index 0 being 50 DPI with the second byte 0x00.
	if value >= 50 && (value-50)%50 == 0 {
		if i := (value - 50) / 50; i < len(dpi3311Table) {
			candidates = append(candidates, candidate{dpi3311Table[i], 0, false})
		}
	}
	// The same table in 100 DPI steps, with the second byte 0x01.
	if value >= 100 && (value-100)%100 == 0 {
		if i := (value - 100) / 100; i < len(dpi3311Table) {
			candidates = append(candidates, candidate{dpi3311Table[i], 1, false})
		}
	}
	// Values above the table's reach are stored as half the target plus the
	// double-speed flag, which is how 22,000 DPI is written as 0x81, 0x01 + double.
	if value%2 == 0 {
		half := value / 2
		if half >= 50 && (half-50)%50 == 0 {
			if i := (half - 50) / 50; i < len(dpi3311Table) {
				candidates = append(candidates, candidate{dpi3311Table[i], 0, true})
			}
		}
		if half >= 100 && (half-100)%100 == 0 {
			if i := (half - 100) / 100; i < len(dpi3311Table) {
				candidates = append(candidates, candidate{dpi3311Table[i], 1, true})
			}
		}
	}
	// 20,100 DPI is stored as 0xEB with the double flag, a code the table's
	// arithmetic cannot derive; DecodeDPI has the matching exception.
	candidates = append(candidates, candidate{0xeb, 0, true})

	for _, c := range candidates {
		if got, decodes := DecodeDPI(c.x, c.y, c.double); decodes && got == value {
			return c.x, c.y, c.double, true
		}
	}
	return 0, 0, false, false
}

// indexOfDPICode returns the position of code in the sensor's table, or -1.
// The first match wins, as it does on the device: the table repeats a few codes
// above 0xEB, which the >10000 DPI encoding never reaches.
func indexOfDPICode(code byte) int {
	for i, b := range dpi3311Table {
		if b == code {
			return i
		}
	}
	return -1
}

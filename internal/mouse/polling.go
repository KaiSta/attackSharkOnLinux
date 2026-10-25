package mouse

import "fmt"

// Reading and writing the polling rate.
//
// The rate is a 9-byte feature report, 0x06, laid out as
//
//	06 09 <profile> <code> <0xFF - code> 00 00 00 00
//
// where the four supported codes are 0x08, 0x04, 0x02 and 0x01 for 125, 250,
// 500 and 1000 Hz. Unlike the DPI configuration the report carries nothing else,
// so a change does not have to preserve anything except the profile it applies
// to: the rate is one field of nine bytes rather than a field inside a
// configuration that also holds six DPI values and their colours.
//
// The read goes through the same one-shot 0xA0 permission handshake as the DPI
// configuration (see dpiread.go). Cheap as the report is, it is still checked:
// a refused permission, a wrong report ID, a wrong checksum or a code this build
// does not know is reported as an error rather than turned into a rate.

const (
	// PollingReportID is the feature report carrying the polling rate.
	PollingReportID = 0x06
	// PollingReportLen is the length of that report including its report ID.
	PollingReportLen = 9
	// defaultProfile is the profile the reports this app writes address. The
	// device reports profile 1 as active on the unit this was written against,
	// and this app does not manage profiles, so the same profile is used here
	// as by the DPI read and the stage change.
	defaultProfile = 0x01
)

// PollingRate is how often the mouse reports to the host, in Hz.
type PollingRate int

const (
	// Polling125 is the slowest rate, which the vendor calls power saving.
	Polling125 PollingRate = 125
	// Polling250 is the vendor's office setting.
	Polling250 PollingRate = 250
	// Polling500 is the vendor's gaming setting.
	Polling500 PollingRate = 500
	// Polling1000 is the fastest rate, the vendor's eSports setting.
	Polling1000 PollingRate = 1000
)

// PollingRates lists the four rates the mouse accepts, slowest first. It is an
// array so callers cannot reorder the menu by appending to it.
var PollingRates = [...]PollingRate{Polling125, Polling250, Polling500, Polling1000}

// String renders the rate the way it is shown to a person.
func (r PollingRate) String() string { return fmt.Sprintf("%d Hz", int(r)) }

// Name is the vendor's own label for the rate, empty for an unknown one.
func (r PollingRate) Name() string {
	switch r {
	case Polling125:
		return "power saving"
	case Polling250:
		return "office"
	case Polling500:
		return "gaming"
	case Polling1000:
		return "eSports"
	}
	return ""
}

// Valid reports whether the rate is one the mouse supports.
func (r PollingRate) Valid() bool {
	_, ok := pollingCode(r)
	return ok
}

// pollingCode returns the byte the device stores for rate.
func pollingCode(rate PollingRate) (byte, bool) {
	switch rate {
	case Polling125:
		return 0x08, true
	case Polling250:
		return 0x04, true
	case Polling500:
		return 0x02, true
	case Polling1000:
		return 0x01, true
	}
	return 0, false
}

// pollingRate returns the rate a stored byte stands for.
func pollingRate(code byte) (PollingRate, bool) {
	switch code {
	case 0x08:
		return Polling125, true
	case 0x04:
		return Polling250, true
	case 0x02:
		return Polling500, true
	case 0x01:
		return Polling1000, true
	}
	return 0, false
}

// PollingConfig is the polling-rate report as read back from the device.
type PollingConfig struct {
	// Rate is the rate the device is set to.
	Rate PollingRate
	// Profile is the profile the setting belongs to, as reported by the device.
	Profile byte
	// Raw is the response exactly as it arrived, for debugging.
	Raw []byte
}

func (c PollingConfig) String() string {
	return fmt.Sprintf("%s (profile %d)", c.Rate, c.Profile)
}

// ParsePollingConfig decodes the 0x06 feature report. The checksum is what
// separates a real answer from the leftover bytes an ungranted read returns, so
// it is checked before the rate is believed.
func ParsePollingConfig(buf []byte) (PollingConfig, error) {
	if len(buf) < PollingReportLen {
		return PollingConfig{}, fmt.Errorf("polling rate report is %d bytes, need %d",
			len(buf), PollingReportLen)
	}
	if buf[0] != PollingReportID {
		return PollingConfig{}, fmt.Errorf("polling rate report starts with report ID 0x%02x, not 0x%02x",
			buf[0], PollingReportID)
	}

	code := buf[3]
	if want := code ^ 0xff; buf[4] != want {
		return PollingConfig{}, fmt.Errorf("polling rate checksum is 0x%02x, the report says 0x%02x",
			want, buf[4])
	}

	rate, ok := pollingRate(code)
	if !ok {
		return PollingConfig{}, fmt.Errorf("polling rate code 0x%02x is not one this build knows", code)
	}

	return PollingConfig{
		Rate:    rate,
		Profile: buf[2],
		Raw:     append([]byte(nil), buf...),
	}, nil
}

// ReadPollingRate grants itself one read and asks the device for report 0x06.
func ReadPollingRate(dev featureDevice) (PollingConfig, error) {
	if err := requestReadPermission(dev, PollingReportID, PollingReportLen, defaultProfile); err != nil {
		return PollingConfig{}, err
	}

	buf, err := dev.GetFeature(PollingReportID, PollingReportLen)
	if err != nil {
		return PollingConfig{}, fmt.Errorf("reading the polling rate: %w", err)
	}
	return ParsePollingConfig(buf)
}

// BuildPollingRateChange returns the report that sets rate for profile.
func BuildPollingRateChange(profile byte, rate PollingRate) ([]byte, error) {
	code, ok := pollingCode(rate)
	if !ok {
		return nil, fmt.Errorf("%d Hz is not a polling rate this mouse supports (125, 250, 500 or 1000 Hz)",
			int(rate))
	}
	if profile == 0 {
		return nil, fmt.Errorf("profile 0 is not a profile the device accepts")
	}

	frame := make([]byte, PollingReportLen)
	frame[0] = PollingReportID
	frame[1] = PollingReportLen
	frame[2] = profile
	frame[3] = code
	frame[4] = code ^ 0xff
	return frame, nil
}

// ApplyPollingRate writes rate to the device, preserving the profile the device
// reports when that read succeeds.
//
// The read is an improvement rather than a requirement, which is the one place
// this differs from the stage change: the DPI report carries every setting, so
// writing it without reading would invent values, while this report carries one
// field. If the read is refused the write still goes ahead for the default
// profile, which is the profile the DPI code already addresses.
func ApplyPollingRate(dev featureDevice, rate PollingRate) ([]byte, error) {
	profile := byte(defaultProfile)
	if cfg, err := ReadPollingRate(dev); err == nil {
		profile = cfg.Profile
	}

	frame, err := BuildPollingRateChange(profile, rate)
	if err != nil {
		return nil, err
	}
	if err := dev.SetFeature(frame); err != nil {
		return nil, fmt.Errorf("writing the polling rate: %w", err)
	}
	return frame, nil
}

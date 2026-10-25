package mouse

import (
	"encoding/binary"
	"fmt"
)

// Setting the DPI value of a stage.
//
// The value is one field of the same 0x04 configuration report the read
// decodes, so a change is a read-modify-write like a stage switch: the current
// configuration is read first and only that stage's two code bytes and its
// double-speed flag are replaced. The six values, per-stage colours, angle snap,
// ripple control and profile that the report also carries are written back
// exactly as they arrived.
//
// The write is a feature SET_REPORT (HIDIOCSFEATURE), the same direction and the
// same report as the stage switch, and the firmware answers with the same 0x50
// confirmation naming report 0x04.

// BuildDPIChange returns a copy of the configuration report with stage's DPI set
// to value and the checksum recomputed. cfg must have been read from the device:
// writing without it would mean inventing the other five values and the settings
// the report also holds.
func BuildDPIChange(cfg DPIConfig, stage, value int) ([]byte, error) {
	if stage < 1 || stage > MaxStage {
		return nil, fmt.Errorf("stage %d is outside 1-%d", stage, MaxStage)
	}
	if value < 1 || value > MaxDPI {
		return nil, fmt.Errorf("%d DPI is outside 1-%d, the range the %s supports",
			value, MaxDPI, SensorName)
	}
	if len(cfg.Raw) < DPIReportLen {
		return nil, fmt.Errorf("refusing to write a %d-byte configuration report; %d bytes are needed to preserve the rest of the settings",
			len(cfg.Raw), DPIReportLen)
	}

	x, y, double, ok := EncodeDPI(value)
	if !ok {
		return nil, fmt.Errorf("the sensor has no stored code for %d DPI", value)
	}

	frame := append([]byte(nil), cfg.Raw[:DPIReportLen]...)
	frame[8+stage-1] = x
	frame[16+stage-1] = y
	if double {
		frame[6] |= 1 << (stage - 1)
	} else {
		frame[6] &^= 1 << (stage - 1)
	}
	binary.BigEndian.PutUint16(frame[50:52], dpiChecksum(frame))
	return frame, nil
}

// ApplyDPIValue reads the configuration, sets one stage's DPI value and writes
// the report back.
//
// It returns the configuration as written, decoded from the frame it sent, so a
// caller can label the stage from what the device now holds - all six values,
// not just the one that changed. The read is not optional, for the same reason it
// is not optional for a stage switch.
func ApplyDPIValue(dev featureDevice, stage, value int) (DPIConfig, error) {
	cfg, err := ReadDPIConfig(dev)
	if err != nil {
		return DPIConfig{}, fmt.Errorf("reading the current configuration before setting %d DPI: %w", value, err)
	}

	frame, err := BuildDPIChange(cfg, stage, value)
	if err != nil {
		return DPIConfig{}, err
	}
	if err := dev.SetFeature(frame); err != nil {
		return DPIConfig{}, fmt.Errorf("writing the DPI configuration: %w", err)
	}
	return ParseDPIConfig(frame)
}

package mouse

import (
	"encoding/binary"
	"fmt"
	"time"
)

// Switching the selected DPI stage.
//
// The device takes a stage change as the whole 0x04 configuration report, not as
// a one-byte command, so a switch is always a read-modify-write: the current
// configuration is read first and only byte 24, the selected stage, is replaced.
// Everything else - per-stage DPI values, colours, angle snap, ripple control,
// profile and padding - is written back exactly as it arrived.
//
// A configuration that cannot be read is never written. Synthesising one from
// defaults would silently reset settings the user never asked to touch, which is
// a far worse outcome than a switch that refuses to happen.
//
// The write is a feature SET_REPORT (HIDIOCSFEATURE on Linux), the same
// direction as the read-permission request. The firmware answers with a 0x50
// confirmation on the status channel, naming report 0x04 and carrying 0x00 for
// success or 0x01 for failure. That reply is not always delivered even when the
// write landed, so a caller that sees no confirmation should verify by reading
// the configuration back rather than assuming failure.

const (
	// ACKTimeout is how long to wait for the device's confirmation of a feature
	// write, whichever setting it changes, before verifying by reading it back.
	// It matches the window the vendor software allows before it retries.
	ACKTimeout = time.Second
)

// BuildStageChange returns a copy of the configuration report with stage
// selected and the checksum recomputed. cfg must have been read from the device:
// a short response means the fields this function would preserve were never
// received, so it refuses rather than writing a truncated report.
func BuildStageChange(cfg DPIConfig, stage int) ([]byte, error) {
	if stage < 1 || stage > MaxStage {
		return nil, fmt.Errorf("stage %d is outside 1-%d", stage, MaxStage)
	}
	if len(cfg.Raw) < DPIReportLen {
		return nil, fmt.Errorf("refusing to write a %d-byte configuration report; %d bytes are needed to preserve the rest of the settings",
			len(cfg.Raw), DPIReportLen)
	}

	frame := append([]byte(nil), cfg.Raw[:DPIReportLen]...)
	frame[24] = byte(stage)
	binary.BigEndian.PutUint16(frame[50:52], dpiChecksum(frame))
	return frame, nil
}

// ApplyStage reads the current configuration, changes only the selected stage
// and writes the report back. It returns the frame it wrote.
//
// The read is not optional: the write carries the whole configuration, so
// writing without it would mean inventing the five DPI values, the colours and
// the sensor flags that the report also contains.
func ApplyStage(dev featureDevice, stage int) ([]byte, error) {
	cfg, err := ReadDPIConfig(dev)
	if err != nil {
		return nil, fmt.Errorf("reading the current configuration before changing the stage: %w", err)
	}

	frame, err := BuildStageChange(cfg, stage)
	if err != nil {
		return nil, err
	}
	if err := dev.SetFeature(frame); err != nil {
		return nil, fmt.Errorf("writing the DPI configuration: %w", err)
	}
	return frame, nil
}

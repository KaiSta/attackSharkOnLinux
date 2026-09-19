package mouse

import (
	"fmt"
	"strconv"
	"strings"
)

// Published facts about the X11, taken from the vendor's own product page. They
// are the only per-stage figures Attack Shark publishes: the number of stages
// and the sensor's ceiling. The factory DPI value of each individual stage is
// not published anywhere and is not readable from Linux - see the "DPI values"
// section of README.md - so a stage's value is configuration, not telemetry.
const (
	// SensorName is the sensor the X11 ships with.
	SensorName = "PixArt PAW3311"
	// MaxDPI is the highest DPI the sensor supports, per the vendor.
	MaxDPI = 22000
)

// DPIStages holds the DPI value of each of the mouse's six stages, index 0
// being stage 1. The zero value means "not configured", which is distinct from
// any real mapping because every accepted value is at least 1.
type DPIStages struct {
	values [MaxStage]int
}

// Configured reports whether a complete mapping has been set.
func (d DPIStages) Configured() bool { return d.values[0] != 0 }

// At returns the DPI value of a 1-based stage. ok is false when stage is out of
// range or no mapping is configured.
func (d DPIStages) At(stage int) (int, bool) {
	if stage < 1 || stage > MaxStage || !d.Configured() {
		return 0, false
	}
	return d.values[stage-1], true
}

// String renders the mapping as "800/1600/2400/3200/6400/22000", or the empty
// string when nothing is configured.
func (d DPIStages) String() string {
	if !d.Configured() {
		return ""
	}
	parts := make([]string, MaxStage)
	for i, v := range d.values {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, "/")
}

// ParseDPIStages reads six DPI values, separated by commas, spaces or
// semicolons, in stage order. Every value must be between 1 and MaxDPI: the
// upper bound is the sensor's published maximum, so a value above it is a typo
// rather than a setting.
//
// The returned mapping is empty whenever an error is returned, so a caller that
// ignores the error is left with nothing to display rather than with a partly
// applied set of numbers.
func ParseDPIStages(text string) (DPIStages, error) {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '/'
	})
	if len(fields) != MaxStage {
		return DPIStages{}, fmt.Errorf("need %d DPI values (one per stage), got %d in %q",
			MaxStage, len(fields), text)
	}

	var out DPIStages
	for i, field := range fields {
		value, err := strconv.Atoi(field)
		if err != nil {
			return DPIStages{}, fmt.Errorf("stage %d: %q is not a number", i+1, field)
		}
		if value < 1 || value > MaxDPI {
			return DPIStages{}, fmt.Errorf("stage %d: %d is outside 1-%d, the range the %s supports",
				i+1, value, MaxDPI, SensorName)
		}
		out.values[i] = value
	}
	return out, nil
}

// StageDescription describes one stage for menus and tooltips: "2400 DPI" when
// a value is configured, and "" when it is not.
func (d DPIStages) StageDescription(stage int) string {
	value, ok := d.At(stage)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d DPI", value)
}

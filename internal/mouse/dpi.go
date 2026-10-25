package mouse

import (
	"fmt"
	"strconv"
	"strings"
)

// Published facts about the X11, taken from the vendor's own product page. They
// are the only per-stage figures Attack Shark publishes: the number of stages
// and the sensor's ceiling. The per-stage values themselves are stored in the
// mouse and read over the vendor feature reports (see dpiread.go), so this type
// holds nothing but what the device reported.
const (
	// SensorName is the sensor the X11 ships with.
	SensorName = "PixArt PAW3311"
	// MaxDPI is the highest DPI the sensor supports, per the vendor.
	MaxDPI = 22000
)

// DPIStages holds the DPI value of each of the mouse's six stages, index 0
// being stage 1. The zero value means "not read from the device yet", which is
// distinct from any real mapping because every accepted value is at least 1.
type DPIStages struct {
	values [MaxStage]int
}

// Configured reports whether a complete mapping has been read.
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

// NewDPIStages builds a mapping from the DPI value of each stage, in stage
// order. Every value must be between 1 and MaxDPI, so a mapping that exists is
// always one the sensor could hold.
//
// Reading the device is the only source of these numbers in the app itself;
// this constructor is for callers that already hold them - tests, mostly.
func NewDPIStages(values [MaxStage]int) (DPIStages, error) {
	var out DPIStages
	for i, value := range values {
		if value < 1 || value > MaxDPI {
			return DPIStages{}, fmt.Errorf("stage %d: %d is outside 1-%d, the range the %s supports",
				i+1, value, MaxDPI, SensorName)
		}
		out.values[i] = value
	}
	return out, nil
}

// StageDescription describes one stage for menus and tooltips: "2400 DPI" when
// the device reported a value, and "" when it has not.
func (d DPIStages) StageDescription(stage int) string {
	value, ok := d.At(stage)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d DPI", value)
}

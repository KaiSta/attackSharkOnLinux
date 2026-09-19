package hidraw

import (
	"fmt"
	"sort"
)

// ReportKind is the direction of a HID main item.
type ReportKind string

const (
	Input   ReportKind = "input"
	Output  ReportKind = "output"
	Feature ReportKind = "feature"
)

// ReportField is one declared report: its direction, its report ID, and the
// number of bits the descriptor reserves for all fields with that ID.
type ReportField struct {
	Kind     ReportKind
	ReportID byte
	Bits     int
}

// Bytes is the report length in bytes, rounding up, excluding the report ID
// byte for numbered reports.
func (f ReportField) Bytes() int { return (f.Bits + 7) / 8 }

func (f ReportField) String() string {
	return fmt.Sprintf("%-7s id 0x%02x  %d bits (%d bytes)", f.Kind, f.ReportID, f.Bits, f.Bytes())
}

// ReportMap is which reports a HID report descriptor declares.
//
// It exists because the difference between an output report and a feature
// report decides whether a WebHID application can talk to a device at all:
// vendors' browser configuration tools typically send commands as an output
// report and read replies on an input report, while this receiver's vendor
// channel is a block of feature reports. Parsing the device's own descriptor
// settles which is which without guessing from a vendor's UI.
type ReportMap struct {
	Fields []ReportField
	// HasReportIDs is true when the descriptor uses explicit report IDs. A
	// descriptor without them declares a single report with ID 0.
	HasReportIDs bool
	// LongItems counts 0xFE long items, which carry no report information and
	// are skipped.
	LongItems int
}

// ParseReportDescriptor walks a HID report descriptor (item by item, as defined
// by the HID specification's item format) and collects every input, output and
// feature report it declares.
//
// Only the items that affect which reports exist are interpreted: report ID,
// report size and report count (globals) and the three report main items.
// Everything else - usages, logical ranges, collections - is skipped.
func ParseReportDescriptor(desc []byte) (*ReportMap, error) {
	m := &ReportMap{}
	seen := map[ReportField]int{}
	var reportID byte
	var reportSize, reportCount int

	for i := 0; i < len(desc); {
		prefix := desc[i]

		// Long items (0xFE) have a different layout and never describe reports.
		if prefix == 0xfe {
			if i+3 > len(desc) {
				return nil, fmt.Errorf("descriptor truncated in a long item at byte %d", i)
			}
			dataLen := int(desc[i+1])
			if i+3+dataLen > len(desc) {
				return nil, fmt.Errorf("descriptor truncated in a long item at byte %d (want %d bytes of data)", i, dataLen)
			}
			m.LongItems++
			i += 3 + dataLen
			continue
		}

		sizeCode := int(prefix & 0x03)
		size := [4]int{0, 1, 2, 4}[sizeCode]
		itemType := (prefix >> 2) & 0x03
		tag := prefix >> 4
		if i+1+size > len(desc) {
			return nil, fmt.Errorf("descriptor truncated in item at byte %d (type %d tag 0x%x)", i, itemType, tag)
		}

		// HID item data is little-endian.
		data := 0
		for k := 0; k < size; k++ {
			data |= int(desc[i+1+k]) << (8 * k)
		}
		i += 1 + size

		switch itemType {
		case 0: // main item
			var kind ReportKind
			switch tag {
			case 0x8:
				kind = Input
			case 0x9:
				kind = Output
			case 0xb:
				kind = Feature
			default:
				continue // Collection, End Collection, and reserved tags
			}
			field := ReportField{Kind: kind, ReportID: reportID}
			seen[field] += reportSize * reportCount
			continue

		case 1: // global item
			// Global item tags: 0x7 Report Size, 0x8 Report ID, 0x9 Report
			// Count. (Input/Output/Feature are main item tags, not these.)
			switch tag {
			case 0x7:
				reportSize = data
			case 0x8:
				reportID = byte(data)
				m.HasReportIDs = true
			case 0x9:
				reportCount = data
			}
		}
		// Local items (type 2) carry no report-identity information.
	}

	if len(seen) == 0 {
		return nil, fmt.Errorf("descriptor declares no input, output or feature reports")
	}

	m.Fields = make([]ReportField, 0, len(seen))
	for field, bits := range seen {
		field.Bits = bits
		m.Fields = append(m.Fields, field)
	}
	sort.Slice(m.Fields, func(i, j int) bool {
		if m.Fields[i].Kind != m.Fields[j].Kind {
			return kindRank(m.Fields[i].Kind) < kindRank(m.Fields[j].Kind)
		}
		return m.Fields[i].ReportID < m.Fields[j].ReportID
	})
	return m, nil
}

func kindRank(k ReportKind) int {
	switch k {
	case Input:
		return 0
	case Output:
		return 1
	default:
		return 2
	}
}

// Of returns every declared report of one direction, in report ID order.
func (m *ReportMap) Of(kind ReportKind) []ReportField {
	var out []ReportField
	for _, f := range m.Fields {
		if f.Kind == kind {
			out = append(out, f)
		}
	}
	return out
}

// Find returns the declared report with the given direction and ID.
func (m *ReportMap) Find(kind ReportKind, id byte) (ReportField, bool) {
	for _, f := range m.Fields {
		if f.Kind == kind && f.ReportID == id {
			return f, true
		}
	}
	return ReportField{}, false
}

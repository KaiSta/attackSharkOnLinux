package hidraw

import (
	"encoding/hex"
	"strings"
	"testing"
)

func descriptorFromHex(t *testing.T, s string) []byte {
	t.Helper()
	raw, err := hex.DecodeString(strings.NewReplacer(" ", "", "\n", "", "\t", "").Replace(s))
	if err != nil {
		t.Fatalf("bad hex in test: %v", err)
	}
	return raw
}

// A plain three-button mouse: no report IDs, one 3-byte input report.
const simpleMouse = `
	05 01 09 02 A1 01 09 01 A1 00
	05 09 19 01 29 03 15 00 25 01 95 03 75 01 81 02
	95 01 75 05 81 03
	05 01 09 30 09 31 15 81 25 7F 75 08 95 02 81 06
	C0 C0
`

func TestParseReportDescriptorWithoutReportIDs(t *testing.T) {
	m, err := ParseReportDescriptor(descriptorFromHex(t, simpleMouse))
	if err != nil {
		t.Fatalf("ParseReportDescriptor: %v", err)
	}
	if m.HasReportIDs {
		t.Error("HasReportIDs = true, want false: this descriptor declares no Report ID items")
	}
	inputs := m.Of(Input)
	if len(inputs) != 1 {
		t.Fatalf("got %d input reports, want 1: %v", len(inputs), inputs)
	}
	if inputs[0].ReportID != 0 {
		t.Errorf("input report ID = %d, want 0 (unnumbered)", inputs[0].ReportID)
	}
	// 3 buttons + 5 bits padding + X + Y = 8 + 16 = 24 bits.
	if inputs[0].Bits != 24 {
		t.Errorf("input bits = %d, want 24", inputs[0].Bits)
	}
	if got := m.Of(Output); len(got) != 0 {
		t.Errorf("got %d output reports, want 0: %v", len(got), got)
	}
}

// The shape that matters here: a vendor block declared as feature reports.
// This is what the X11 receiver presents, and it is why WebHID tools that send
// output reports cannot configure it.
const featureOnlyVendorBlock = `
	06 00 FF 09 01 A1 01
	85 08 75 08 95 07 09 01 B1 02
	85 09 75 08 95 07 09 01 B1 02
	C0
`

func TestParseReportDescriptorFeatureBlockIsNotOutput(t *testing.T) {
	m, err := ParseReportDescriptor(descriptorFromHex(t, featureOnlyVendorBlock))
	if err != nil {
		t.Fatalf("ParseReportDescriptor: %v", err)
	}
	if !m.HasReportIDs {
		t.Error("HasReportIDs = false, want true")
	}
	field, ok := m.Find(Feature, 0x08)
	if !ok {
		t.Fatal("report ID 8 is not declared as a feature report")
	}
	if field.Bytes() != 7 {
		t.Errorf("feature report 8 = %d bytes, want 7", field.Bytes())
	}
	if _, ok := m.Find(Output, 0x08); ok {
		t.Error("report ID 8 is also declared as an output report, which would let an output-report tool bind")
	}
	if got := m.Of(Output); len(got) != 0 {
		t.Errorf("got %d output reports, want 0: %v", len(got), got)
	}
}

// A device that does expose output report 8, as the vendor hub requires.
const outputReportEight = `
	06 00 FF 09 01 A1 01
	85 08 75 08 95 08 09 01 91 02
	85 08 75 08 95 08 09 01 81 02
	C0
`

func TestParseReportDescriptorFindsOutputReport(t *testing.T) {
	m, err := ParseReportDescriptor(descriptorFromHex(t, outputReportEight))
	if err != nil {
		t.Fatalf("ParseReportDescriptor: %v", err)
	}
	if _, ok := m.Find(Output, 0x08); !ok {
		t.Error("output report 8 not found, but the descriptor declares it")
	}
	if _, ok := m.Find(Input, 0x08); !ok {
		t.Error("input report 8 not found, but the descriptor declares it")
	}
}

func TestParseReportDescriptorSkipsLongItems(t *testing.T) {
	// A long item (0xFE) carrying four bytes, then a normal input report.
	desc := append([]byte{0xfe, 0x04, 0x01, 't', 'e', 's', 't'},
		descriptorFromHex(t, `06 00 FF 09 01 A1 01 75 08 95 01 81 02 C0`)...)
	m, err := ParseReportDescriptor(desc)
	if err != nil {
		t.Fatalf("ParseReportDescriptor: %v", err)
	}
	if m.LongItems != 1 {
		t.Errorf("LongItems = %d, want 1", m.LongItems)
	}
	if inputs := m.Of(Input); len(inputs) != 1 || inputs[0].Bits != 8 {
		t.Errorf("input reports = %v, want one 8-bit report", inputs)
	}
}

func TestParseReportDescriptorRejectsTruncatedInput(t *testing.T) {
	if _, err := ParseReportDescriptor([]byte{0x05}); err == nil {
		t.Error("ParseReportDescriptor accepted an item with a missing data byte")
	}
	if _, err := ParseReportDescriptor(descriptorFromHex(t, `75 08 95 01 81`)); err == nil {
		t.Error("ParseReportDescriptor accepted a truncated main item")
	}
	if _, err := ParseReportDescriptor(nil); err == nil {
		t.Error("ParseReportDescriptor accepted an empty descriptor")
	}
}

func TestReportFieldStringIsReadable(t *testing.T) {
	field := ReportField{Kind: Feature, ReportID: 8, Bits: 56}
	got := field.String()
	for _, want := range []string{"feature", "id 0x08", "56 bits", "7 bytes"} {
		if !strings.Contains(got, want) {
			t.Errorf("ReportField.String() = %q, want it to contain %q", got, want)
		}
	}
}

package mouse

import (
	"errors"
	"fmt"
	"testing"
)

// pollingReport builds a feature report the way the device stores one, so the
// checksum and offsets are exercised rather than hand-written twice.
func pollingReport(code byte, profile byte) []byte {
	buf := make([]byte, PollingReportLen)
	buf[0] = PollingReportID
	buf[1] = PollingReportLen
	buf[2] = profile
	buf[3] = code
	buf[4] = code ^ 0xff
	return buf
}

func TestPollingRateRoundTripsEverySupportedRate(t *testing.T) {
	for _, rate := range PollingRates {
		code, ok := pollingCode(rate)
		if !ok {
			t.Fatalf("%s has no code", rate)
		}
		cfg, err := ParsePollingConfig(pollingReport(code, 1))
		if err != nil {
			t.Fatalf("ParsePollingConfig for %s: %v", rate, err)
		}
		if cfg.Rate != rate {
			t.Errorf("code 0x%02x decoded as %s, want %s", code, cfg.Rate, rate)
		}
		if cfg.Profile != 1 {
			t.Errorf("profile = %d, want 1", cfg.Profile)
		}

		frame, err := BuildPollingRateChange(1, rate)
		if err != nil {
			t.Fatalf("BuildPollingRateChange(%s): %v", rate, err)
		}
		if got, want := fmt.Sprintf("% x", frame), fmt.Sprintf("% x", pollingReport(code, 1)); got != want {
			t.Errorf("frame for %s = %s, want %s", rate, got, want)
		}
		if again, err := ParsePollingConfig(frame); err != nil || again.Rate != rate {
			t.Errorf("the frame for %s does not parse back: %v (%v)", rate, again, err)
		}
	}
}

func TestPollingRateNames(t *testing.T) {
	want := map[PollingRate]string{
		Polling125:  "power saving",
		Polling250:  "office",
		Polling500:  "gaming",
		Polling1000: "eSports",
	}
	for rate, name := range want {
		if got := rate.Name(); got != name {
			t.Errorf("%s.Name() = %q, want %q", rate, got, name)
		}
		if got, want := rate.String(), fmt.Sprintf("%d Hz", int(rate)); got != want {
			t.Errorf("String() = %q, want %q", got, want)
		}
	}
	if got := PollingRate(300).Name(); got != "" {
		t.Errorf("an unsupported rate has the name %q, want none", got)
	}
	if PollingRate(300).Valid() {
		t.Error("300 Hz is reported as supported")
	}
}

func TestBuildPollingRateChangeRejectsBadInput(t *testing.T) {
	for _, rate := range []PollingRate{0, -125, 300, 4000} {
		if _, err := BuildPollingRateChange(1, rate); err == nil {
			t.Errorf("BuildPollingRateChange accepted %d Hz", int(rate))
		}
	}
	if _, err := BuildPollingRateChange(0, Polling1000); err == nil {
		t.Error("BuildPollingRateChange accepted profile 0")
	}
}

// A bad read must not turn into a plausible rate: the checksum and the code are
// both what stand between leftover bytes and a number shown to the user.
func TestParsePollingConfigRejectsBadReports(t *testing.T) {
	good := pollingReport(0x02, 1)

	short := good[:PollingReportLen-1]
	if _, err := ParsePollingConfig(short); err == nil {
		t.Error("a short report parsed")
	}

	wrongID := append([]byte(nil), good...)
	wrongID[0] = DPIReportID
	if _, err := ParsePollingConfig(wrongID); err == nil {
		t.Error("a report with the wrong ID parsed")
	}

	badSum := append([]byte(nil), good...)
	badSum[4] ^= 0xff
	if _, err := ParsePollingConfig(badSum); err == nil {
		t.Error("a report with a bad checksum parsed")
	}

	unknownCode := append([]byte(nil), good...)
	unknownCode[3] = 0x10
	unknownCode[4] = 0x10 ^ 0xff
	if _, err := ParsePollingConfig(unknownCode); err == nil {
		t.Error("a report with an unknown rate code parsed")
	}
}

// The read has to ask the mailbox for exactly this report and length: the
// firmware rejects a request whose length does not match the report.
func TestReadPollingRateAsksForTheRightLength(t *testing.T) {
	zeroDelay(t)
	dev := &fakeFeature{grant: true, polling: pollingReport(0x01, 1)}

	cfg, err := ReadPollingRate(dev)
	if err != nil {
		t.Fatalf("ReadPollingRate: %v", err)
	}
	if cfg.Rate != Polling1000 {
		t.Errorf("rate = %s, want 1000 Hz", cfg.Rate)
	}
	if len(dev.writes) != 1 {
		t.Fatalf("wrote %d frames, want the permission request only", len(dev.writes))
	}
	want := []byte{0xa0, PollingReportID, PollingReportLen, 0x00, defaultProfile, 0x00, 0x00, 0x00}
	if got := fmt.Sprintf("% x", dev.writes[0]); got != fmt.Sprintf("% x", want) {
		t.Errorf("permission request = %s, want %s", got, want)
	}
	if last := dev.reads[len(dev.reads)-1]; last.id != PollingReportID || last.size != PollingReportLen {
		t.Errorf("read %+v, want report 0x%02x of %d bytes", last, PollingReportID, PollingReportLen)
	}
}

func TestReadPollingRateReportsARefusal(t *testing.T) {
	zeroDelay(t)
	dev := &fakeFeature{grant: false, polling: pollingReport(0x01, 1)}

	if _, err := ReadPollingRate(dev); !errors.Is(err, ErrNoReadPermission) {
		t.Errorf("ReadPollingRate = %v, want a refusal", err)
	}
}

// The write keeps the profile the device says it is using, so a change made
// while another profile is selected does not silently land on profile 1.
func TestApplyPollingRatePreservesTheReportedProfile(t *testing.T) {
	zeroDelay(t)
	dev := &fakeFeature{grant: true, polling: pollingReport(0x08, 3)}

	frame, err := ApplyPollingRate(dev, Polling500)
	if err != nil {
		t.Fatalf("ApplyPollingRate: %v", err)
	}
	if want := []byte{0x06, 0x09, 0x03, 0x02, 0xfd, 0x00, 0x00, 0x00, 0x00}; fmt.Sprintf("% x", frame) != fmt.Sprintf("% x", want) {
		t.Errorf("frame = % x, want % x", frame, want)
	}
	if got := dev.writes[len(dev.writes)-1]; fmt.Sprintf("% x", got) != fmt.Sprintf("% x", frame) {
		t.Errorf("last write = % x, want the frame % x", got, frame)
	}
}

// A refused read is not a reason to refuse the write: this report holds only
// the rate, so the change goes ahead for the default profile.
func TestApplyPollingRateWritesWithoutARead(t *testing.T) {
	zeroDelay(t)
	dev := &fakeFeature{grant: false, polling: pollingReport(0x01, 1)}

	frame, err := ApplyPollingRate(dev, Polling250)
	if err != nil {
		t.Fatalf("ApplyPollingRate: %v", err)
	}
	if frame[2] != defaultProfile {
		t.Errorf("profile = %d, want the default %d", frame[2], defaultProfile)
	}
	if last := dev.writes[len(dev.writes)-1]; last[0] != PollingReportID || last[3] != 0x04 {
		t.Errorf("last write = % x, want a polling-rate write of 250 Hz", last)
	}
}

package mouse

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

// zeroDelay keeps the handshake tests from waiting the firmware's 250 ms.
func zeroDelay(t *testing.T) {
	t.Helper()
	previous, previousRetry := permissionWait, permissionRetryWait
	permissionWait, permissionRetryWait = 0, 0
	t.Cleanup(func() { permissionWait, permissionRetryWait = previous, previousRetry })
}

// dpiReport builds a feature report the way the device stores one, so the
// checksum and field offsets are exercised rather than hand-written twice.
type stageCode struct {
	x, y   byte
	double bool
}

func dpiReport(stages [6]stageCode, active byte) []byte {
	buf := make([]byte, DPIReportLen)
	buf[0] = DPIReportID
	buf[1] = byte(DPIReportLen)
	buf[2] = 0x01 // profile
	buf[3] = 0x00 // lift-off distance | angle snap
	buf[4] = 0x01 // motion sync | ripple control
	buf[5] = 0x3f // all six stages active
	buf[24] = active
	for i, s := range stages {
		buf[8+i] = s.x
		buf[16+i] = s.y
		if s.double {
			buf[6] |= 1 << i
		}
	}
	var sum uint16
	for _, b := range buf[3:50] {
		sum += uint16(b)
	}
	binary.BigEndian.PutUint16(buf[50:52], sum)
	return buf
}

// The factory-visible default mapping the upstream driver ships with, which is
// the only real capture available for these codes.
func defaultStages() [6]stageCode {
	return [6]stageCode{
		{x: 0x12}, {x: 0x25}, {x: 0x38}, {x: 0x4b}, {x: 0x75}, {x: 0x81, y: 0x01, double: true},
	}
}

type featureCall struct {
	id   byte
	size int
}

type fakeFeature struct {
	writes [][]byte
	reads  []featureCall
	grant  bool
	// refusals is how many mailbox replies to answer without a grant before
	// granting, for testing the retry path.
	refusals     int
	mailboxReads int
	dpi          []byte
	polling      []byte
	setErr       error
	readErr      error
}

func (f *fakeFeature) SetFeature(buf []byte) error {
	f.writes = append(f.writes, append([]byte(nil), buf...))
	return f.setErr
}

func (f *fakeFeature) GetFeature(id byte, size int) ([]byte, error) {
	f.reads = append(f.reads, featureCall{id: id, size: size})
	if f.readErr != nil {
		return nil, f.readErr
	}
	switch id {
	case PermissionReportID:
		f.mailboxReads++
		reply := make([]byte, size)
		if f.grant && f.mailboxReads > f.refusals {
			reply[1] = 0x01
		}
		return reply, nil
	case DPIReportID:
		return f.dpi, nil
	case PollingReportID:
		return f.polling, nil
	default:
		return nil, errors.New("unexpected report")
	}
}

func TestPermissionRequestLayout(t *testing.T) {
	got := PermissionRequest(DPIReportID, DPIReportLen, 0x01)
	want := []byte{0xa0, 0x04, 0x38, 0x00, 0x01, 0x00, 0x00, 0x00}
	if len(got) != len(want) {
		t.Fatalf("PermissionRequest() has %d bytes, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("PermissionRequest() = % x, want % x", got, want)
		}
	}
}

func TestReadDPIConfigHandshake(t *testing.T) {
	zeroDelay(t)
	dev := &fakeFeature{grant: true, dpi: dpiReport(defaultStages(), 2)}

	cfg, err := ReadDPIConfig(dev)
	if err != nil {
		t.Fatalf("ReadDPIConfig: %v", err)
	}
	if got, want := cfg.Stages.String(), "800/1600/2400/3200/5000/22000"; got != want {
		t.Errorf("stages = %q, want %q", got, want)
	}
	if cfg.ActiveStage != 2 {
		t.Errorf("ActiveStage = %d, want 2", cfg.ActiveStage)
	}
	if cfg.ActiveMask != 0x3f {
		t.Errorf("ActiveMask = 0x%02x, want 0x3f", cfg.ActiveMask)
	}

	// Exactly one write, and it is the mailbox frame; the settings themselves
	// are never written.
	if len(dev.writes) != 1 {
		t.Fatalf("%d writes, want 1", len(dev.writes))
	}
	if got := dev.writes[0]; len(got) != 8 || got[0] != PermissionReportID {
		t.Errorf("wrote % x, want a single 0xA0 mailbox frame", got)
	}
	wantReads := []featureCall{{PermissionReportID, 8}, {DPIReportID, DPIReportLen}}
	if len(dev.reads) != len(wantReads) {
		t.Fatalf("reads = %v, want %v", dev.reads, wantReads)
	}
	for i := range wantReads {
		if dev.reads[i] != wantReads[i] {
			t.Errorf("read %d = %+v, want %+v", i, dev.reads[i], wantReads[i])
		}
	}
}

// A mailbox reply that is present but not a grant means the firmware refused;
// the request is retried a bounded number of times, and nothing may be read
// afterwards, because that read would return residue.
func TestReadDPIConfigRefused(t *testing.T) {
	zeroDelay(t)
	dev := &fakeFeature{grant: false, dpi: dpiReport(defaultStages(), 1)}

	_, err := ReadDPIConfig(dev)
	if !errors.Is(err, ErrNoReadPermission) {
		t.Fatalf("error = %v, want ErrNoReadPermission", err)
	}
	if len(dev.writes) != permissionAttempts {
		t.Errorf("%d permission requests, want %d", len(dev.writes), permissionAttempts)
	}
	for _, call := range dev.reads {
		if call.id != PermissionReportID {
			t.Errorf("read %+v after a refusal, want only mailbox reads", call)
		}
	}
}

// A refusal that clears on a second attempt must still produce a configuration:
// measured on hardware, back-to-back reads were refused about once in eight.
func TestReadDPIConfigRetriesARefusedPermission(t *testing.T) {
	zeroDelay(t)
	dev := &fakeFeature{
		grant:    true,
		refusals: 2,
		dpi:      dpiReport(defaultStages(), 3),
	}

	cfg, err := ReadDPIConfig(dev)
	if err != nil {
		t.Fatalf("ReadDPIConfig: %v", err)
	}
	if cfg.ActiveStage != 3 {
		t.Errorf("ActiveStage = %d, want 3", cfg.ActiveStage)
	}
	if len(dev.writes) != 3 {
		t.Errorf("%d permission requests, want 3", len(dev.writes))
	}
}

func TestReadDPIConfigSurfacesWriteAndReadErrors(t *testing.T) {
	zeroDelay(t)

	dev := &fakeFeature{grant: true, setErr: errors.New("EPIPE")}
	if _, err := ReadDPIConfig(dev); err == nil || !strings.Contains(err.Error(), "EPIPE") {
		t.Errorf("write failure error = %v, want it to name EPIPE", err)
	}

	dev = &fakeFeature{grant: true, readErr: errors.New("EIO")}
	if _, err := ReadDPIConfig(dev); err == nil || !strings.Contains(err.Error(), "EIO") {
		t.Errorf("read failure error = %v, want it to name EIO", err)
	}
}

func TestParseDPIConfigRejectsBadReports(t *testing.T) {
	valid := dpiReport(defaultStages(), 2)

	tests := []struct {
		name string
		buf  []byte
		want string
	}{
		{
			name: "short",
			buf:  valid[:DPIReportPayload-1],
			want: "need at least",
		},
		{
			name: "wrong report id",
			buf:  append([]byte{0x05}, valid[1:]...),
			want: "report ID",
		},
		{
			name: "checksum",
			buf:  func() []byte { b := append([]byte(nil), valid...); b[12]++; return b }(),
			want: "checksum",
		},
		{
			name: "unknown code",
			buf:  dpiReport([6]stageCode{{x: 0x07}, {x: 0x25}, {x: 0x38}, {x: 0x4b}, {x: 0x75}, {x: 0x81, y: 1, double: true}}, 1),
			want: "does not know",
		},
		{
			name: "out of range",
			// The packed encoding reaches well past the sensor's ceiling.
			buf:  dpiReport([6]stageCode{{x: 0x12}, {x: 0x25}, {x: 0x38}, {x: 0x4b}, {x: 0x75}, {x: 0x00, y: 0x02}}, 1),
			want: "outside 1-",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseDPIConfig(tc.buf)
			if err == nil {
				t.Fatal("ParseDPIConfig accepted the report")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestDecodeDPI(t *testing.T) {
	tests := []struct {
		name string
		x, y byte
		dbl  bool
		want int
		ok   bool
	}{
		{name: "800", x: 0x12, want: 800, ok: true},
		{name: "1600", x: 0x25, want: 1600, ok: true},
		{name: "2400", x: 0x38, want: 2400, ok: true},
		{name: "3200", x: 0x4b, want: 3200, ok: true},
		{name: "5000", x: 0x75, want: 5000, ok: true},
		{name: "10000 is the 100-DPI scale", x: 0x75, y: 0x01, want: 10000, ok: true},
		{name: "22000", x: 0x81, y: 0x01, dbl: true, want: 22000, ok: true},
		{name: "20100", x: 0xeb, y: 0x01, dbl: true, want: 20100, ok: true},
		{name: "packed above 10000", x: 0xd0, dbl: true, want: 17700, ok: true},
		{name: "zero means unset", x: 0x00, y: 0x00},
		{name: "unknown code", x: 0x07},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := DecodeDPI(tc.x, tc.y, tc.dbl)
			if ok != tc.ok {
				t.Fatalf("DecodeDPI(0x%02x, 0x%02x, %t) ok = %t, want %t", tc.x, tc.y, tc.dbl, ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Errorf("DecodeDPI(0x%02x, 0x%02x, %t) = %d, want %d", tc.x, tc.y, tc.dbl, got, tc.want)
			}
		})
	}
}

// The whole point of the exercise: a report built the way this mouse stores its
// configuration must decode to the values that were encoded.
func TestParseDPIConfigRoundTrip(t *testing.T) {
	cfg, err := ParseDPIConfig(dpiReport(defaultStages(), 6))
	if err != nil {
		t.Fatalf("ParseDPIConfig: %v", err)
	}
	if !cfg.Stages.Configured() {
		t.Fatal("decoded stages report as unconfigured")
	}
	if cfg.ActiveStage != 6 {
		t.Errorf("ActiveStage = %d, want 6", cfg.ActiveStage)
	}
	if len(cfg.Raw) != DPIReportLen {
		t.Errorf("Raw is %d bytes, want the %d-byte response kept for debugging", len(cfg.Raw), DPIReportLen)
	}
	if got, want := cfg.String(), "profile 1, stage 6, 800/1600/2400/3200/5000/22000"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestParseDPIConfigAcceptsShortDescriptorPayload(t *testing.T) {
	// The descriptor declares 55 bytes of payload; a response that stops right
	// after the checksum must still parse, since the fields we need end there.
	buf := dpiReport(defaultStages(), 1)[:52]
	if _, err := ParseDPIConfig(buf); err != nil {
		t.Fatalf("ParseDPIConfig on a 52-byte response: %v", err)
	}
}

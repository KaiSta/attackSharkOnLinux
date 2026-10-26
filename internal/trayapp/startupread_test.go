package trayapp

import (
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	"attackshark-x11/internal/mouse"
)

// A refused read is asked for again, first quickly and then with growing pauses,
// because the firmware declines in bursts that pass within seconds.
func TestStartupReadSchedulesItsAttempts(t *testing.T) {
	var read startupRead
	now := time.Now()

	if read.due(now) {
		t.Fatal("a read that has not failed is due")
	}
	for i, wait := range startupReadWaits {
		if !read.failed(now) {
			t.Fatalf("failure %d did not schedule another attempt", i+1)
		}
		if got := read.next.Sub(now); got != wait {
			t.Errorf("attempt %d waits %s, want %s", i+1, got, wait)
		}
		if !read.due(now.Add(wait)) {
			t.Errorf("attempt %d is not due once its pause of %s has passed", i+1, wait)
		}
		if read.due(now) {
			t.Errorf("attempt %d is due before its pause has passed", i+1)
		}
	}
}

// A device that never answers must not be asked for the rest of the session.
func TestStartupReadGivesUpAfterItsAttempts(t *testing.T) {
	var read startupRead
	now := time.Now()

	for range startupReadWaits {
		read.failed(now)
	}
	if read.failed(now) {
		t.Fatalf("scheduled another attempt after %d failures", len(startupReadWaits))
	}
	if read.due(now.Add(24 * time.Hour)) {
		t.Error("an attempt is due after the attempts were exhausted")
	}
}

// A successful read ends the schedule, and a later refusal starts a whole new
// budget rather than resuming an exhausted one.
func TestStartupReadSuccessClearsTheSchedule(t *testing.T) {
	var read startupRead
	now := time.Now()

	read.failed(now)
	read.succeeded()
	if read.due(now.Add(time.Hour)) {
		t.Error("an attempt is due after the read succeeded")
	}
	if read.tries != 0 {
		t.Errorf("tries = %d after success, want 0", read.tries)
	}
	if !read.failed(now) {
		t.Error("a refusal after a success scheduled nothing")
	}
	if got := read.next.Sub(now); got != startupReadWaits[0] {
		t.Errorf("the attempt after a success waits %s, want the first pause %s",
			got, startupReadWaits[0])
	}
}

// stubFeature answers the 0xA0 handshake without hardware, refusing the first
// mailbox reads the way the firmware does while it is still cooling down.
type stubFeature struct {
	refusals int
	mailbox  int
	report   []byte
}

func (f *stubFeature) SetFeature([]byte) error { return nil }

func (f *stubFeature) GetFeature(id byte, size int) ([]byte, error) {
	switch id {
	case mouse.PermissionReportID:
		f.mailbox++
		reply := make([]byte, size)
		if f.mailbox > f.refusals {
			reply[1] = 0x01
		}
		return reply, nil
	case mouse.DPIReportID:
		return f.report, nil
	default:
		return nil, fmt.Errorf("unexpected read of report 0x%02x", id)
	}
}

// dpiReport builds the configuration report the device would answer with, so the
// decoder is exercised rather than bypassed.
func dpiReport(t *testing.T, values []int, active byte) []byte {
	t.Helper()

	buf := make([]byte, mouse.DPIReportLen)
	buf[0] = mouse.DPIReportID
	buf[1] = byte(mouse.DPIReportLen)
	buf[2] = 0x01 // profile
	buf[5] = 0x3f // all six stages enabled
	buf[24] = active
	for i, value := range values {
		x, y, double, ok := mouse.EncodeDPI(value)
		if !ok {
			t.Fatalf("EncodeDPI(%d) is not a value the sensor stores", value)
		}
		buf[8+i] = x
		buf[16+i] = y
		if double {
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

// The failure this fixes: a refusal at open used to be final for the session, so
// the tray reported "DPI stage unknown" and the DPI value menu could not be used
// at all. Now the loop asks again once the pause has passed.
func TestRefusedStartupReadIsRetried(t *testing.T) {
	a := testApp(configured(t))
	a.opt.ReadDPI = true

	// The first burst is refused, exactly as measured on hardware after a reboot.
	dev := &stubFeature{refusals: 3, report: dpiReport(t, []int{800, 1600, 2400, 3200, 5000, 22000}, 4)}

	a.readDeviceDPI(dev)
	if a.haveStage {
		t.Fatal("a refused read was recorded as a stage")
	}
	if a.stageRead.next.IsZero() {
		t.Fatal("a refused read was not scheduled for another attempt")
	}

	// Nothing is read again until the pause has passed.
	a.retryStartupReads(dev)
	if a.haveStage {
		t.Fatal("an attempt ran before its pause had passed")
	}

	// Once the pause is over the same request is granted, and the stage and the
	// values arrive without the user having to reconnect by hand.
	a.stageRead.next = time.Now().Add(-time.Millisecond)
	a.retryStartupReads(dev)

	if !a.haveStage || a.stage.Stage != 4 {
		t.Fatalf("stage = %v (known %t) after the retry, want the device's stage 4",
			a.stage, a.haveStage)
	}
	if want := "800/1600/2400/3200/5000/22000"; a.deviceDPI.String() != want {
		t.Errorf("values = %q after the retry, want %q", a.deviceDPI, want)
	}
	if !a.stageRead.next.IsZero() {
		t.Error("a successful retry left another attempt scheduled")
	}
}

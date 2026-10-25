package trayapp

import (
	"strings"
	"testing"
	"time"

	"attackshark-x11/internal/mouse"
)

func pollingApp(t *testing.T) *app {
	t.Helper()
	a := testApp(configured(t))
	a.connected = true
	a.requests = make(chan settingRequest, 1)
	return a
}

func pollingRequest(rate mouse.PollingRate) settingRequest {
	return settingRequest{kind: settingPolling, rate: rate}
}

func pendingPollingChange(rate mouse.PollingRate, deadline time.Time) *pendingChange {
	return &pendingChange{
		kind:     settingPolling,
		report:   mouse.PollingReportID,
		rate:     rate,
		deadline: deadline,
	}
}

// The tick may only move when the device confirms the report that was written,
// and the DPI confirmation says nothing about the polling rate.
func TestPollingConfirmationMovesTheTick(t *testing.T) {
	a := pollingApp(t)
	a.polling = mouse.Polling1000
	a.havePolling = true
	a.pending = pendingPollingChange(mouse.Polling500, time.Now().Add(time.Second))

	if _, ok := a.resolveConfirmation(mouse.CommandConfirmation{
		ReportID: mouse.DPIReportID, Success: true,
	}); ok {
		t.Fatal("a DPI confirmation resolved the polling-rate change")
	}
	if a.polling != mouse.Polling1000 {
		t.Errorf("rate = %s after an unrelated confirmation, want 1000 Hz", a.polling)
	}

	outcome, ok := a.resolveConfirmation(mouse.CommandConfirmation{
		ReportID: mouse.PollingReportID, Success: true,
	})
	if !ok {
		t.Fatal("the polling-rate confirmation did not resolve the change")
	}
	if !outcome.success || outcome.change.rate != mouse.Polling500 || outcome.reason != "acknowledged" {
		t.Errorf("outcome = %+v, want an acknowledged switch to 500 Hz", outcome)
	}
	if a.polling != mouse.Polling500 || !a.havePolling {
		t.Errorf("rate = %s (known %t), want 500 Hz", a.polling, a.havePolling)
	}
	if a.pending != nil {
		t.Errorf("pending change = %+v after confirmation, want cleared", a.pending)
	}
}

// A rejection must leave the rate the device gave us, not the one that was asked for.
func TestRejectedPollingConfirmationKeepsTheReportedRate(t *testing.T) {
	a := pollingApp(t)
	a.polling = mouse.Polling250
	a.havePolling = true
	a.pending = pendingPollingChange(mouse.Polling1000, time.Now().Add(time.Second))

	outcome, ok := a.resolveConfirmation(mouse.CommandConfirmation{
		ReportID: mouse.PollingReportID, Success: false, Status: 0x01,
	})
	if !ok {
		t.Fatal("a rejection did not resolve the change")
	}
	if outcome.success || outcome.reason != "rejected" {
		t.Errorf("outcome = %+v, want a rejection", outcome)
	}
	if a.polling != mouse.Polling250 {
		t.Errorf("rate = %s after a rejection, want the reported 250 Hz", a.polling)
	}
}

func TestPollingConfirmationTimesOut(t *testing.T) {
	a := pollingApp(t)
	now := time.Now()
	a.polling = mouse.Polling125
	a.havePolling = true
	a.pending = pendingPollingChange(mouse.Polling1000, now.Add(time.Second))

	if _, ok := a.resolveTimeout(now); ok {
		t.Fatal("the change timed out before its deadline")
	}
	p, ok := a.resolveTimeout(now.Add(2 * time.Second))
	if !ok {
		t.Fatal("the change did not time out after its deadline")
	}
	if p.kind != settingPolling || p.rate != mouse.Polling1000 {
		t.Errorf("timed-out change = %+v, want a polling change to 1000 Hz", p)
	}
	if a.polling != mouse.Polling125 {
		t.Errorf("rate = %s after a timeout, want the reported 125 Hz", a.polling)
	}
	if a.pending != nil {
		t.Errorf("pending change = %+v after a timeout, want cleared", a.pending)
	}
}

func TestRequestPollingChangeQueuesWhenIdle(t *testing.T) {
	a := pollingApp(t)
	a.requestChange(pollingRequest(mouse.Polling500))

	select {
	case got := <-a.requests:
		if got.kind != settingPolling || got.rate != mouse.Polling500 {
			t.Errorf("queued %+v, want a polling change to 500 Hz", got)
		}
	default:
		t.Fatal("the click was not queued")
	}
}

func TestRequestPollingChangeIgnoresTheCurrentRate(t *testing.T) {
	a := pollingApp(t)
	a.polling = mouse.Polling1000
	a.havePolling = true

	a.requestChange(pollingRequest(mouse.Polling1000))
	select {
	case got := <-a.requests:
		t.Errorf("queued a switch to the current rate %s", got.rate)
	default:
	}
}

// An unknown rate is not the same as the requested one: without a reading the
// click has to go through, because the device may well need the change.
func TestRequestPollingChangeQueuesWhenTheRateIsUnknown(t *testing.T) {
	a := pollingApp(t)
	a.requestChange(pollingRequest(mouse.Polling1000))

	select {
	case got := <-a.requests:
		if got.rate != mouse.Polling1000 {
			t.Errorf("queued %s, want 1000 Hz", got.rate)
		}
	default:
		t.Fatal("the click was not queued although the rate is unknown")
	}
}

func TestPollingMenuTooltipStatesTheKnownRate(t *testing.T) {
	a := pollingApp(t)
	a.polling = mouse.Polling500
	a.havePolling = true

	got := a.pollingMenuTooltip()
	if !strings.Contains(got, "The mouse is set to 500 Hz.") {
		t.Errorf("tooltip = %q, want it to state the current rate", got)
	}
	if !strings.Contains(got, "tick follows the device's confirmation") {
		t.Errorf("tooltip = %q, want it to explain when the tick moves", got)
	}

	a.havePolling = false
	got = a.pollingMenuTooltip()
	if !strings.Contains(got, "has not been read") {
		t.Errorf("tooltip = %q, want it to admit the rate is not known", got)
	}
	if strings.Contains(got, "500 Hz") {
		t.Errorf("tooltip = %q claims a rate that was never read", got)
	}
}

func TestPollingItemTooltipDoesNotClaimToConfirm(t *testing.T) {
	got := pollingItemTooltip(mouse.Polling1000)
	if want := "Switch the mouse to 1000 Hz (eSports)."; got != want {
		t.Errorf("tooltip = %q, want %q", got, want)
	}
}

// The tooltip carries the rate only once the device has given one, and never as
// a measurement it did not make.
func TestTooltipIncludesThePollingRateWhenKnown(t *testing.T) {
	a := testApp(configured(t))
	a.connected = true
	a.haveBattery = true
	a.battery = mouse.Battery{Percent: 55, Charge: mouse.ChargeDischarging}
	a.haveStage = true
	a.stage = mouse.Stage{Stage: 2}

	if got := a.tooltipLocked(); strings.Contains(got, "polling") {
		t.Errorf("tooltip = %q mentions polling before it was read", got)
	}

	a.polling = mouse.Polling1000
	a.havePolling = true
	got := a.tooltipLocked()
	if want := "1000 Hz polling"; !strings.Contains(got, want) {
		t.Errorf("tooltip = %q, want it to contain %q", got, want)
	}
}

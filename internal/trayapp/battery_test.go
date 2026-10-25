package trayapp

import (
	"strings"
	"testing"
	"time"

	"attackshark-x11/internal/mouse"
)

// The firmware can report 100 % before its gauge has settled, so the first frame
// after a device open must not reach the tooltip, the menu or the alerts.
func TestFirstBatteryFrameAfterOpeningIsHeldBack(t *testing.T) {
	a := testApp(configured(t))
	a.connected = true
	now := time.Now()

	published, changed, silent := a.acceptBatteryLocked(
		mouse.Battery{Percent: 100, Charge: mouse.ChargeCharging}, now)
	if published {
		t.Fatal("the first frame after opening was published")
	}
	if changed || silent != 0 {
		t.Errorf("held frame reported changed=%t silent=%s, want neither", changed, silent)
	}
	if a.haveBattery {
		t.Error("a held frame counts as a reading")
	}
	if !a.batteryPrimed {
		t.Error("the frame did not count as the first one after opening")
	}
	if !strings.Contains(a.tooltipLocked(), "waiting for the first report") {
		t.Errorf("tooltip = %q, want it to say the battery is still waiting", a.tooltipLocked())
	}
}

func TestSecondBatteryFrameIsPublished(t *testing.T) {
	a := testApp(configured(t))
	now := time.Now()
	a.connected = true
	a.acceptBatteryLocked(mouse.Battery{Percent: 100, Charge: mouse.ChargeCharging}, now)

	second := mouse.Battery{Percent: 64, Charge: mouse.ChargeDischarging}
	published, changed, silent := a.acceptBatteryLocked(second, now.Add(2*time.Second))
	if !published {
		t.Fatal("the second frame was held back")
	}
	if !changed {
		t.Error("the second frame did not count as a change from nothing")
	}
	// The held frame is not a reading, so there is no gap to report yet.
	if silent != 0 {
		t.Errorf("silent = %s, want no gap before the first published reading", silent)
	}
	if a.battery != second || !a.haveBattery {
		t.Errorf("battery = %v (known %t), want %v", a.battery, a.haveBattery, second)
	}
}

func TestUnchangedBatteryFrameIsNotAChange(t *testing.T) {
	a := testApp(configured(t))
	now := time.Now()
	battery := mouse.Battery{Percent: 75, Charge: mouse.ChargeDischarging}
	a.acceptBatteryLocked(battery, now)                    // held
	a.acceptBatteryLocked(battery, now.Add(2*time.Second)) // published

	if _, changed, _ := a.acceptBatteryLocked(battery, now.Add(4*time.Second)); changed {
		t.Error("an identical heartbeat counted as a change")
	}
}

// A completed charge is a reported state, not the "charging at 99 %" guess, and
// it must be named rather than shown as unknown.
func TestFullyChargedIsNamed(t *testing.T) {
	if got := mouse.ChargeFull.String(); got != "fully charged" {
		t.Errorf("ChargeFull = %q, want %q", got, "fully charged")
	}

	a := testApp(configured(t))
	a.connected = true
	a.haveBattery = true
	a.battery = mouse.Battery{Percent: 100, Charge: mouse.ChargeFull, ChargeRaw: 0x02}

	got := a.tooltipLocked()
	if !strings.Contains(got, "100 % (fully charged)") {
		t.Errorf("tooltip = %q, want it to say the battery is fully charged", got)
	}
	if strings.Contains(got, "(unknown)") {
		t.Errorf("tooltip = %q reports the charge state as unknown", got)
	}
}

func TestFullChargeAlertsOnceAndReArmsTheLowWarnings(t *testing.T) {
	a := testApp(configured(t))
	a.alerts.low20 = true
	a.alerts.low10 = true

	a.maybeAlert(mouse.Battery{Percent: 100, Charge: mouse.ChargeFull, ChargeRaw: 0x02})
	if !a.alerts.full {
		t.Error("the full state did not latch")
	}
	if a.alerts.low20 || a.alerts.low10 {
		t.Errorf("low warnings = %t/%t after a completed charge, want re-armed",
			a.alerts.low20, a.alerts.low10)
	}

	// Discharging clears the latch, so the next completed charge alerts again.
	a.maybeAlert(mouse.Battery{Percent: 99, Charge: mouse.ChargeDischarging})
	if a.alerts.full {
		t.Error("the full latch survived a discharging reading")
	}
}

package trayapp

import (
	"strings"
	"testing"
	"time"

	"attackshark-x11/internal/mouse"
)

// The point of these tests: a reading that has stopped arriving must say so,
// instead of looking exactly like a live one. The mouse's heartbeat is every two
// seconds, so silence is measurable.
func TestBatteryPhraseStates(t *testing.T) {
	const reading = "78 % (discharging)"

	tests := []struct {
		name string
		age  time.Duration
		want string
	}{
		{"just arrived", 0, reading},
		{"one heartbeat later", 2 * time.Second, reading},
		{"three missed heartbeats, still treated as current", 7 * time.Second, reading},
		{"four missed heartbeats, the first point worth dating", 8 * time.Second, reading + ", last read 8 s ago"},
		{"a reading worth dating", 12 * time.Second, reading + ", last read 12 s ago"},
		{"just under the quiet threshold", 59 * time.Second, reading + ", last read 59 s ago"},
		{"quiet", time.Minute, "no reports for 1 min; last read " + reading},
		{"quiet for a while", 12 * time.Minute, "no reports for 12 min; last read " + reading},
		{"quiet for hours", 3*time.Hour + 5*time.Minute, "no reports for 3 h 5 min; last read " + reading},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := batteryPhrase(reading, tc.age); got != tc.want {
				t.Errorf("batteryPhrase(age %s) = %q, want %q", tc.age, got, tc.want)
			}
		})
	}

	// The number must always survive: an age is added to the reading, never
	// instead of it.
	for _, age := range []time.Duration{0, 30 * time.Second, 5 * time.Minute, 4 * time.Hour} {
		if got := batteryPhrase(reading, age); !strings.Contains(got, reading) {
			t.Errorf("age %s: %q dropped the reading itself", age, got)
		}
	}
}

func TestFormatAge(t *testing.T) {
	tests := []struct {
		age  time.Duration
		want string
	}{
		{0, "0 s"},
		{9 * time.Second, "9 s"},
		{59 * time.Second, "59 s"},
		{time.Minute, "1 min"},
		{90 * time.Second, "1 min"},
		{59 * time.Minute, "59 min"},
		{time.Hour, "1 h"},
		{time.Hour + time.Minute, "1 h 1 min"},
		{3*time.Hour + 5*time.Minute, "3 h 5 min"},
		// Past a day it keeps counting hours rather than inventing a "1 d": the
		// point is to convey how long a mouse has been quiet, not to be a
		// calendar.
		{26 * time.Hour, "26 h"},
		{26*time.Hour + 30*time.Minute, "26 h 30 min"},
	}
	for _, tc := range tests {
		if got := formatAge(tc.age); got != tc.want {
			t.Errorf("formatAge(%s) = %q, want %q", tc.age, got, tc.want)
		}
	}
}

func TestTooltipDatesAStaleReading(t *testing.T) {
	a := testApp(configured(t))
	a.connected = true
	a.haveBattery = true
	a.battery = mouse.Battery{Percent: 78, Charge: mouse.ChargeDischarging}
	a.batterySeen = time.Now().Add(-90 * time.Second)
	a.haveStage = true
	a.stage = mouse.Stage{Stage: 3}

	got := a.tooltipLocked()
	for _, want := range []string{"no reports for 1 min", "78 % (discharging)", "DPI stage 3 of 6 (2400 DPI)"} {
		if !strings.Contains(got, want) {
			t.Errorf("tooltip = %q, want it to contain %q", got, want)
		}
	}
}

func TestTooltipLeavesAFreshReadingAlone(t *testing.T) {
	a := testApp(mouse.DPIStages{})
	a.connected = true
	a.haveBattery = true
	a.battery = mouse.Battery{Percent: 78, Charge: mouse.ChargeDischarging}
	a.batterySeen = time.Now()
	a.haveStage = true
	a.stage = mouse.Stage{Stage: 3}

	if want, got := "Attack Shark X11 - 78 % (discharging) - DPI stage 3 of 6", a.tooltipLocked(); got != want {
		t.Errorf("tooltip = %q, want %q", got, want)
	}
	if strings.Contains(a.tooltipLocked(), "last read") {
		t.Error("a current reading was dated, which would be noise on every tooltip")
	}
}

// A missing timestamp means the age is unknown. Saying nothing is right;
// inventing an age is not.
func TestTooltipTreatsAMissingTimestampAsCurrent(t *testing.T) {
	a := testApp(mouse.DPIStages{})
	a.connected = true
	a.haveBattery = true
	a.battery = mouse.Battery{Percent: 42, Charge: mouse.ChargeDischarging}

	if got := a.tooltipLocked(); strings.Contains(got, "last read") || strings.Contains(got, "no reports") {
		t.Errorf("tooltip = %q, want no age claim without a timestamp", got)
	}
}

func TestBatteryMenuTitleDatesAStaleReading(t *testing.T) {
	a := testApp(mouse.DPIStages{})
	a.haveBattery = true
	a.battery = mouse.Battery{Percent: 78, Charge: mouse.ChargeDischarging}

	if got := batteryPhrase(a.battery.String(), a.batteryAgeLocked()); got != "78 % (discharging)" {
		t.Errorf("phrase with no timestamp = %q, want the bare reading", got)
	}

	a.batterySeen = time.Now().Add(-2 * time.Hour)
	if got := batteryPhrase(a.battery.String(), a.batteryAgeLocked()); !strings.HasPrefix(got, "no reports for 2 h;") {
		t.Errorf("phrase after two hours of silence = %q, want it to lead with the silence", got)
	}
}

// The thresholds are tied to the observed 2 s heartbeat, so moving one without
// the other should fail here.
func TestFreshnessThresholdsFitTheHeartbeat(t *testing.T) {
	if freshFor < 2*heartbeat {
		t.Errorf("freshFor = %s, which is less than two missed heartbeats (%s)", freshFor, 2*heartbeat)
	}
	if quietAfter <= freshFor {
		t.Errorf("quietAfter = %s must be longer than freshFor = %s", quietAfter, freshFor)
	}
	if quietAfter > time.Minute {
		t.Errorf("quietAfter = %s is longer than a minute; a mouse that has stopped reporting should be called out sooner", quietAfter)
	}
}

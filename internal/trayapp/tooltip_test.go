package trayapp

import (
	"strings"
	"testing"

	"attackshark-x11/internal/mouse"
)

func testApp(dpi mouse.DPIStages) *app {
	return &app{opt: Options{DPI: dpi, DPIConfigPath: "/tmp/dpi.conf"}}
}

func configured(t *testing.T) mouse.DPIStages {
	t.Helper()
	stages, err := mouse.ParseDPIStages("800,1600,2400,3200,6400,22000")
	if err != nil {
		t.Fatalf("parsing test values: %v", err)
	}
	return stages
}

func TestTooltipWithoutConfiguredDPI(t *testing.T) {
	a := testApp(mouse.DPIStages{})
	a.connected = true
	a.haveBattery = true
	a.battery = mouse.Battery{Percent: 78, Charge: mouse.ChargeDischarging}
	a.haveStage = true
	a.stageConfirmed = true
	a.stage = mouse.Stage{Stage: 3}

	got := a.tooltipLocked()
	if want := "Attack Shark X11 - 78 % (discharging) - DPI stage 3 of 6"; got != want {
		t.Errorf("tooltip = %q, want %q", got, want)
	}
	// Nothing may imply a DPI number the device never sent.
	if strings.Contains(got, "DPI)") || strings.Contains(got, "22000") {
		t.Errorf("tooltip invented a DPI value: %q", got)
	}
}

func TestTooltipWithConfiguredDPI(t *testing.T) {
	a := testApp(configured(t))
	a.connected = true
	a.haveBattery = true
	a.battery = mouse.Battery{Percent: 42, Charge: mouse.ChargeCharging}
	a.haveStage = true
	a.stageConfirmed = true
	a.stage = mouse.Stage{Stage: 3}

	if want, got := "Attack Shark X11 - 42 % (charging) - DPI stage 3 of 6 (2400 DPI)",
		a.tooltipLocked(); got != want {
		t.Errorf("tooltip = %q, want %q", got, want)
	}
}

func TestTooltipRememberedStageIsMarkedAsSuch(t *testing.T) {
	a := testApp(configured(t))
	a.connected = true
	a.haveStage = true
	a.stageConfirmed = false
	a.stage = mouse.Stage{Stage: 5}

	if want, got := "DPI stage 5 of 6, 6400 DPI (last seen)", a.tooltipLocked(); !strings.Contains(got, want) {
		t.Errorf("tooltip = %q, want it to contain %q", got, want)
	}
}

func TestTooltipStageUnknown(t *testing.T) {
	a := testApp(configured(t))
	a.connected = true
	if want, got := "DPI stage unknown (tap the mouse's DPI button to sync)", a.tooltipLocked(); !strings.Contains(got, want) {
		t.Errorf("tooltip = %q, want it to contain %q", got, want)
	}
}

func TestTooltipDisconnected(t *testing.T) {
	a := testApp(configured(t))
	if want, got := "not connected", a.tooltipLocked(); !strings.Contains(got, want) {
		t.Errorf("tooltip = %q, want it to mention being disconnected", got)
	}
}

func TestStageTitle(t *testing.T) {
	tests := []struct {
		name string
		dpi  mouse.DPIStages
		want string
	}{
		{
			name: "configured",
			dpi:  configured(t),
			want: "DPI: 6400 DPI (stage 5 of 6)",
		},
		{
			name: "not configured",
			want: "DPI: stage 5 of 6",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(tc.dpi)
			a.stage = mouse.Stage{Stage: 5}
			if got := a.stageTitleLocked(); got != tc.want {
				t.Errorf("stageTitleLocked() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDPIMenuTooltipMentionsWhereNumbersComeFrom(t *testing.T) {
	withValues := testApp(configured(t)).dpiMenuTooltip()
	if !strings.Contains(withValues, "from your settings") {
		t.Errorf("dpiMenuTooltip() = %q, want it to say where the values come from", withValues)
	}

	withoutValues := testApp(mouse.DPIStages{}).dpiMenuTooltip()
	if !strings.Contains(withoutValues, mouse.SensorName) || !strings.Contains(withoutValues, "22000 DPI") {
		t.Errorf("dpiMenuTooltip() = %q, want it to cite the vendor's published sensor facts", withoutValues)
	}
}

func TestShorten(t *testing.T) {
	if got := shorten("a   b\nc", 20); got != "a b c" {
		t.Errorf("shorten collapsed whitespace to %q", got)
	}
	long := strings.Repeat("x", 100)
	got := shorten(long, 10)
	if len(got) > 10 || !strings.HasSuffix(got, "...") {
		t.Errorf("shorten(%d chars, 10) = %q (len %d), want at most 10 ending in ...", len(long), got, len(got))
	}
	if got := shorten("abcdef", 3); got != "abc" {
		t.Errorf("shorten(abcdef, 3) = %q, want %q", got, "abc")
	}
	if got := shorten("abc", 3); got != "abc" {
		t.Errorf("shorten(abc, 3) = %q, want it unchanged", got)
	}
}

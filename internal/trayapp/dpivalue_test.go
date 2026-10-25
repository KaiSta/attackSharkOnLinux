package trayapp

import (
	"testing"
	"time"

	"attackshark-x11/internal/mouse"
)

func dpiValueRequest(value int) settingRequest {
	return settingRequest{kind: settingDPI, value: value}
}

func dpiValueApp(t *testing.T) *app {
	t.Helper()
	a := testApp(configured(t))
	a.connected = true
	a.requests = make(chan settingRequest, 1)
	a.stage = mouse.Stage{Stage: 4}
	a.haveStage = true
	return a
}

// The value applies to the stage the mouse is on at the moment of the click, not
// to whatever stage was current when the menu was built.
func TestDPIValueRequestUsesTheCurrentStage(t *testing.T) {
	a := dpiValueApp(t)
	a.requestChange(dpiValueRequest(4000))

	select {
	case got := <-a.requests:
		if got.kind != settingDPI || got.value != 4000 {
			t.Errorf("queued %+v, want 4000 DPI", got)
		}
		if got.stage != 4 {
			t.Errorf("queued for stage %d, want the current 4", got.stage)
		}
	default:
		t.Fatal("the click was not queued")
	}

	// The mouse moves to another stage; the next click follows it.
	a.stage = mouse.Stage{Stage: 2}
	a.requestChange(dpiValueRequest(2000))
	select {
	case got := <-a.requests:
		if got.stage != 2 {
			t.Errorf("queued for stage %d after the mouse moved, want 2", got.stage)
		}
	default:
		t.Fatal("the click after the stage change was not queued")
	}
}

func TestDPIValueRequestIgnoresAValueAlreadySet(t *testing.T) {
	a := dpiValueApp(t)
	// The configured mapping has stage 4 at 3200 DPI.
	a.requestChange(dpiValueRequest(3200))

	select {
	case got := <-a.requests:
		t.Errorf("queued a write of the value already set: %+v", got)
	default:
	}
}

// Without a stage from the device there is no way to know which stage to change,
// so the click is refused rather than applied to a guess.
func TestDPIValueRequestRefusedWhenTheStageIsUnknown(t *testing.T) {
	a := dpiValueApp(t)
	a.haveStage = false

	a.requestChange(dpiValueRequest(4000))
	select {
	case got := <-a.requests:
		t.Errorf("queued %+v with no stage from the device", got)
	default:
	}
}

// The configuration read before the write is what the labels should describe, so
// a confirmed write replaces the whole mapping rather than one number.
func TestDPIConfirmationAdoptsTheWrittenConfiguration(t *testing.T) {
	a := dpiValueApp(t)

	stages, err := mouse.NewDPIStages([mouse.MaxStage]int{800, 1600, 2400, 4000, 6400, 22000})
	if err != nil {
		t.Fatalf("building test values: %v", err)
	}
	a.pending = &pendingChange{
		kind:     settingDPI,
		report:   mouse.DPIReportID,
		stage:    4,
		value:    4000,
		dpi:      &mouse.DPIConfig{Stages: stages, ActiveStage: 4, Profile: 1},
		deadline: time.Now().Add(time.Second),
	}

	outcome, ok := a.resolveConfirmation(mouse.CommandConfirmation{
		ReportID: mouse.DPIReportID, Success: true,
	})
	if !ok {
		t.Fatal("the DPI confirmation did not resolve the change")
	}
	if !outcome.success || outcome.reason != "acknowledged" {
		t.Errorf("outcome = %+v, want an acknowledgement", outcome)
	}
	if got, _ := a.dpiStagesLocked().At(4); got != 4000 {
		t.Errorf("stage 4 = %d, want the written 4000", got)
	}
	if got, _ := a.dpiStagesLocked().At(1); got != 800 {
		t.Errorf("stage 1 = %d, want the 800 from the same configuration", got)
	}
	if want := "DPI: 4000 DPI (stage 4 of 6)"; a.stageTitleLocked() != want {
		t.Errorf("stageTitleLocked() = %q, want %q", a.stageTitleLocked(), want)
	}
}

// A rejected write must not relabel anything: the device never applied it.
func TestRejectedDPIChangeKeepsTheReportedValues(t *testing.T) {
	a := dpiValueApp(t)

	stages, err := mouse.NewDPIStages([mouse.MaxStage]int{800, 1600, 2400, 4000, 6400, 22000})
	if err != nil {
		t.Fatalf("building test values: %v", err)
	}
	a.pending = &pendingChange{
		kind:     settingDPI,
		report:   mouse.DPIReportID,
		stage:    4,
		value:    4000,
		dpi:      &mouse.DPIConfig{Stages: stages, ActiveStage: 4, Profile: 1},
		deadline: time.Now().Add(time.Second),
	}

	outcome, ok := a.resolveConfirmation(mouse.CommandConfirmation{
		ReportID: mouse.DPIReportID, Success: false, Status: 0x01,
	})
	if !ok {
		t.Fatal("the rejection did not resolve the change")
	}
	if outcome.success || outcome.reason != "rejected" {
		t.Errorf("outcome = %+v, want a rejection", outcome)
	}
	if got, _ := a.dpiStagesLocked().At(4); got != 3200 {
		t.Errorf("stage 4 = %d, want the reported 3200", got)
	}
}

func TestDPIChangeLabelNamesTheStageAndValue(t *testing.T) {
	p := pendingChange{kind: settingDPI, stage: 3, value: 6000}
	if want := "6000 DPI on stage 3"; p.label() != want {
		t.Errorf("label = %q, want %q", p.label(), want)
	}
}

// The menu covers 2000 DPI up to the sensor's ceiling in 2000 DPI steps.
func TestDPIValueStepsCoverTheSensor(t *testing.T) {
	if len(dpiValueSteps) != 11 {
		t.Fatalf("%d steps, want 11", len(dpiValueSteps))
	}
	if dpiValueSteps[0] != 2000 {
		t.Errorf("first step = %d, want 2000", dpiValueSteps[0])
	}
	if last := dpiValueSteps[len(dpiValueSteps)-1]; last != mouse.MaxDPI {
		t.Errorf("last step = %d, want %d", last, mouse.MaxDPI)
	}
	for i := 1; i < len(dpiValueSteps); i++ {
		if dpiValueSteps[i]-dpiValueSteps[i-1] != 2000 {
			t.Errorf("steps %d and %d are %d apart, want 2000",
				dpiValueSteps[i-1], dpiValueSteps[i], dpiValueSteps[i]-dpiValueSteps[i-1])
		}
	}
}

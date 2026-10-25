package trayapp

import (
	"testing"
	"time"

	"attackshark-x11/internal/mouse"
)

func stageApp(t *testing.T) *app {
	t.Helper()
	a := testApp(configured(t))
	a.connected = true
	a.requests = make(chan settingRequest, 1)
	return a
}

// stageRequest is the click a stage entry produces.
func stageRequest(stage int) settingRequest {
	return settingRequest{kind: settingStage, stage: stage}
}

func pendingStageChange(stage int, deadline time.Time) *pendingChange {
	return &pendingChange{
		kind:     settingStage,
		report:   mouse.DPIReportID,
		stage:    stage,
		deadline: deadline,
	}
}

// The tick may only move when the device confirms the report that was written.
// A confirmation for another feature report says nothing about the stage.
func TestConfirmationOnlyResolvesItsOwnReport(t *testing.T) {
	a := stageApp(t)
	a.pending = pendingStageChange(3, time.Now().Add(time.Second))
	a.stage = mouse.Stage{Stage: 1}
	a.haveStage = true

	if _, ok := a.resolveConfirmation(mouse.CommandConfirmation{
		ReportID: mouse.PollingReportID, Success: true,
	}); ok {
		t.Fatal("a polling-rate confirmation resolved the stage change")
	}
	if a.pending == nil || a.pending.stage != 3 {
		t.Fatalf("pending change = %+v after an unrelated confirmation, want stage 3", a.pending)
	}
	if a.stage.Stage != 1 {
		t.Errorf("stage moved to %d without its own confirmation", a.stage.Stage)
	}

	outcome, ok := a.resolveConfirmation(mouse.CommandConfirmation{
		ReportID: mouse.DPIReportID, Success: true,
	})
	if !ok {
		t.Fatal("the DPI confirmation did not resolve the pending change")
	}
	if !outcome.success || outcome.change.stage != 3 || outcome.reason != "acknowledged" {
		t.Errorf("outcome = %+v, want an acknowledged switch to stage 3", outcome)
	}
	if a.pending != nil {
		t.Errorf("pending change = %+v after confirmation, want cleared", a.pending)
	}
	if a.stage.Stage != 3 || !a.haveStage {
		t.Errorf("stage = %v (known %t), want stage 3", a.stage, a.haveStage)
	}
}

// A rejected write must leave the displayed stage alone: the mouse never switched.
func TestRejectedConfirmationKeepsTheReportedStage(t *testing.T) {
	a := stageApp(t)
	a.pending = pendingStageChange(5, time.Now().Add(time.Second))
	a.stage = mouse.Stage{Stage: 2}
	a.haveStage = true

	outcome, ok := a.resolveConfirmation(mouse.CommandConfirmation{
		ReportID: mouse.DPIReportID, Success: false, Status: 0x01,
	})
	if !ok {
		t.Fatal("a rejection did not resolve the pending change")
	}
	if outcome.success || outcome.change.stage != 5 || outcome.reason != "rejected" {
		t.Errorf("outcome = %+v, want a rejected switch to stage 5", outcome)
	}
	if a.stage.Stage != 2 {
		t.Errorf("stage = %d after a rejection, want the reported 2", a.stage.Stage)
	}
	if a.pending != nil {
		t.Errorf("pending change = %+v after a rejection, want cleared", a.pending)
	}
}

// Silence is not success: a window that closes without a reply must be reported,
// and must not move the stage.
func TestStageConfirmationTimesOut(t *testing.T) {
	a := stageApp(t)
	now := time.Now()
	a.pending = pendingStageChange(4, now.Add(time.Second))
	a.stage = mouse.Stage{Stage: 1}
	a.haveStage = true

	if _, ok := a.resolveTimeout(now); ok {
		t.Fatal("the change timed out before its deadline")
	}
	if a.pending == nil || a.pending.stage != 4 {
		t.Fatalf("pending change = %+v before the deadline, want stage 4", a.pending)
	}

	p, ok := a.resolveTimeout(now.Add(2 * time.Second))
	if !ok {
		t.Fatal("the change did not time out after its deadline")
	}
	if p.stage != 4 {
		t.Errorf("timed-out change = %+v, want stage 4", p)
	}
	if a.stage.Stage != 1 {
		t.Errorf("stage = %d after a timeout, want the reported 1", a.stage.Stage)
	}
	if a.pending != nil {
		t.Errorf("pending change = %+v after a timeout, want cleared", a.pending)
	}
}

// The configuration read carries the selected stage, so startup should not have
// to wait for an announcement before the label means anything.
func TestDeviceStageFromConfiguration(t *testing.T) {
	a := testApp(configured(t))

	if !a.applyDeviceStage(4) {
		t.Fatal("applyDeviceStage rejected a valid stage")
	}
	if a.stage.Stage != 4 {
		t.Errorf("stage = %d, want 4 from the configuration", a.stage.Stage)
	}
	if !a.haveStage {
		t.Error("the stage read from the device is not recorded as known")
	}
	if want := "DPI: 3200 DPI (stage 4 of 6)"; a.stageTitleLocked() != want {
		t.Errorf("stageTitleLocked() = %q, want %q", a.stageTitleLocked(), want)
	}
}

// A nonsensical active-stage byte must not replace the stage that is shown.
func TestDeviceStageOutsideRangeKeepsTheReportedStage(t *testing.T) {
	a := testApp(configured(t))
	a.stage = mouse.Stage{Stage: 2}
	a.haveStage = true

	for _, bad := range []int{0, -1, mouse.MaxStage + 1} {
		if a.applyDeviceStage(bad) {
			t.Errorf("applyDeviceStage accepted stage %d", bad)
		}
		if a.stage.Stage != 2 {
			t.Errorf("stage = %d after stage %d, want the reported 2", a.stage.Stage, bad)
		}
	}
}

func TestRequestStageChangeQueuesWhenIdle(t *testing.T) {
	a := stageApp(t)
	a.requestChange(stageRequest(2))

	select {
	case got := <-a.requests:
		if got.stage != 2 || got.kind != settingStage {
			t.Errorf("queued %+v, want a stage change to 2", got)
		}
	default:
		t.Fatal("the click was not queued")
	}
}

// One write at a time: a second click while a change is in flight is refused
// rather than queued, because two writes would make the reply ambiguous.
func TestRequestStageChangeRefusesWhileBusy(t *testing.T) {
	a := stageApp(t)
	a.pending = pendingStageChange(3, time.Now().Add(time.Second))
	a.requestChange(stageRequest(5))

	select {
	case got := <-a.requests:
		t.Errorf("queued %+v while a change was pending", got)
	default:
	}
}

func TestRequestStageChangeIgnoresTheCurrentStage(t *testing.T) {
	a := stageApp(t)
	a.stage = mouse.Stage{Stage: 2}
	a.haveStage = true

	a.requestChange(stageRequest(2))
	select {
	case got := <-a.requests:
		t.Errorf("queued a switch to the current stage %d", got.stage)
	default:
	}
}

func TestRequestStageChangeWhenDisconnected(t *testing.T) {
	a := stageApp(t)
	a.connected = false
	a.requestChange(stageRequest(2))

	select {
	case got := <-a.requests:
		t.Errorf("queued %+v with no device open", got)
	default:
	}
}

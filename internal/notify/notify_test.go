package notify

import (
	"testing"
	"time"
)

// A nil notifier is the documented "notifications are off" case, and every call
// site holds one unconditionally, so it has to stay harmless.
func TestNilNotifierIsANoOp(t *testing.T) {
	var n *Notifier
	if err := n.Notify("summary", "body"); err != nil {
		t.Errorf("Notify on a nil notifier = %v, want nil", err)
	}
}

// A notifier built but never connected is what an app started outside a desktop
// session ends up with.
func TestNotifierWithoutAConnectionIsANoOp(t *testing.T) {
	n := &Notifier{}
	if err := n.Notify("summary", "body"); err != nil {
		t.Errorf("Notify without a connection = %v, want nil", err)
	}
}

// This is the behaviour that replaced the old permanent "unusable" flag: a
// failure pauses the backend for one interval and then lets it be tried again.
func TestFailurePausesPostsOnlyUntilTheIntervalPasses(t *testing.T) {
	n := &Notifier{retry: time.Minute}

	if n.pausedLocked() {
		t.Error("a notifier that has never failed should not be paused")
	}

	n.failedAt = time.Now()
	if !n.pausedLocked() {
		t.Error("a fresh failure should pause the next post")
	}

	n.failedAt = time.Now().Add(-time.Minute - time.Second)
	if n.pausedLocked() {
		t.Error("posts should be allowed again once the retry interval has passed")
	}
}

// A zero interval would turn the window into a no-op, so it has to fall back to
// the documented default rather than accepting every post immediately.
func TestRetryIntervalFallsBackToTheDefault(t *testing.T) {
	n := &Notifier{}
	if got := n.retryInterval(); got != RetryInterval {
		t.Errorf("retryInterval() with no value set = %v, want %v", got, RetryInterval)
	}

	n.retry = 30 * time.Second
	if got := n.retryInterval(); got != 30*time.Second {
		t.Errorf("retryInterval() = %v, want 30s", got)
	}
}

// The default is part of the contract with the tray app, which retries a failed
// alert after exactly this long.
func TestDefaultRetryIntervalIsSlowEnoughToBeCheap(t *testing.T) {
	if RetryInterval < time.Minute {
		t.Errorf("RetryInterval = %v, want at least a minute: a faster retry turns a missing "+
			"notification daemon into background noise", RetryInterval)
	}
}

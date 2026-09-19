// Package notify posts desktop notifications over the session bus.
//
// It uses godbus, which is already a direct dependency of the tray library, so
// this adds no new module to the build. Everything here is best effort: if no
// notification daemon is listening the call fails and is reported to the
// caller, but a failure is not a reason to give up for the rest of the run. It
// only holds the backend off for RetryInterval, so a desktop whose notification
// service starts late - or is restarted - gets notifications again.
package notify

import (
	"fmt"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	destination = "org.freedesktop.Notifications"
	objectPath  = "/org/freedesktop/Notifications"
	iface       = "org.freedesktop.Notifications"
	appName     = "attackshark-x11"

	// RetryInterval is how long a failed post suppresses the next attempt. It
	// is deliberately slow: the failures this covers are structural (no
	// notification daemon on the bus, or one that has just crashed), and
	// retrying those on every battery change would be pointless traffic.
	RetryInterval = 5 * time.Minute
)

// Notifier posts notifications. A nil *Notifier is safe to use and does
// nothing, so callers can hold one unconditionally.
type Notifier struct {
	mu   sync.Mutex
	conn *dbus.Conn
	// retry is how long a failure suppresses the next attempt, defaulting to
	// RetryInterval. New sets it and nothing changes it afterwards, so it can
	// be read without the mutex.
	retry time.Duration
	// failedAt is when the last post failed. The zero value means the last
	// attempt did not fail, which is also the state before the first attempt.
	failedAt time.Time
}

// New connects to the session bus. It returns an error if no session bus is
// reachable, which is expected when the app is started outside a desktop
// session.
func New() (*Notifier, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("connecting to the session bus: %w", err)
	}
	return &Notifier{conn: conn, retry: RetryInterval}, nil
}

// Notify posts a notification. A nil *Notifier, one without a connection and
// one still inside its retry window all do nothing and report success, because
// the caller could not usefully act on a difference between them. A post that
// is actually attempted and fails is returned so the caller can log it.
func (n *Notifier) Notify(summary, body string) error {
	if n == nil {
		return nil
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	if n.conn == nil || n.pausedLocked() {
		return nil
	}

	obj := n.conn.Object(destination, dbus.ObjectPath(objectPath))
	call := obj.Call(iface+".Notify", 0,
		appName,                   // app_name
		uint32(0),                 // replaces_id
		"",                        // app_icon
		summary,                   // summary
		body,                      // body
		[]string{},                // actions
		map[string]dbus.Variant{}, // hints
		int32(-1),                 // expire_timeout, -1 means the daemon decides
	)
	if call.Err != nil {
		n.failedAt = time.Now()
		return call.Err
	}
	// A working post clears the window, so a daemon that comes back is used
	// immediately rather than after waiting out another interval.
	n.failedAt = time.Time{}
	return nil
}

// pausedLocked reports whether the last post failed recently enough that
// trying again now would only fail again. The caller must hold n.mu.
func (n *Notifier) pausedLocked() bool {
	if n.failedAt.IsZero() {
		return false
	}
	return time.Since(n.failedAt) < n.retryInterval()
}

// retryInterval is how long a failure suppresses further posts.
func (n *Notifier) retryInterval() time.Duration {
	if n.retry > 0 {
		return n.retry
	}
	return RetryInterval
}

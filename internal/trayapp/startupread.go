package trayapp

import "time"

// Retrying the reads made when the device is opened.
//
// The DPI configuration and the polling rate are not pushed on the status
// channel, so the tray asks for them when it opens the receiver. The firmware
// declines those requests in bursts: measured on hardware, a request that
// arrives while the mailbox is still cooling down is refused, and every refused
// request restarts that cooldown, so one burst of attempts can all fail while
// the next one, seconds later, is granted.
//
// A single burst at open was therefore not enough. A tray whose first attempt
// was refused reported "DPI stage unknown" for the rest of the session - which
// is the opposite of what the read is for - and the DPI value menu could not be
// used either, because it needs the stage. So a refused read is asked for again
// a few times, with growing pauses, and then left alone: a device that never
// answers (an older firmware without the mailbox, a mouse switched off) must not
// be poked every couple of seconds for as long as the tray runs.

// startupRead is how many times one of those reads has been retried and when the
// next attempt is due. The zero value means nothing is scheduled.
//
// It is owned by the goroutine that owns the device, like the reads themselves.
type startupRead struct {
	tries int
	next  time.Time
}

// startupReadWaits are the pauses before the retries. The first is short because
// the refusal is usually a burst that has already ended; later ones grow, so a
// receiver that cannot answer at all is not asked on every pass of the loop.
var startupReadWaits = []time.Duration{
	2 * time.Second,
	5 * time.Second,
	15 * time.Second,
	45 * time.Second,
	2 * time.Minute,
}

// failed records a refused attempt and schedules the next one. It reports
// whether another attempt is scheduled at all.
func (r *startupRead) failed(now time.Time) bool {
	r.tries++
	if r.tries > len(startupReadWaits) {
		r.next = time.Time{}
		return false
	}
	r.next = now.Add(startupReadWaits[r.tries-1])
	return true
}

// due reports whether the scheduled attempt has come around.
func (r *startupRead) due(now time.Time) bool {
	return !r.next.IsZero() && !now.Before(r.next)
}

// succeeded ends the schedule: there is nothing left to ask for.
func (r *startupRead) succeeded() {
	*r = startupRead{}
}

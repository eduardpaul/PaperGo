package connections

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Rates. Controlling is at human pace: ten a second per person and
// channel, bursting to twenty. Seeing over HTTP is twenty a second per
// person and connection, bursting to forty; cached answers do not count.
var (
	controlRate = rate.Limit(10)
	seeRate     = rate.Limit(20)
)

const (
	controlBurst = 20
	seeBurst     = 40
	// idleLimiter is how long a caller's unused limiter is kept.
	idleLimiter = 10 * time.Minute
)

type limiter struct {
	l    *rate.Limiter
	used time.Time
}

// limits are the callers' rate limiters, by caller and what they use.
type limits struct {
	mu sync.Mutex
	by map[string]*limiter
}

func newLimits() *limits { return &limits{by: map[string]*limiter{}} }

// allow takes one request of key at the use's rate, or says how long to
// wait.
func (l *limits) allow(key string, use Use, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.by[key]
	if entry == nil {
		r, burst := controlRate, controlBurst
		if use == UseSee {
			r, burst = seeRate, seeBurst
		}
		entry = &limiter{l: rate.NewLimiter(r, burst)}
		l.by[key] = entry
	}
	entry.used = now
	reservation := entry.l.ReserveN(now, 1)
	if delay := reservation.DelayFrom(now); delay > 0 {
		reservation.CancelAt(now)
		return false, delay
	}
	return true, 0
}

// sweep forgets limiters nobody used for a while.
func (l *limits) sweep(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, entry := range l.by {
		if now.Sub(entry.used) > idleLimiter {
			delete(l.by, key)
		}
	}
}

// limit refuses subject's request over their rate.
func (r *Runtime) limit(subject, connectionID, channel string, use Use) error {
	key := subject + "\x00" + connectionID
	if use == UseControl {
		key += "\x00" + channel
	}
	if ok, wait := r.limits.allow(string(use)+"\x00"+key, use, time.Now()); !ok {
		return &busy{code: "connection_rate_limited", detail: "too many requests through this connection; slow down", after: wait}
	}
	return nil
}

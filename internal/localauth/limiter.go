package localauth

import (
	"context"
	"sync"
	"time"
)

const (
	maxFailures  = 5
	failureWin   = 15 * time.Minute
	lockDuration = 15 * time.Minute

	// globalSlowDelay is the pause added to every login attempt while the
	// failures from all sources together reach maxFailures in failureWin.
	globalSlowDelay = time.Second
	// maxGlobalFailures bounds the memory of the global failure window.
	maxGlobalFailures = 1024
)

// limiter counts failed logins.
//
//   - Per source address: 5 failures in 15 minutes lock that source for 15
//     minutes, even with the right password.
//   - Globally: failures from every source are counted too, but they never
//     lock anyone out (a remote attacker could otherwise lock the operator
//     out from any address). Above the threshold every attempt is only slowed
//     down by globalSlowDelay.
type limiter struct {
	now    func() time.Time
	sleep  func(ctx context.Context, d time.Duration)
	mu     sync.Mutex
	scopes map[string]*scope
	global []time.Time
}

type scope struct {
	failures    []time.Time
	lockedUntil time.Time
}

func newLimiter(now func() time.Time) *limiter {
	return &limiter{now: now, sleep: sleepCtx, scopes: map[string]*scope{}}
}

// sleepCtx waits d or until ctx is done.
func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// blocked returns the remaining lock time of the source.
func (l *limiter) blocked(source string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if sc := l.scopes[source]; sc != nil {
		if now := l.now(); sc.lockedUntil.After(now) {
			return sc.lockedUntil.Sub(now), true
		}
	}
	return 0, false
}

// globalDelay returns the pause to apply to a login attempt: globalSlowDelay
// while the global failure count is at or above the threshold, else zero.
func (l *limiter) globalDelay() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.global = prune(l.global, l.now())
	if len(l.global) >= maxFailures {
		return globalSlowDelay
	}
	return 0
}

// wait applies the global delay, if any.
func (l *limiter) wait(ctx context.Context) {
	if d := l.globalDelay(); d > 0 {
		l.sleep(ctx, d)
	}
}

func (l *limiter) fail(source string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()

	sc := l.scopes[source]
	if sc == nil {
		sc = &scope{}
		l.scopes[source] = sc
	}
	sc.failures = append(prune(sc.failures, now), now)
	if len(sc.failures) >= maxFailures {
		sc.lockedUntil = now.Add(lockDuration)
		sc.failures = nil
	}

	l.global = append(prune(l.global, now), now)
	if len(l.global) > maxGlobalFailures {
		l.global = l.global[len(l.global)-maxGlobalFailures:]
	}
}

// prune drops the failures older than failureWin.
func prune(failures []time.Time, now time.Time) []time.Time {
	kept := failures[:0]
	for _, t := range failures {
		if now.Sub(t) < failureWin {
			kept = append(kept, t)
		}
	}
	return kept
}

// success clears the source counter (the global one only decays by time).
func (l *limiter) success(source string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.scopes, source)
}

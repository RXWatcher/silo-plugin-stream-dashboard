package server

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// minInterval is the minimum time that must elapse between two successful
// invocations of a rate-limited endpoint. The expensive read endpoints
// (/api/overview, /api/map) fan out across the plugin database and may trigger
// outbound geoip lookups, so a rapid-refresh client can amplify load well
// beyond the configured dashboard refresh cadence. Even though these routes are
// admin-only, an admin holding down refresh (or a buggy client) should not be
// able to hammer the backend faster than this floor.
//
// The limiter is per-process and per-route rather than per-admin: the host does
// not guarantee a trustworthy per-admin identity header, and trusting a
// spoofable one would let a caller bypass the floor by varying the value. A
// per-process floor is conservative but correct under all inputs.
const minInterval = 2 * time.Second

// rateLimiter enforces a per-key minimum interval between allowed calls. It is
// safe for concurrent use. The clock is injectable so tests can advance time
// deterministically.
type rateLimiter struct {
	interval time.Duration
	now      func() time.Time

	mu   sync.Mutex
	last map[string]time.Time
}

func newRateLimiter(interval time.Duration) *rateLimiter {
	return &rateLimiter{
		interval: interval,
		now:      time.Now,
		last:     make(map[string]time.Time),
	}
}

// allow reports whether a call for key may proceed now. When it returns false
// it also returns how long the caller should wait before retrying. A successful
// allow records the current time so the next call is gated by interval.
func (rl *rateLimiter) allow(key string) (bool, time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.now()
	if last, ok := rl.last[key]; ok {
		if elapsed := now.Sub(last); elapsed < rl.interval {
			return false, rl.interval - elapsed
		}
	}
	rl.last[key] = now
	return true, 0
}

// throttle wraps a handler so it rejects calls that arrive sooner than the
// limiter's minimum interval with 429 Too Many Requests and a Retry-After
// header. The route name keys the limiter so distinct expensive endpoints are
// throttled independently.
func (rl *rateLimiter) throttle(route string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ok, retry := rl.allow(route); !ok {
			seconds := int(retry.Seconds())
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeErr(w, http.StatusTooManyRequests, "rate_limited", "refresh too frequent; please slow down")
			return
		}
		next(w, r)
	}
}

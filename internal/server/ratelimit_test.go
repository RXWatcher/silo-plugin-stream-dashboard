package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RXWatcher/silo-plugin-stream-dashboard/internal/store"
)

func TestRateLimiterEnforcesMinimumInterval(t *testing.T) {
	now := time.Unix(0, 0)
	rl := newRateLimiter(2 * time.Second)
	rl.now = func() time.Time { return now }

	if ok, _ := rl.allow("map"); !ok {
		t.Fatal("first call should be allowed")
	}
	ok, retry := rl.allow("map")
	if ok {
		t.Fatal("immediate second call should be blocked")
	}
	if retry <= 0 || retry > 2*time.Second {
		t.Fatalf("retry = %v, want within (0, 2s]", retry)
	}

	// A different route key is throttled independently.
	if ok, _ := rl.allow("overview"); !ok {
		t.Fatal("first call for a distinct key should be allowed")
	}

	// Still inside the window.
	now = now.Add(time.Second)
	if ok, _ := rl.allow("map"); ok {
		t.Fatal("call inside interval should be blocked")
	}

	// Window elapsed.
	now = now.Add(2 * time.Second)
	if ok, _ := rl.allow("map"); !ok {
		t.Fatal("call after interval should be allowed")
	}
}

func TestExpensiveEndpointsAreRateLimited(t *testing.T) {
	st := &stubStore{
		counts:      store.Counts{Servers: store.ServerSummary{ByType: map[string]int{}}},
		mapSessions: []store.MapSession{},
	}
	h := New(Deps{Store: st, RefreshSeconds: 30})

	call := func(path string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Silo-User-Role", "admin")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	for _, path := range []string{"/api/overview", "/api/map"} {
		if code := call(path); code != http.StatusOK {
			t.Fatalf("first %s = %d, want 200", path, code)
		}
		if code := call(path); code != http.StatusTooManyRequests {
			t.Fatalf("second %s = %d, want 429", path, code)
		}
	}

	// Cheap endpoints stay unthrottled even under rapid repeats.
	for i := 0; i < 3; i++ {
		if code := call("/api/counts"); code != http.StatusOK {
			t.Fatalf("counts call %d = %d, want 200 (must not be rate limited)", i, code)
		}
	}
}

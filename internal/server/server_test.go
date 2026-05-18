package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIReportsNotConfiguredInsteadOfPanicking(t *testing.T) {
	h := New(Deps{})
	req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
}

func TestBoundedIntQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/history?page=999999&limit=999999", nil)
	if got := boundedIntQuery(req, "page", 1, 1, 10000); got != 10000 {
		t.Fatalf("page = %d, want 10000", got)
	}
	if got := boundedIntQuery(req, "limit", 50, 1, 500); got != 500 {
		t.Fatalf("limit = %d, want 500", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/history?page=-1&limit=nope", nil)
	if got := boundedIntQuery(req, "page", 1, 1, 10000); got != 1 {
		t.Fatalf("bad page = %d, want fallback 1", got)
	}
	if got := boundedIntQuery(req, "limit", 50, 1, 500); got != 50 {
		t.Fatalf("bad limit = %d, want fallback 50", got)
	}
}

func TestPluginBaseHref(t *testing.T) {
	tests := map[string]string{
		"/dashboard":                            "/",
		"/api/v1/plugins/stream-dashboard/":     "/api/v1/plugins/stream-dashboard/",
		"/api/v1/plugins/stream-dashboard/map":  "/api/v1/plugins/stream-dashboard/",
		"/api/v1/plugins/42/dashboard/sessions": "/api/v1/plugins/42/",
	}
	for path, want := range tests {
		if got := pluginBaseHref(path); got != want {
			t.Fatalf("pluginBaseHref(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestRefreshSecondsHasSafeDefault(t *testing.T) {
	if got := refreshSeconds(0); got != 30 {
		t.Fatalf("refreshSeconds(0) = %d, want 30", got)
	}
	if got := refreshSeconds(5); got != 5 {
		t.Fatalf("refreshSeconds(5) = %d, want 5", got)
	}
}

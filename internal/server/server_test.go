package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	pluginrt "github.com/RXWatcher/silo-plugin-stream-dashboard/internal/runtime"
	"github.com/RXWatcher/silo-plugin-stream-dashboard/internal/store"
)

type stubStore struct {
	config          pluginrt.Config
	configErr       error
	updateConfigErr error
	status          map[string]any
	statusErr       error
	sessions        []store.Session
	sessionsErr     error
	counts          store.Counts
	countsErr       error
	mapSessions     []store.MapSession
	mapErr          error
	readOnlyHistory store.PlaybackHistoryPage
	readOnlyErr     error
	readOnlyCalls   int
}

func (s *stubStore) GetAppConfig(context.Context) (pluginrt.Config, error) {
	return s.config, s.configErr
}

func (s *stubStore) UpdateAppConfig(context.Context, pluginrt.Config) error {
	return s.updateConfigErr
}

func (s *stubStore) Status(context.Context) (map[string]any, error) {
	return s.status, s.statusErr
}

func (s *stubStore) Sessions(context.Context) ([]store.Session, error) {
	return s.sessions, s.sessionsErr
}

func (s *stubStore) Counts(context.Context) (store.Counts, error) {
	return s.counts, s.countsErr
}

func (s *stubStore) MapSessions(context.Context) ([]store.MapSession, error) {
	return s.mapSessions, s.mapErr
}

func (s *stubStore) PlaybackHistoryReadOnly(context.Context, int, int) (store.PlaybackHistoryPage, error) {
	s.readOnlyCalls++
	return s.readOnlyHistory, s.readOnlyErr
}

func TestAPIReportsNotConfiguredInsteadOfPanicking(t *testing.T) {
	h := New(Deps{})
	req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	req.Header.Set("X-Silo-User-Role", "admin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
}

func TestNewDoesNotPanicWhenEmbeddedAssetsAreMissing(t *testing.T) {
	webFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html><body>dashboard</body></html>")},
	}

	h := New(Deps{WebFS: webFS})
	req := httptest.NewRequest(http.MethodGet, "/assets/index.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("asset status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestSafeSubReportsMissingDirectory(t *testing.T) {
	_, ok := safeSub(fstest.MapFS{}, "assets")
	if ok {
		t.Fatal("safeSub should report missing assets")
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

func TestRefreshSecondsHasSafeDefault(t *testing.T) {
	if got := refreshSeconds(0); got != 30 {
		t.Fatalf("refreshSeconds(0) = %d, want 30", got)
	}
	if got := refreshSeconds(5); got != 5 {
		t.Fatalf("refreshSeconds(5) = %d, want 5", got)
	}
}

func TestDashboardRoutesRequireAdminAccessInManifest(t *testing.T) {
	body, err := os.ReadFile("../../cmd/silo-plugin-stream-dashboard/manifest.json")
	if err != nil {
		t.Fatal(err)
	}

	var manifest struct {
		HTTPRoutes []struct {
			Path   string `json:"path"`
			Access string `json:"access"`
		} `json:"http_routes"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}

	accessByPath := map[string]string{}
	for _, route := range manifest.HTTPRoutes {
		accessByPath[route.Path] = route.Access
	}

	if got := accessByPath["/dashboard"]; got != "admin" {
		t.Fatalf("dashboard access = %q, want admin", got)
	}
	if got := accessByPath["/dashboard/*"]; got != "admin" {
		t.Fatalf("dashboard wildcard access = %q, want admin", got)
	}
}

func TestManifestHasSingleNavigableAdminEntry(t *testing.T) {
	body, err := os.ReadFile("../../cmd/silo-plugin-stream-dashboard/manifest.json")
	if err != nil {
		t.Fatal(err)
	}

	var manifest struct {
		HTTPRoutes []struct {
			Path           string `json:"path"`
			Navigable      bool   `json:"navigable"`
			NavigationKind string `json:"navigation_kind"`
		} `json:"http_routes"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}

	var navigableAdminRoutes []string
	for _, route := range manifest.HTTPRoutes {
		if route.Navigable && route.NavigationKind == "admin" {
			navigableAdminRoutes = append(navigableAdminRoutes, route.Path)
		}
	}

	if len(navigableAdminRoutes) != 1 {
		t.Fatalf("navigable admin routes = %v, want exactly one", navigableAdminRoutes)
	}
	if navigableAdminRoutes[0] != "/admin" {
		t.Fatalf("navigable admin route = %q, want /admin", navigableAdminRoutes[0])
	}
}

func TestConfigEndpointRejectsNonAdminRequests(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/api/config", strings.NewReader(`{}`))
	req.Header.Set("X-Silo-User-Role", "user")
	rec := httptest.NewRecorder()

	h := New(Deps{Store: &store.Store{}})
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestOverviewReturnsSectionHealthWhenHistoryFails(t *testing.T) {
	st := &stubStore{
		counts: store.Counts{
			Servers:  store.ServerSummary{Total: 2, Online: 2, Offline: 0, ByType: map[string]int{"silo": 2}},
			Sessions: store.SessionCounts{Active: 1, DirectPlay: 1, Transcoding: 0},
			History:  store.HistoryCounts{Total: 9, Today: 2, ThisWeek: 4, ThisMonth: 9},
			Users:    store.UserCounts{Unique: 3, ActiveToday: 2, ActiveThisWeek: 3},
			Media:    map[string]int{"movie": 9},
		},
		sessions:    []store.Session{{ID: "s1"}},
		mapSessions: []store.MapSession{},
		readOnlyErr: errors.New("history query failed"),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	req.Header.Set("X-Silo-User-Role", "admin")
	rec := httptest.NewRecorder()

	New(Deps{Store: st, RefreshSeconds: 30}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var payload struct {
		Counts struct {
			Servers struct {
				Total int `json:"total"`
			} `json:"servers"`
		} `json:"counts"`
		Health map[string]struct {
			OK      bool   `json:"ok"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"health"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}

	if payload.Counts.Servers.Total != 2 {
		t.Fatalf("counts.servers.total = %d, want 2", payload.Counts.Servers.Total)
	}
	if payload.Health["history"].OK {
		t.Fatal("history health unexpectedly ok")
	}
	if payload.Health["history"].Code != "history_failed" {
		t.Fatalf("history code = %q, want history_failed", payload.Health["history"].Code)
	}
}

func TestOverviewUsesReadOnlyHistoryPath(t *testing.T) {
	st := &stubStore{
		counts: store.Counts{Servers: store.ServerSummary{ByType: map[string]int{}}},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	req.Header.Set("X-Silo-User-Role", "admin")
	rec := httptest.NewRecorder()

	New(Deps{Store: st, RefreshSeconds: 30}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if st.readOnlyCalls == 0 {
		t.Fatal("read-only history path was not called")
	}
}

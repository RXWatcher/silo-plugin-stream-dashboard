package server

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/hashicorp/go-hclog"

	pluginrt "github.com/RXWatcher/continuum-plugin-stream-dashboard/internal/runtime"
	"github.com/RXWatcher/continuum-plugin-stream-dashboard/internal/store"
)

type Deps struct {
	Store                  serverStore
	Logger                 hclog.Logger
	WebFS                  fs.FS
	RefreshSeconds         int
	HistoryRetentionPolicy store.RetentionPolicy
}

var errForbidden = errors.New("admin access required")

type serverStore interface {
	GetAppConfig(ctx context.Context) (pluginrt.Config, error)
	UpdateAppConfig(ctx context.Context, cfg pluginrt.Config) error
	Status(ctx context.Context) (map[string]any, error)
	Sessions(ctx context.Context) ([]store.Session, error)
	Counts(ctx context.Context) (store.Counts, error)
	MapSessions(ctx context.Context) ([]store.MapSession, error)
	PlaybackHistory(ctx context.Context, limit, offset int, policy store.RetentionPolicy, realtime bool) (store.PlaybackHistoryPage, error)
	PlaybackHistoryReadOnly(ctx context.Context, limit, offset int) (store.PlaybackHistoryPage, error)
}

type sectionHealth struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type overviewResponse struct {
	Counts         store.Counts                 `json:"counts"`
	Sessions       []store.Session              `json:"sessions"`
	MapSessions    []store.MapSession           `json:"map_sessions"`
	History        store.PlaybackHistoryPage    `json:"history"`
	RefreshSeconds int                          `json:"refresh_seconds"`
	GeneratedAt    time.Time                    `json:"generated_at"`
	Health         map[string]sectionHealth     `json:"health"`
}

func New(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	r.Route("/api", func(r chi.Router) {
		r.Use(requireStore(d))
		r.Use(requireAdmin)
		r.Get("/status", hStatus(d))
		r.Get("/sessions", hSessions(d))
		r.Get("/history", hHistory(d))
		r.Get("/counts", hCounts(d))
		r.Get("/map", hMap(d))
		r.Get("/overview", hOverview(d))
		r.Get("/config", hGetConfig(d))
		r.Patch("/config", hUpdateConfig(d))
	})

	if d.WebFS != nil {
		if assets, ok := safeSub(d.WebFS, "assets"); ok {
			r.Handle("/assets/*", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
		}
		r.Get("/dashboard", hSPA(d))
		r.Get("/dashboard/*", hSPA(d))
		r.Get("/admin", hSPA(d))
		r.Get("/admin/*", hSPA(d))
		r.Get("/", hSPA(d))
	}
	return r
}

func hGetConfig(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, err := d.Store.GetAppConfig(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "config_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, cfg)
	}
}

func requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := strings.TrimSpace(r.Header.Get("X-Continuum-User-Role"))
		if role == "" {
			role = strings.TrimSpace(r.Header.Get("X-Continuum-Role"))
		}
		if strings.EqualFold(role, "admin") {
			next.ServeHTTP(w, r)
			return
		}
		writeErr(w, http.StatusForbidden, "forbidden", errForbidden.Error())
	})
}

func hUpdateConfig(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var cfg pluginrt.Config
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_json", "invalid JSON body")
			return
		}
		if err := d.Store.UpdateAppConfig(r.Context(), cfg); err != nil {
			writeErr(w, http.StatusBadRequest, "config_failed", err.Error())
			return
		}
		cfg, err := d.Store.GetAppConfig(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "config_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, cfg)
	}
}

func requireStore(d Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if d.Store == nil {
				writeErr(w, http.StatusServiceUnavailable, "not_configured", "stream dashboard plugin is not configured")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func hStatus(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, err := d.Store.Status(r.Context())
		if err != nil {
			writeErr(w, http.StatusBadGateway, "status_failed", err.Error())
			return
		}
		status["refresh_seconds"] = refreshSeconds(d.RefreshSeconds)
		writeJSON(w, http.StatusOK, status)
	}
}

func hSessions(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessions, err := d.Store.Sessions(r.Context())
		if err != nil {
			writeErr(w, http.StatusBadGateway, "sessions_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions, "count": len(sessions), "refresh_seconds": refreshSeconds(d.RefreshSeconds)})
	}
}

func hCounts(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		counts, err := d.Store.Counts(r.Context())
		if err != nil {
			writeErr(w, http.StatusBadGateway, "counts_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"counts": counts, "refresh_seconds": refreshSeconds(d.RefreshSeconds)})
	}
}

func hHistory(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page := boundedIntQuery(r, "page", 1, 1, 10000)
		limit := boundedIntQuery(r, "limit", 50, 1, 500)
		history, err := d.Store.PlaybackHistoryReadOnly(r.Context(), limit, (page-1)*limit)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "history_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, history)
	}
}

func hMap(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessions, err := d.Store.MapSessions(r.Context())
		if err != nil {
			writeErr(w, http.StatusBadGateway, "map_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions, "count": len(sessions), "refresh_seconds": refreshSeconds(d.RefreshSeconds)})
	}
}

func hOverview(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := overviewResponse{
			Counts:         zeroCounts(),
			Sessions:       []store.Session{},
			MapSessions:    []store.MapSession{},
			History:        zeroHistoryPage(20),
			RefreshSeconds: refreshSeconds(d.RefreshSeconds),
			GeneratedAt:    time.Now().UTC(),
			Health: map[string]sectionHealth{
				"counts":   {OK: true},
				"sessions": {OK: true},
				"map":      {OK: true},
				"history":  {OK: true},
			},
		}

		counts, err := d.Store.Counts(r.Context())
		if err != nil {
			resp.Health["counts"] = sectionHealth{OK: false, Code: "counts_failed", Message: err.Error()}
		} else {
			resp.Counts = counts
		}

		sessions, err := d.Store.Sessions(r.Context())
		if err != nil {
			resp.Health["sessions"] = sectionHealth{OK: false, Code: "sessions_failed", Message: err.Error()}
		} else {
			resp.Sessions = sessions
		}

		mapSessions, err := d.Store.MapSessions(r.Context())
		if err != nil {
			resp.Health["map"] = sectionHealth{OK: false, Code: "map_failed", Message: err.Error()}
		} else {
			resp.MapSessions = mapSessions
		}

		history, err := d.Store.PlaybackHistoryReadOnly(r.Context(), 20, 0)
		if err != nil {
			resp.Health["history"] = sectionHealth{OK: false, Code: "history_failed", Message: err.Error()}
		} else {
			resp.History = history
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

func zeroCounts() store.Counts {
	return store.Counts{
		Servers: store.ServerSummary{ByType: map[string]int{}},
		Sessions: store.SessionCounts{},
		History: store.HistoryCounts{},
		Users: store.UserCounts{},
		Media: map[string]int{},
	}
}

func zeroHistoryPage(limit int) store.PlaybackHistoryPage {
	return store.PlaybackHistoryPage{
		Items:      []store.PlaybackHistoryItem{},
		Total:      0,
		Limit:      limit,
		Offset:     0,
		SyncedRows: 0,
		PrunedRows: 0,
	}
}

func hSPA(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := loadIndex(d.WebFS)
		if err != nil {
			http.Error(w, "spa not available", http.StatusServiceUnavailable)
			return
		}
		body = rewritePluginAssets(body, r.URL.Path)
		body = injectTheme(body, r)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	}
}

func injectTheme(body []byte, r *http.Request) []byte {
	theme := r.Header.Get("X-Continuum-Theme")
	if theme == "" {
		theme = r.URL.Query().Get("theme")
	}
	if theme == "" {
		return body
	}
	safe := html.EscapeString(theme)
	htmlBody := string(body)
	if strings.Contains(htmlBody, "<html ") {
		return []byte(strings.Replace(htmlBody, "<html ", `<html data-theme="`+safe+`" `, 1))
	}
	return []byte(strings.Replace(htmlBody, "<html>", `<html data-theme="`+safe+`">`, 1))
}

func loadIndex(webFS fs.FS) ([]byte, error) {
	f, err := webFS.Open("index.html")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func rewritePluginAssets(body []byte, requestPath string) []byte {
	html := string(body)
	prefix := adminAssetPrefix(requestPath)
	html = strings.ReplaceAll(html, `src="/assets/`, `src="`+prefix)
	html = strings.ReplaceAll(html, `href="/assets/`, `href="`+prefix)
	html = strings.ReplaceAll(html, `src="./assets/`, `src="`+prefix)
	html = strings.ReplaceAll(html, `href="./assets/`, `href="`+prefix)
	return []byte(html)
}

func adminAssetPrefix(requestPath string) string {
	if requestPath == "/admin" || requestPath == "/dashboard" || requestPath == "/" {
		return "assets/"
	}
	return "../assets/"
}

func pluginBaseHref(path string) string {
	const marker = "/api/v1/plugins/"
	i := strings.Index(path, marker)
	if i < 0 {
		return "/"
	}
	rest := path[i+len(marker):]
	j := strings.IndexByte(rest, '/')
	if j < 0 {
		return path + "/"
	}
	return path[:i+len(marker)+j] + "/"
}

func safeSub(webFS fs.FS, dir string) (fs.FS, bool) {
	if stat, err := fs.Stat(webFS, dir); err != nil || !stat.IsDir() {
		return nil, false
	}
	sub, err := fs.Sub(webFS, dir)
	if err != nil {
		return nil, false
	}
	return sub, true
}

func refreshSeconds(v int) int {
	if v < 5 {
		return 30
	}
	return v
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func intQuery(r *http.Request, key string, fallback int) int {
	return boundedIntQuery(r, key, fallback, 1, 0)
}

func boundedIntQuery(r *http.Request, key string, fallback, min, max int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n < min {
				return min
			}
			if max > 0 && n > max {
				return max
			}
			return n
		}
	}
	return fallback
}

func retentionPolicy(r *http.Request, base store.RetentionPolicy) store.RetentionPolicy {
	policy := base
	if v := intQuery(r, "history_days", 0); v > 0 {
		policy.Days = v
	}
	if v := intQuery(r, "history_rows", 0); v > 0 {
		policy.MaxRows = v
	}
	if v := intQuery(r, "history_min_seconds", 0); v > 0 {
		policy.MinWatchSeconds = v
	}
	if r.URL.Query().Get("history_completed_only") == "true" {
		policy.CompletedOnly = true
	}
	return policy
}

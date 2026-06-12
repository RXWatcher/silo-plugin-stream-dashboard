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

	pluginrt "github.com/RXWatcher/silo-plugin-stream-dashboard/internal/runtime"
	"github.com/RXWatcher/silo-plugin-stream-dashboard/internal/store"
)

type Deps struct {
	Store          serverStore
	Logger         hclog.Logger
	WebFS          fs.FS
	RefreshSeconds int
}

var errForbidden = errors.New("admin access required")

type serverStore interface {
	GetAppConfig(ctx context.Context) (pluginrt.Config, error)
	UpdateAppConfig(ctx context.Context, cfg pluginrt.Config) error
	Status(ctx context.Context) (map[string]any, error)
	Sessions(ctx context.Context) ([]store.Session, error)
	Counts(ctx context.Context) (store.Counts, error)
	MapSessions(ctx context.Context) ([]store.MapSession, error)
	PlaybackHistoryReadOnly(ctx context.Context, limit, offset int) (store.PlaybackHistoryPage, error)
}

type sectionHealth struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type overviewResponse struct {
	Counts         store.Counts              `json:"counts"`
	Sessions       []store.Session           `json:"sessions"`
	MapSessions    []store.MapSession        `json:"map_sessions"`
	History        store.PlaybackHistoryPage `json:"history"`
	RefreshSeconds int                       `json:"refresh_seconds"`
	GeneratedAt    time.Time                 `json:"generated_at"`
	Health         map[string]sectionHealth  `json:"health"`
}

func New(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	// The expensive read endpoints fan out across the database and may trigger
	// outbound geoip lookups, so they get a per-process minimum refresh interval
	// floor to bound amplification from rapid-refresh clients.
	limiter := newRateLimiter(minInterval)

	r.Route("/api", func(r chi.Router) {
		r.Use(requireStore(d))
		r.Use(requireAdmin)
		r.Get("/status", hStatus(d))
		r.Get("/sessions", hSessions(d))
		r.Get("/history", hHistory(d))
		r.Get("/counts", hCounts(d))
		r.Get("/map", limiter.throttle("map", hMap(d)))
		r.Get("/overview", limiter.throttle("overview", hOverview(d)))
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

// logErr records the underlying error server-side so the detail is never echoed
// to clients. Raw store/pgx errors can leak SQL text and schema details, so
// handlers surface a generic message and rely on this for diagnostics.
func logErr(d Deps, code string, err error) {
	if d.Logger != nil {
		d.Logger.Error("request failed", "code", code, "error", err)
	}
}

func hGetConfig(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, err := d.Store.GetAppConfig(r.Context())
		if err != nil {
			logErr(d, "config_failed", err)
			writeErr(w, http.StatusInternalServerError, "config_failed", "failed to load configuration")
			return
		}
		writeJSON(w, http.StatusOK, cfg)
	}
}

func requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The host guarantees it strips any client-supplied X-Silo-User-Role and
		// stamps the authenticated caller's role on this single header. We trust
		// exactly that header and nothing else, so a spoofed alias cannot grant
		// admin access. Do not add fallback headers here without an equivalent
		// host-side strip/stamp guarantee.
		role := strings.TrimSpace(r.Header.Get("X-Silo-User-Role"))
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
			var invalid pluginrt.ConfigError
			if errors.As(err, &invalid) {
				writeErr(w, http.StatusBadRequest, "config_invalid", invalid.Error())
				return
			}
			logErr(d, "config_failed", err)
			writeErr(w, http.StatusInternalServerError, "config_failed", "failed to save configuration")
			return
		}
		cfg, err := d.Store.GetAppConfig(r.Context())
		if err != nil {
			logErr(d, "config_failed", err)
			writeErr(w, http.StatusInternalServerError, "config_failed", "failed to load configuration")
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
			logErr(d, "status_failed", err)
			writeErr(w, http.StatusBadGateway, "status_failed", "failed to load status")
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
			logErr(d, "sessions_failed", err)
			writeErr(w, http.StatusBadGateway, "sessions_failed", "failed to load sessions")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions, "count": len(sessions), "refresh_seconds": refreshSeconds(d.RefreshSeconds)})
	}
}

func hCounts(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		counts, err := d.Store.Counts(r.Context())
		if err != nil {
			logErr(d, "counts_failed", err)
			writeErr(w, http.StatusBadGateway, "counts_failed", "failed to load counts")
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
			logErr(d, "history_failed", err)
			writeErr(w, http.StatusBadGateway, "history_failed", "failed to load history")
			return
		}
		writeJSON(w, http.StatusOK, history)
	}
}

func hMap(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessions, err := d.Store.MapSessions(r.Context())
		if err != nil {
			logErr(d, "map_failed", err)
			writeErr(w, http.StatusBadGateway, "map_failed", "failed to load map sessions")
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
			logErr(d, "counts_failed", err)
			resp.Health["counts"] = sectionHealth{OK: false, Code: "counts_failed", Message: "failed to load counts"}
		} else {
			resp.Counts = counts
		}

		sessions, err := d.Store.Sessions(r.Context())
		if err != nil {
			logErr(d, "sessions_failed", err)
			resp.Health["sessions"] = sectionHealth{OK: false, Code: "sessions_failed", Message: "failed to load sessions"}
		} else {
			resp.Sessions = sessions
		}

		mapSessions, err := d.Store.MapSessions(r.Context())
		if err != nil {
			logErr(d, "map_failed", err)
			resp.Health["map"] = sectionHealth{OK: false, Code: "map_failed", Message: "failed to load map sessions"}
		} else {
			resp.MapSessions = mapSessions
		}

		history, err := d.Store.PlaybackHistoryReadOnly(r.Context(), 20, 0)
		if err != nil {
			logErr(d, "history_failed", err)
			resp.Health["history"] = sectionHealth{OK: false, Code: "history_failed", Message: "failed to load history"}
		} else {
			resp.History = history
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

func zeroCounts() store.Counts {
	return store.Counts{
		Servers:  store.ServerSummary{ByType: map[string]int{}},
		Sessions: store.SessionCounts{},
		History:  store.HistoryCounts{},
		Users:    store.UserCounts{},
		Media:    map[string]int{},
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
	theme := r.Header.Get("X-Silo-Theme")
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

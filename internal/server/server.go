package server

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/hashicorp/go-hclog"

	"github.com/ContinuumApp/continuum-plugin-stream-dashboard/internal/store"
)

type Deps struct {
	Store                  *store.Store
	Logger                 hclog.Logger
	WebFS                  fs.FS
	RefreshSeconds         int
	HistoryRetentionPolicy store.RetentionPolicy
}

func New(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	r.Route("/api", func(r chi.Router) {
		r.Get("/status", hStatus(d))
		r.Get("/sessions", hSessions(d))
		r.Get("/history", hHistory(d))
		r.Get("/counts", hCounts(d))
		r.Get("/map", hMap(d))
		r.Get("/overview", hOverview(d))
	})

	if d.WebFS != nil {
		r.Handle("/assets/*", http.StripPrefix("/assets/", http.FileServer(http.FS(mustSub(d.WebFS, "assets")))))
		r.Get("/dashboard", hSPA(d))
		r.Get("/dashboard/*", hSPA(d))
		r.Get("/", hSPA(d))
	}
	return r
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
		page := intQuery(r, "page", 1)
		limit := intQuery(r, "limit", 50)
		policy := retentionPolicy(r, d.HistoryRetentionPolicy)
		history, err := d.Store.PlaybackHistory(r.Context(), limit, (page-1)*limit, policy, true)
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
		counts, err := d.Store.Counts(r.Context())
		if err != nil {
			writeErr(w, http.StatusBadGateway, "counts_failed", err.Error())
			return
		}
		sessions, err := d.Store.Sessions(r.Context())
		if err != nil {
			writeErr(w, http.StatusBadGateway, "sessions_failed", err.Error())
			return
		}
		mapSessions, err := d.Store.MapSessions(r.Context())
		if err != nil {
			writeErr(w, http.StatusBadGateway, "map_failed", err.Error())
			return
		}
		history, err := d.Store.PlaybackHistory(r.Context(), 20, 0, retentionPolicy(r, d.HistoryRetentionPolicy), true)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "history_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"counts":          counts,
			"sessions":        sessions,
			"map_sessions":    mapSessions,
			"history":         history,
			"refresh_seconds": refreshSeconds(d.RefreshSeconds),
			"generated_at":    time.Now().UTC(),
		})
	}
}

func hSPA(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := loadIndex(d.WebFS)
		if err != nil {
			http.Error(w, "spa not available", http.StatusServiceUnavailable)
			return
		}
		baseHref := pluginBaseHref(r.URL.Path)
		body = []byte(strings.Replace(string(body), "<head>", `<head><base href="`+baseHref+`">`, 1))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	}
}

func loadIndex(webFS fs.FS) ([]byte, error) {
	f, err := webFS.Open("index.html")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
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

func mustSub(webFS fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(webFS, dir)
	if err != nil {
		panic(err)
	}
	return sub
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
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
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

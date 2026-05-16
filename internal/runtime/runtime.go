package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"

	pluginv1 "github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginproto/continuum/plugin/v1"
	"github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginsdk/runtimedefault"
)

type Config struct {
	DatabaseURL                   string
	SourceDatabaseURL             string
	RefreshSeconds                int
	DefaultServerLat              float64
	DefaultServerLon              float64
	GeoIPEnabled                  bool
	GeoIPDatabasePath             string
	GeoIPLookupMissingCoordinate  bool
	GeoIPOverrideCoordinates      bool
	GeoIPLookupCDN                bool
	GeoIPIncludePrivateIPs        bool
	GeoIPProviderOrder            []string
	GeoIPRequestTimeoutSeconds    int
	GeoIPCacheTTLSeconds          int
	GeoIPCacheMaxEntries          int
	GeoIPIPAPIEnabled             bool
	GeoIPIPAPIBaseURL             string
	GeoIPIPInfoEnabled            bool
	GeoIPIPInfoBaseURL            string
	GeoIPIPInfoToken              string
	GeoIPIPWhoisEnabled           bool
	GeoIPIPWhoisBaseURL           string
	HistoryRetentionDays          int
	HistoryRetentionMaxRows       int
	HistoryRetentionMinSeconds    int
	HistoryRetentionCompletedOnly bool
}

type Server struct {
	runtimedefault.Server
	manifest *pluginv1.PluginManifest
	onConfig func(Config) error

	mu  sync.RWMutex
	cfg Config
}

func New(manifest *pluginv1.PluginManifest, onConfig func(Config) error) *Server {
	return &Server{manifest: manifest, onConfig: onConfig}
}

func (s *Server) GetManifest(context.Context, *pluginv1.GetManifestRequest) (*pluginv1.GetManifestResponse, error) {
	return &pluginv1.GetManifestResponse{Manifest: s.manifest}, nil
}

func (s *Server) Configure(_ context.Context, req *pluginv1.ConfigureRequest) (*pluginv1.ConfigureResponse, error) {
	cfg := Config{
		RefreshSeconds:               30,
		DefaultServerLat:             37.5485,
		DefaultServerLon:             -121.9886,
		GeoIPLookupMissingCoordinate: true,
		GeoIPLookupCDN:               true,
		GeoIPProviderOrder:           []string{"mmdb", "ipapi", "ipinfo", "ipwhois"},
		GeoIPRequestTimeoutSeconds:   3,
		GeoIPCacheTTLSeconds:         3600,
		GeoIPCacheMaxEntries:         4096,
		HistoryRetentionDays:         365,
	}
	for _, e := range req.GetConfig() {
		if e.GetValue() == nil {
			continue
		}
		m := e.GetValue().AsMap()
		switch e.GetKey() {
		case "plugin_database":
			cfg.DatabaseURL = stringValue(m["database_url"], m["value"], firstString(m))
		case "continuum_database":
			cfg.SourceDatabaseURL = stringValue(m["source_database_url"])
			if cfg.SourceDatabaseURL == "" {
				cfg.SourceDatabaseURL = stringValue(m["database_url"], m["value"], firstString(m))
			}
		case "stream_dashboard":
			cfg.RefreshSeconds = intValue(m["refresh_seconds"], cfg.RefreshSeconds)
			cfg.DefaultServerLat = floatValue(m["default_server_lat"], cfg.DefaultServerLat)
			cfg.DefaultServerLon = floatValue(m["default_server_lon"], cfg.DefaultServerLon)
			cfg.GeoIPEnabled = boolValue(m["geoip_enabled"], cfg.GeoIPEnabled)
			cfg.GeoIPDatabasePath = stringValue(m["geoip_database_path"])
			cfg.GeoIPLookupMissingCoordinate = boolValue(m["geoip_lookup_missing_coordinates"], cfg.GeoIPLookupMissingCoordinate)
			cfg.GeoIPOverrideCoordinates = boolValue(m["geoip_override_session_coordinates"], cfg.GeoIPOverrideCoordinates)
			cfg.GeoIPLookupCDN = boolValue(m["geoip_lookup_cdn_nodes"], cfg.GeoIPLookupCDN)
			cfg.GeoIPIncludePrivateIPs = boolValue(m["geoip_include_private_ips"], cfg.GeoIPIncludePrivateIPs)
			cfg.GeoIPProviderOrder = stringListValue(m["geoip_provider_order"], cfg.GeoIPProviderOrder)
			cfg.GeoIPRequestTimeoutSeconds = intValue(m["geoip_request_timeout_seconds"], cfg.GeoIPRequestTimeoutSeconds)
			cfg.GeoIPCacheTTLSeconds = intValue(m["geoip_cache_ttl_seconds"], cfg.GeoIPCacheTTLSeconds)
			cfg.GeoIPCacheMaxEntries = intValue(m["geoip_cache_max_entries"], cfg.GeoIPCacheMaxEntries)
			cfg.GeoIPIPAPIEnabled = boolValue(m["geoip_ipapi_enabled"], cfg.GeoIPIPAPIEnabled)
			cfg.GeoIPIPAPIBaseURL = stringValue(m["geoip_ipapi_base_url"])
			cfg.GeoIPIPInfoEnabled = boolValue(m["geoip_ipinfo_enabled"], cfg.GeoIPIPInfoEnabled)
			cfg.GeoIPIPInfoBaseURL = stringValue(m["geoip_ipinfo_base_url"])
			cfg.GeoIPIPInfoToken = stringValue(m["geoip_ipinfo_token"])
			cfg.GeoIPIPWhoisEnabled = boolValue(m["geoip_ipwhois_enabled"], cfg.GeoIPIPWhoisEnabled)
			cfg.GeoIPIPWhoisBaseURL = stringValue(m["geoip_ipwhois_base_url"])
			cfg.HistoryRetentionDays = intValue(m["history_retention_days"], cfg.HistoryRetentionDays)
			cfg.HistoryRetentionMaxRows = intValue(m["history_retention_max_rows"], cfg.HistoryRetentionMaxRows)
			cfg.HistoryRetentionMinSeconds = intValue(m["history_retention_min_seconds"], cfg.HistoryRetentionMinSeconds)
			cfg.HistoryRetentionCompletedOnly = boolValue(m["history_retention_completed_only"], cfg.HistoryRetentionCompletedOnly)
		}
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("plugin_database.database_url is required")
	}
	if cfg.SourceDatabaseURL == "" {
		cfg.SourceDatabaseURL = cfg.DatabaseURL
	}
	if cfg.RefreshSeconds < 5 {
		cfg.RefreshSeconds = 5
	}
	if cfg.GeoIPRequestTimeoutSeconds < 1 {
		cfg.GeoIPRequestTimeoutSeconds = 1
	}
	if cfg.GeoIPCacheTTLSeconds < 60 {
		cfg.GeoIPCacheTTLSeconds = 60
	}
	if cfg.GeoIPCacheMaxEntries < 100 {
		cfg.GeoIPCacheMaxEntries = 100
	}
	if cfg.HistoryRetentionDays < 0 {
		cfg.HistoryRetentionDays = 0
	}
	if cfg.HistoryRetentionMaxRows < 0 {
		cfg.HistoryRetentionMaxRows = 0
	}
	if cfg.HistoryRetentionMinSeconds < 0 {
		cfg.HistoryRetentionMinSeconds = 0
	}
	if s.onConfig != nil {
		if err := s.onConfig(cfg); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	return &pluginv1.ConfigureResponse{}, nil
}

func stringValue(candidates ...any) string {
	for _, c := range candidates {
		if s, ok := c.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func intValue(v any, fallback int) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return fallback
	}
}

func floatValue(v any, fallback float64) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	default:
		return fallback
	}
}

func boolValue(v any, fallback bool) bool {
	switch b := v.(type) {
	case bool:
		return b
	default:
		return fallback
	}
}

func stringListValue(v any, fallback []string) []string {
	switch value := v.(type) {
	case string:
		parts := strings.Split(value, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return fallback
}

func firstString(m map[string]any) any {
	for _, v := range m {
		if _, ok := v.(string); ok {
			return v
		}
	}
	return nil
}

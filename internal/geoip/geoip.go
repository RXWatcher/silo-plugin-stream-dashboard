package geoip

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/geoip2-golang"
)

const (
	defaultIPAPIBaseURL  = "http://ip-api.com/json"
	defaultIPInfoBaseURL = "https://api.ipinfo.io/lookup"
	defaultIPWhoisURL    = "https://ipwho.is"
)

type Locator struct {
	providers       []provider
	includePrivate  bool
	timeout         time.Duration
	cacheTTL        time.Duration
	maxCacheEntries int

	mu    sync.RWMutex
	cache map[string]cacheEntry
}

type Config struct {
	DatabasePath    string
	IncludePrivate  bool
	Timeout         time.Duration
	CacheTTL        time.Duration
	MaxCacheEntries int
	ProviderOrder   []string
	IPAPIEnabled    bool
	IPAPIBaseURL    string
	IPInfoEnabled   bool
	IPInfoBaseURL   string
	IPInfoToken     string
	IPWhoisEnabled  bool
	IPWhoisBaseURL  string
}

type Location struct {
	Lat      float64
	Lon      float64
	Location string
	Source   string
}

type provider interface {
	Name() string
	Lookup(context.Context, string) (Location, bool, error)
	Close()
}

type cacheEntry struct {
	location Location
	found    bool
	expires  time.Time
}

func Open(cfg Config) (*Locator, error) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	cacheTTL := cfg.CacheTTL
	if cacheTTL <= 0 {
		cacheTTL = time.Hour
	}
	maxCacheEntries := cfg.MaxCacheEntries
	if maxCacheEntries <= 0 {
		maxCacheEntries = 4096
	}

	available := map[string]provider{}
	if strings.TrimSpace(cfg.DatabasePath) != "" {
		p, err := openMMDBProvider(cfg.DatabasePath)
		if err != nil {
			return nil, fmt.Errorf("open local mmdb: %w", err)
		}
		available[p.Name()] = p
	}

	client := &http.Client{Timeout: timeout}
	if cfg.IPAPIEnabled {
		available["ipapi"] = ipAPIProvider{client: client, baseURL: defaultString(cfg.IPAPIBaseURL, defaultIPAPIBaseURL)}
	}
	if cfg.IPInfoEnabled {
		available["ipinfo"] = ipInfoProvider{client: client, baseURL: defaultString(cfg.IPInfoBaseURL, defaultIPInfoBaseURL), token: cfg.IPInfoToken}
	}
	if cfg.IPWhoisEnabled {
		available["ipwhois"] = ipWhoisProvider{client: client, baseURL: defaultString(cfg.IPWhoisBaseURL, defaultIPWhoisURL)}
	}

	order := cfg.ProviderOrder
	if len(order) == 0 {
		order = []string{"mmdb", "ipapi", "ipinfo", "ipwhois"}
	}
	providers := make([]provider, 0, len(available))
	used := map[string]bool{}
	for _, name := range order {
		name = normalizeProviderName(name)
		if p, ok := available[name]; ok && !used[name] {
			providers = append(providers, p)
			used[name] = true
		}
	}
	for name, p := range available {
		if !used[name] {
			providers = append(providers, p)
		}
	}
	if len(providers) == 0 {
		return nil, fmt.Errorf("geoip is enabled but no local or online provider is configured")
	}
	return &Locator{providers: providers, includePrivate: cfg.IncludePrivate, timeout: timeout, cacheTTL: cacheTTL, maxCacheEntries: maxCacheEntries, cache: map[string]cacheEntry{}}, nil
}

func (l *Locator) Close() {
	if l == nil {
		return
	}
	for _, p := range l.providers {
		p.Close()
	}
}

func (l *Locator) Lookup(ctx context.Context, ipText string) (float64, float64, string, string, bool) {
	if l == nil {
		return 0, 0, "", "", false
	}
	ipText = strings.TrimSpace(ipText)
	if ipText == "" || net.ParseIP(ipText) == nil {
		return 0, 0, "", "", false
	}
	if !l.includePrivate && isPrivateOrLocal(ipText) {
		return 0, 0, "", "", false
	}
	if entry, ok := l.cacheGet(ipText); ok {
		return entry.location.Lat, entry.location.Lon, entry.location.Location, entry.location.Source, entry.found
	}

	lookupCtx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()
	for _, p := range l.providers {
		location, ok, err := p.Lookup(lookupCtx, ipText)
		if err != nil || !ok {
			continue
		}
		location.Source = "geoip:" + p.Name()
		l.cacheSet(ipText, location, true)
		return location.Lat, location.Lon, location.Location, location.Source, true
	}
	l.cacheSet(ipText, Location{}, false)
	return 0, 0, "", "", false
}

func (l *Locator) cacheGet(ip string) (cacheEntry, bool) {
	l.mu.RLock()
	entry, ok := l.cache[ip]
	l.mu.RUnlock()
	if !ok || time.Now().After(entry.expires) {
		return cacheEntry{}, false
	}
	return entry, true
}

func (l *Locator) cacheSet(ip string, location Location, found bool) {
	l.mu.Lock()
	l.pruneCacheLocked()
	l.cache[ip] = cacheEntry{location: location, found: found, expires: time.Now().Add(l.cacheTTL)}
	l.mu.Unlock()
}

func (l *Locator) pruneCacheLocked() {
	if l.maxCacheEntries <= 0 || len(l.cache) < l.maxCacheEntries {
		return
	}
	now := time.Now()
	for ip, entry := range l.cache {
		if now.After(entry.expires) {
			delete(l.cache, ip)
		}
	}
	for len(l.cache) >= l.maxCacheEntries {
		var oldestIP string
		var oldestExpiry time.Time
		for ip, entry := range l.cache {
			if oldestIP == "" || entry.expires.Before(oldestExpiry) {
				oldestIP = ip
				oldestExpiry = entry.expires
			}
		}
		if oldestIP == "" {
			return
		}
		delete(l.cache, oldestIP)
	}
}

type mmdbProvider struct {
	db *geoip2.Reader
}

func openMMDBProvider(path string) (mmdbProvider, error) {
	db, err := geoip2.Open(path)
	return mmdbProvider{db: db}, err
}

func (p mmdbProvider) Name() string { return "mmdb" }

func (p mmdbProvider) Close() {
	if p.db != nil {
		_ = p.db.Close()
	}
}

func (p mmdbProvider) Lookup(_ context.Context, ipText string) (Location, bool, error) {
	record, err := p.db.City(net.ParseIP(ipText))
	if err != nil || record == nil || (record.Location.Latitude == 0 && record.Location.Longitude == 0) {
		return Location{}, false, err
	}
	return Location{Lat: record.Location.Latitude, Lon: record.Location.Longitude, Location: formatMMDBLocation(record)}, true, nil
}

type ipAPIProvider struct {
	client  *http.Client
	baseURL string
}

func (p ipAPIProvider) Name() string { return "ipapi" }
func (p ipAPIProvider) Close()       {}

func (p ipAPIProvider) Lookup(ctx context.Context, ipText string) (Location, bool, error) {
	endpoint := strings.TrimRight(p.baseURL, "/") + "/" + url.PathEscape(ipText)
	q := url.Values{}
	q.Set("fields", "status,message,country,regionName,city,lat,lon,query")
	var out struct {
		Status     string  `json:"status"`
		Message    string  `json:"message"`
		Country    string  `json:"country"`
		RegionName string  `json:"regionName"`
		City       string  `json:"city"`
		Lat        float64 `json:"lat"`
		Lon        float64 `json:"lon"`
	}
	if err := getJSON(ctx, p.client, endpoint+"?"+q.Encode(), nil, &out); err != nil {
		return Location{}, false, err
	}
	if out.Status != "success" || (out.Lat == 0 && out.Lon == 0) {
		return Location{}, false, fmt.Errorf("ip-api lookup failed: %s", out.Message)
	}
	return Location{Lat: out.Lat, Lon: out.Lon, Location: joinLocation(out.City, out.RegionName, out.Country)}, true, nil
}

type ipInfoProvider struct {
	client  *http.Client
	baseURL string
	token   string
}

func (p ipInfoProvider) Name() string { return "ipinfo" }
func (p ipInfoProvider) Close()       {}

func (p ipInfoProvider) Lookup(ctx context.Context, ipText string) (Location, bool, error) {
	endpoint := strings.TrimRight(p.baseURL, "/") + "/" + url.PathEscape(ipText)
	var headers http.Header
	if p.token != "" {
		// Pass the token via Authorization header instead of the query string so
		// it is not leaked through request logs, referrers, or proxies.
		headers = http.Header{"Authorization": []string{"Bearer " + p.token}}
	}
	var out struct {
		Geo struct {
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
			City      string  `json:"city"`
			Region    string  `json:"region"`
			Country   string  `json:"country_name"`
		} `json:"geo"`
		Loc     string `json:"loc"`
		City    string `json:"city"`
		Region  string `json:"region"`
		Country string `json:"country"`
	}
	if err := getJSON(ctx, p.client, endpoint, headers, &out); err != nil {
		return Location{}, false, err
	}
	lat, lon := out.Geo.Latitude, out.Geo.Longitude
	if lat == 0 && lon == 0 && out.Loc != "" {
		lat, lon = parseLoc(out.Loc)
	}
	if lat == 0 && lon == 0 {
		return Location{}, false, nil
	}
	return Location{Lat: lat, Lon: lon, Location: joinLocation(firstNonEmpty(out.Geo.City, out.City), firstNonEmpty(out.Geo.Region, out.Region), firstNonEmpty(out.Geo.Country, out.Country))}, true, nil
}

type ipWhoisProvider struct {
	client  *http.Client
	baseURL string
}

func (p ipWhoisProvider) Name() string { return "ipwhois" }
func (p ipWhoisProvider) Close()       {}

func (p ipWhoisProvider) Lookup(ctx context.Context, ipText string) (Location, bool, error) {
	endpoint := strings.TrimRight(p.baseURL, "/") + "/" + url.PathEscape(ipText)
	var out struct {
		Success   bool    `json:"success"`
		Message   string  `json:"message"`
		Country   string  `json:"country"`
		Region    string  `json:"region"`
		City      string  `json:"city"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	}
	if err := getJSON(ctx, p.client, endpoint, nil, &out); err != nil {
		return Location{}, false, err
	}
	if !out.Success || (out.Latitude == 0 && out.Longitude == 0) {
		return Location{}, false, fmt.Errorf("ipwhois lookup failed: %s", out.Message)
	}
	return Location{Lat: out.Latitude, Lon: out.Longitude, Location: joinLocation(out.City, out.Region, out.Country)}, true, nil
}

func getJSON(ctx context.Context, client *http.Client, endpoint string, headers http.Header, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("geoip provider returned HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ValidateBaseURL guards the configurable geoip provider base URLs against
// server-side request forgery. The plugin fetches these URLs server-side, so a
// caller able to set them could otherwise probe internal services. We require
// an https URL with a host that is not an IP literal inside loopback, private,
// link-local, or unspecified ranges. DNS names are allowed because resolving and
// pinning every possible record is out of scope here; the scheme + literal
// checks block the obvious internal-target cases.
func ValidateBaseURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("must use https scheme")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("missing host")
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
			addr.IsLinkLocalMulticast() || addr.IsUnspecified() {
			return fmt.Errorf("host %q targets a private or loopback address", host)
		}
	}
	return nil
}

func isPrivateOrLocal(ipText string) bool {
	addr, err := netip.ParseAddr(ipText)
	if err != nil {
		return false
	}
	return addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsUnspecified() || addr.IsMulticast()
}

func formatMMDBLocation(record *geoip2.City) string {
	parts := make([]string, 0, 3)
	if city := record.City.Names["en"]; city != "" {
		parts = append(parts, city)
	}
	if len(record.Subdivisions) > 0 {
		if subdivision := record.Subdivisions[0].Names["en"]; subdivision != "" {
			parts = append(parts, subdivision)
		}
	}
	if country := record.Country.Names["en"]; country != "" {
		parts = append(parts, country)
	}
	return strings.Join(parts, ", ")
}

func joinLocation(parts ...string) string {
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		key := strings.ToLower(part)
		if part != "" && !seen[key] {
			out = append(out, part)
			seen[key] = true
		}
	}
	return strings.Join(out, ", ")
}

func parseLoc(loc string) (float64, float64) {
	parts := strings.Split(loc, ",")
	if len(parts) != 2 {
		return 0, 0
	}
	var lat, lon float64
	if _, err := fmt.Sscanf(strings.TrimSpace(parts[0])+","+strings.TrimSpace(parts[1]), "%f,%f", &lat, &lon); err != nil {
		return 0, 0
	}
	return lat, lon
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func normalizeProviderName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "-", "")
	name = strings.ReplaceAll(name, "_", "")
	switch name {
	case "local", "maxmind", "mmdb":
		return "mmdb"
	case "ipapi", "ipapicom":
		return "ipapi"
	case "ipinfo", "ipinfocom":
		return "ipinfo"
	case "ipwhois", "ipwhoisio":
		return "ipwhois"
	default:
		return name
	}
}

package geoip

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOnlineProviders(t *testing.T) {
	tests := []struct {
		name        string
		config      func(string) Config
		response    string
		wantSource  string
		wantPlace   string
		wantLat     float64
		wantLon     float64
		wantPathBit string
	}{
		{
			name: "ipapi",
			config: func(baseURL string) Config {
				return Config{IPAPIEnabled: true, IPAPIBaseURL: baseURL, ProviderOrder: []string{"ipapi"}, Timeout: time.Second}
			},
			response:    `{"status":"success","country":"United States","regionName":"California","city":"Mountain View","lat":37.386,"lon":-122.0838}`,
			wantSource:  "geoip:ipapi",
			wantPlace:   "Mountain View, California, United States",
			wantLat:     37.386,
			wantLon:     -122.0838,
			wantPathBit: "/8.8.8.8",
		},
		{
			name: "ipinfo",
			config: func(baseURL string) Config {
				return Config{IPInfoEnabled: true, IPInfoBaseURL: baseURL, IPInfoToken: "token-1", ProviderOrder: []string{"ipinfo"}, Timeout: time.Second}
			},
			response:    `{"geo":{"latitude":37.386,"longitude":-122.0838,"city":"Mountain View","region":"California","country_name":"United States"}}`,
			wantSource:  "geoip:ipinfo",
			wantPlace:   "Mountain View, California, United States",
			wantLat:     37.386,
			wantLon:     -122.0838,
			wantPathBit: "/8.8.8.8",
		},
		{
			name: "ipwhois",
			config: func(baseURL string) Config {
				return Config{IPWhoisEnabled: true, IPWhoisBaseURL: baseURL, ProviderOrder: []string{"ipwhois"}, Timeout: time.Second}
			},
			response:    `{"success":true,"country":"United States","region":"California","city":"Mountain View","latitude":37.386,"longitude":-122.0838}`,
			wantSource:  "geoip:ipwhois",
			wantPlace:   "Mountain View, California, United States",
			wantLat:     37.386,
			wantLon:     -122.0838,
			wantPathBit: "/8.8.8.8",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var path string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.String()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.response))
			}))
			defer server.Close()

			locator, err := Open(tt.config(server.URL))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer locator.Close()

			lat, lon, place, source, ok := locator.Lookup(context.Background(), "8.8.8.8")
			if !ok {
				t.Fatal("expected lookup result")
			}
			if lat != tt.wantLat || lon != tt.wantLon {
				t.Fatalf("coords = %v,%v, want %v,%v", lat, lon, tt.wantLat, tt.wantLon)
			}
			if place != tt.wantPlace {
				t.Fatalf("place = %q, want %q", place, tt.wantPlace)
			}
			if source != tt.wantSource {
				t.Fatalf("source = %q, want %q", source, tt.wantSource)
			}
			if !strings.Contains(path, tt.wantPathBit) {
				t.Fatalf("path = %q, want to contain %q", path, tt.wantPathBit)
			}
		})
	}
}

func TestIPInfoTokenSentAsAuthorizationHeader(t *testing.T) {
	var gotAuth, gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"geo":{"latitude":1,"longitude":2,"city":"X"}}`))
	}))
	defer server.Close()

	locator, err := Open(Config{IPInfoEnabled: true, IPInfoBaseURL: server.URL, IPInfoToken: "secret-token", ProviderOrder: []string{"ipinfo"}, Timeout: time.Second})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer locator.Close()

	if _, _, _, _, ok := locator.Lookup(context.Background(), "8.8.8.8"); !ok {
		t.Fatal("expected lookup result")
	}
	if gotAuth != "Bearer secret-token" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer secret-token")
	}
	if strings.Contains(gotQuery, "secret-token") {
		t.Fatalf("token leaked into query string: %q", gotQuery)
	}
}

func TestValidateBaseURL(t *testing.T) {
	valid := []string{
		"",
		"https://api.ipinfo.io/lookup",
		"https://ipwho.is",
		"https://203.0.113.10/json",
	}
	for _, v := range valid {
		if err := ValidateBaseURL(v); err != nil {
			t.Fatalf("ValidateBaseURL(%q) = %v, want nil", v, err)
		}
	}

	invalid := []string{
		"http://ip-api.com/json",       // not https
		"https://127.0.0.1/json",       // loopback
		"https://10.0.0.5/json",        // private
		"https://192.168.1.1/json",     // private
		"https://169.254.169.254/meta", // link-local
		"https://0.0.0.0/json",         // unspecified
		"://not-a-url",                 // unparseable
	}
	for _, v := range invalid {
		if err := ValidateBaseURL(v); err == nil {
			t.Fatalf("ValidateBaseURL(%q) = nil, want error", v)
		}
	}
}

func TestPrivateIPSkippedByDefault(t *testing.T) {
	locator, err := Open(Config{IPWhoisEnabled: true, ProviderOrder: []string{"ipwhois"}, Timeout: time.Second})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer locator.Close()

	if _, _, _, _, ok := locator.Lookup(context.Background(), "192.168.1.10"); ok {
		t.Fatal("expected private IP lookup to be skipped")
	}
}

func TestCacheHonorsMaxEntries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"country":"United States","region":"California","city":"Mountain View","latitude":37.386,"longitude":-122.0838}`))
	}))
	defer server.Close()

	locator, err := Open(Config{
		IPWhoisEnabled:  true,
		IPWhoisBaseURL:  server.URL,
		ProviderOrder:   []string{"ipwhois"},
		Timeout:         time.Second,
		MaxCacheEntries: 2,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer locator.Close()

	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "9.9.9.9", "4.4.4.4"} {
		if _, _, _, _, ok := locator.Lookup(context.Background(), ip); !ok {
			t.Fatalf("expected lookup for %s", ip)
		}
	}

	locator.mu.RLock()
	size := len(locator.cache)
	locator.mu.RUnlock()
	if size > 2 {
		t.Fatalf("cache size = %d, want <= 2", size)
	}
}

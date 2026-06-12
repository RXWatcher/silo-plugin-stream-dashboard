package runtime

import (
	"errors"
	"testing"
)

func TestValidateAppConfigRejectsSSRFBaseURLs(t *testing.T) {
	cases := map[string]Config{
		"ipapi loopback":  {GeoIPIPAPIBaseURL: "https://127.0.0.1/json"},
		"ipinfo private":  {GeoIPIPInfoBaseURL: "https://10.1.2.3/lookup"},
		"ipwhois http":    {GeoIPIPWhoisBaseURL: "http://ipwho.is"},
		"link-local meta": {GeoIPIPAPIBaseURL: "https://169.254.169.254/latest"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateAppConfig(cfg)
			if err == nil {
				t.Fatal("expected validation error")
			}
			var ce ConfigError
			if !errors.As(err, &ce) {
				t.Fatalf("error = %T, want ConfigError", err)
			}
		})
	}
}

func TestValidateAppConfigAllowsEmptyAndPublicURLs(t *testing.T) {
	cfg := Config{
		GeoIPIPAPIBaseURL:   "",
		GeoIPIPInfoBaseURL:  "https://api.ipinfo.io/lookup",
		GeoIPIPWhoisBaseURL: "https://ipwho.is",
	}
	if err := ValidateAppConfig(cfg); err != nil {
		t.Fatalf("ValidateAppConfig = %v, want nil", err)
	}
}

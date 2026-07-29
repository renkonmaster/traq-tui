package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validEnvironment() map[string]string {
	return map[string]string{
		"TRAQ_API_BASE_URL":  "https://q.example.test/api/v3",
		"TRAQ_CLIENT_ID":     "client-id",
		"TRAQ_CLIENT_SECRET": "client-secret",
		"TRAQ_REDIRECT_URL":  "http://127.0.0.1:18080/callback",
	}
}

func loadWithEnvironment(t *testing.T, values map[string]string, workingDir string) (Config, error) {
	t.Helper()
	return Load(func(key string) string {
		return values[key]
	}, workingDir)
}

func TestLoadValidConfiguration(t *testing.T) {
	workingDir := filepath.Join(string(filepath.Separator), "work", "traq-tui")

	got, err := loadWithEnvironment(t, validEnvironment(), workingDir)
	if err != nil {
		t.Fatal(err)
	}

	if got.APIBaseURL.String() != "https://q.example.test/api/v3" {
		t.Fatalf("API base URL = %q", got.APIBaseURL)
	}
	if got.ClientID != "client-id" {
		t.Fatalf("client ID = %q", got.ClientID)
	}
	if got.ClientSecret != "client-secret" {
		t.Fatalf("client secret was not loaded")
	}
	if got.RedirectURL.String() != "http://127.0.0.1:18080/callback" {
		t.Fatalf("redirect URL = %q", got.RedirectURL)
	}
	wantTokenFile := filepath.Join(workingDir, ".traq-tui", "token.json")
	if got.TokenFile != wantTokenFile {
		t.Fatalf("token file = %q, want %q", got.TokenFile, wantTokenFile)
	}
	if got.PollInterval != 5*time.Second {
		t.Fatalf("poll interval = %s", got.PollInterval)
	}
}

func TestLoadReportsEachMissingRequiredVariable(t *testing.T) {
	for _, missing := range []string{
		"TRAQ_API_BASE_URL",
		"TRAQ_CLIENT_ID",
		"TRAQ_CLIENT_SECRET",
		"TRAQ_REDIRECT_URL",
	} {
		t.Run(missing, func(t *testing.T) {
			values := validEnvironment()
			delete(values, missing)

			_, err := loadWithEnvironment(t, values, "/work")
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), missing) {
				t.Fatalf("error %q does not name %s", err, missing)
			}
		})
	}
}

func TestLoadRejectsInvalidAPIBaseURL(t *testing.T) {
	tests := map[string]string{
		"http":         "http://q.example.test/api/v3",
		"wrong path":   "https://q.example.test/api",
		"query":        "https://q.example.test/api/v3?unexpected=true",
		"fragment":     "https://q.example.test/api/v3#fragment",
		"missing host": "https:///api/v3",
	}

	for name, rawURL := range tests {
		t.Run(name, func(t *testing.T) {
			values := validEnvironment()
			values["TRAQ_API_BASE_URL"] = rawURL

			if _, err := loadWithEnvironment(t, values, "/work"); err == nil {
				t.Fatalf("accepted invalid API base URL %q", rawURL)
			}
		})
	}
}

func TestLoadRejectsInvalidRedirectURL(t *testing.T) {
	tests := map[string]string{
		"https":            "https://127.0.0.1:18080/callback",
		"non-loopback":     "http://example.test:18080/callback",
		"missing port":     "http://127.0.0.1/callback",
		"missing path":     "http://127.0.0.1:18080",
		"query":            "http://127.0.0.1:18080/callback?unexpected=true",
		"userinfo":         "http://user@127.0.0.1:18080/callback",
		"unspecified host": "http://0.0.0.0:18080/callback",
	}

	for name, rawURL := range tests {
		t.Run(name, func(t *testing.T) {
			values := validEnvironment()
			values["TRAQ_REDIRECT_URL"] = rawURL

			if _, err := loadWithEnvironment(t, values, "/work"); err == nil {
				t.Fatalf("accepted invalid redirect URL %q", rawURL)
			}
		})
	}
}

func TestLoadAcceptsLoopbackRedirectHosts(t *testing.T) {
	for _, rawURL := range []string{
		"http://localhost:18080/callback",
		"http://127.0.0.1:18080/callback",
		"http://[::1]:18080/callback",
	} {
		t.Run(rawURL, func(t *testing.T) {
			values := validEnvironment()
			values["TRAQ_REDIRECT_URL"] = rawURL

			if _, err := loadWithEnvironment(t, values, "/work"); err != nil {
				t.Fatalf("rejected loopback redirect %q: %v", rawURL, err)
			}
		})
	}
}

func TestLoadRejectsInvalidPollInterval(t *testing.T) {
	for _, value := range []string{"not-a-duration", "0s", "-1s"} {
		t.Run(value, func(t *testing.T) {
			values := validEnvironment()
			values["TRAQ_TUI_POLL_INTERVAL"] = value

			if _, err := loadWithEnvironment(t, values, "/work"); err == nil {
				t.Fatalf("accepted poll interval %q", value)
			}
		})
	}
}

func TestLoadResolvesCustomTokenFile(t *testing.T) {
	values := validEnvironment()
	values["TRAQ_TUI_TOKEN_FILE"] = filepath.Join("state", "oauth.json")
	values["TRAQ_TUI_POLL_INTERVAL"] = "9s"

	got, err := loadWithEnvironment(t, values, "/work")
	if err != nil {
		t.Fatal(err)
	}

	if got.TokenFile != filepath.Join("/work", "state", "oauth.json") {
		t.Fatalf("token file = %q", got.TokenFile)
	}
	if got.PollInterval != 9*time.Second {
		t.Fatalf("poll interval = %s", got.PollInterval)
	}
}

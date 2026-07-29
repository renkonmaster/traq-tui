// Package config loads and validates traq-tui's environment-backed settings.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

const defaultPollInterval = 5 * time.Second

// Config contains all runtime configuration needed by traq-tui.
type Config struct {
	APIBaseURL   *url.URL
	ClientID     string
	ClientSecret string
	RedirectURL  *url.URL
	TokenFile    string
	PollInterval time.Duration
}

// Load reads configuration using getenv and resolves relative paths against
// workingDir. It never includes configuration values in validation errors.
func Load(getenv func(string) string, workingDir string) (Config, error) {
	required := func(name string) (string, error) {
		value := strings.TrimSpace(getenv(name))
		if value == "" {
			return "", fmt.Errorf("%s is required", name)
		}
		return value, nil
	}

	apiRaw, err := required("TRAQ_API_BASE_URL")
	if err != nil {
		return Config{}, err
	}
	clientID, err := required("TRAQ_CLIENT_ID")
	if err != nil {
		return Config{}, err
	}
	clientSecret, err := required("TRAQ_CLIENT_SECRET")
	if err != nil {
		return Config{}, err
	}
	redirectRaw, err := required("TRAQ_REDIRECT_URL")
	if err != nil {
		return Config{}, err
	}

	apiURL, err := url.Parse(apiRaw)
	if err != nil || !validAPIBaseURL(apiURL) {
		return Config{}, errors.New("TRAQ_API_BASE_URL must be an https URL ending in /api/v3")
	}

	redirectURL, err := url.Parse(redirectRaw)
	if err != nil || !validRedirectURL(redirectURL) {
		return Config{}, errors.New("TRAQ_REDIRECT_URL must be an http loopback URL with an explicit port and callback path")
	}

	tokenFile := strings.TrimSpace(getenv("TRAQ_TUI_TOKEN_FILE"))
	if tokenFile == "" {
		tokenFile = filepath.Join(workingDir, ".traq-tui", "token.json")
	} else if !filepath.IsAbs(tokenFile) {
		tokenFile = filepath.Join(workingDir, tokenFile)
	}

	pollInterval := defaultPollInterval
	if raw := strings.TrimSpace(getenv("TRAQ_TUI_POLL_INTERVAL")); raw != "" {
		pollInterval, err = time.ParseDuration(raw)
		if err != nil || pollInterval <= 0 {
			return Config{}, errors.New("TRAQ_TUI_POLL_INTERVAL must be a positive duration")
		}
	}

	return Config{
		APIBaseURL:   apiURL,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		TokenFile:    filepath.Clean(tokenFile),
		PollInterval: pollInterval,
	}, nil
}

func validAPIBaseURL(parsed *url.URL) bool {
	if parsed == nil {
		return false
	}
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	return parsed.Scheme == "https" &&
		parsed.Host != "" &&
		parsed.User == nil &&
		path == "/api/v3" &&
		parsed.RawQuery == "" &&
		parsed.Fragment == ""
}

func validRedirectURL(parsed *url.URL) bool {
	if parsed == nil {
		return false
	}
	host := parsed.Hostname()
	if parsed.Scheme != "http" ||
		parsed.User != nil ||
		parsed.Port() == "" ||
		parsed.Path == "" ||
		parsed.Path == "/" ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return false
	}

	switch strings.ToLower(host) {
	case "localhost":
		return true
	default:
		return net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
	}
}

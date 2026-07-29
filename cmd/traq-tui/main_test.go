package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/oauth2"

	"traq-tui/internal/app"
	"traq-tui/internal/auth"
	"traq-tui/internal/config"
	traqapi "traq-tui/internal/traq"
)

type cliTokenStore struct{}

func (cliTokenStore) Load(context.Context) (*oauth2.Token, error) {
	return nil, auth.ErrTokenNotFound
}

func (cliTokenStore) Save(context.Context, *oauth2.Token) error {
	return nil
}

func (cliTokenStore) Delete(context.Context) error {
	return nil
}

type cliAuthenticator struct {
	visitor          auth.VisitURL
	authorizationURL string
	token            *oauth2.Token
	tokenErr         error
	logoutErr        error
	forceLoginCalls  []bool
	logoutCalls      int
}

func (a *cliAuthenticator) Token(_ context.Context, forceLogin bool) (*oauth2.Token, error) {
	a.forceLoginCalls = append(a.forceLoginCalls, forceLogin)
	if a.authorizationURL != "" {
		a.visitor(a.authorizationURL)
	}
	return a.token, a.tokenErr
}

func (a *cliAuthenticator) TokenSource(
	_ context.Context,
	token *oauth2.Token,
) oauth2.TokenSource {
	return oauth2.StaticTokenSource(token)
}

func (a *cliAuthenticator) Logout(context.Context) error {
	a.logoutCalls++
	return a.logoutErr
}

type cliService struct{}

func (cliService) Channels(context.Context) ([]traqapi.Channel, error) {
	return nil, nil
}

func (cliService) Users(context.Context) (map[string]traqapi.User, error) {
	return nil, nil
}

func (cliService) Messages(context.Context, string, int) ([]traqapi.Message, error) {
	return nil, nil
}

func (cliService) Post(context.Context, string, string) (traqapi.Message, error) {
	return traqapi.Message{}, nil
}

type cliHarness struct {
	authenticator *cliAuthenticator

	workingDirectory string
	workingDirErr    error
	authenticatorErr error
	serviceErr       error
	programErr       error
	browserErr       error

	tokenFile      string
	loadedConfig   config.Config
	serviceBaseURL string
	serviceToken   *oauth2.Token
	modelService   traqapi.Service
	modelPoll      time.Duration
	programModel   tea.Model
	programContext context.Context
	programOutput  io.Writer
	browserURL     string
	programCalls   int
}

func newCLIHarness() *cliHarness {
	return &cliHarness{
		authenticator: &cliAuthenticator{
			token: &oauth2.Token{
				AccessToken:  "fake-access-token",
				RefreshToken: "fake-refresh-token",
				TokenType:    "Bearer",
			},
		},
		workingDirectory: "/workspace",
	}
}

func (h *cliHarness) dependencies() runtimeDependencies {
	return runtimeDependencies{
		workingDirectory: func() (string, error) {
			return h.workingDirectory, h.workingDirErr
		},
		newTokenStore: func(path string) auth.TokenStore {
			h.tokenFile = path
			return cliTokenStore{}
		},
		newAuthenticator: func(
			cfg config.Config,
			_ auth.TokenStore,
			visitor auth.VisitURL,
		) (oauthAuthenticator, error) {
			h.loadedConfig = cfg
			h.authenticator.visitor = visitor
			return h.authenticator, h.authenticatorErr
		},
		newService: func(baseURL string, source oauth2.TokenSource) (traqapi.Service, error) {
			h.serviceBaseURL = baseURL
			if source != nil {
				h.serviceToken, _ = source.Token()
			}
			return cliService{}, h.serviceErr
		},
		newModel: func(service traqapi.Service, poll time.Duration) tea.Model {
			h.modelService = service
			h.modelPoll = poll
			return app.New(service, poll)
		},
		runProgram: func(ctx context.Context, model tea.Model, output io.Writer) error {
			h.programCalls++
			h.programContext = ctx
			h.programModel = model
			h.programOutput = output
			return h.programErr
		},
		openBrowser: func(rawURL string) error {
			h.browserURL = rawURL
			return h.browserErr
		},
	}
}

func validCLIEnvironment() func(string) string {
	values := map[string]string{
		"TRAQ_API_BASE_URL":       "https://traq.example/api/v3",
		"TRAQ_CLIENT_ID":          "fake-client-id",
		"TRAQ_CLIENT_SECRET":      "fake-client-secret",
		"TRAQ_REDIRECT_URL":       "http://127.0.0.1:18080/oauth/callback",
		"TRAQ_TUI_TOKEN_FILE":     "tokens/token.json",
		"TRAQ_TUI_POLL_INTERVAL":  "7s",
		"UNRELATED_SECRET_VALUE":  "must-not-print",
		"UNRELATED_PRIVATE_VALUE": "must-not-print",
	}
	return func(name string) string {
		return values[name]
	}
}

func runCLI(
	t *testing.T,
	harness *cliHarness,
	args []string,
	getenv func(string) string,
) (int, string, string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runWithDependencies(
		context.Background(),
		args,
		getenv,
		&stdout,
		&stderr,
		harness.dependencies(),
	)
	return code, stdout.String(), stderr.String()
}

func TestCLIStartsOAuthServiceAndTUIWithStoredTokenByDefault(t *testing.T) {
	harness := newCLIHarness()

	code, _, stderr := runCLI(t, harness, nil, validCLIEnvironment())

	if code != 0 || stderr != "" {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if len(harness.authenticator.forceLoginCalls) != 1 ||
		harness.authenticator.forceLoginCalls[0] {
		t.Fatalf("force login calls = %#v", harness.authenticator.forceLoginCalls)
	}
	if harness.tokenFile != "/workspace/tokens/token.json" {
		t.Fatalf("token file = %q", harness.tokenFile)
	}
	if harness.serviceBaseURL != "https://traq.example/api/v3" {
		t.Fatalf("service base URL = %q", harness.serviceBaseURL)
	}
	if harness.serviceToken == nil || harness.serviceToken.AccessToken != "fake-access-token" {
		t.Fatalf("service token = %#v", harness.serviceToken)
	}
	if harness.modelService == nil || harness.modelPoll != 7*time.Second {
		t.Fatalf("model wiring: service=%v poll=%v", harness.modelService != nil, harness.modelPoll)
	}
	if harness.programCalls != 1 ||
		harness.programModel == nil ||
		harness.programContext == nil ||
		harness.programOutput == nil {
		t.Fatalf(
			"program wiring: calls=%d model=%v context=%v output=%v",
			harness.programCalls,
			harness.programModel != nil,
			harness.programContext != nil,
			harness.programOutput != nil,
		)
	}
}

func TestCLILoginFlagForcesOAuthLogin(t *testing.T) {
	harness := newCLIHarness()

	code, _, stderr := runCLI(t, harness, []string{"--login"}, validCLIEnvironment())

	if code != 0 || stderr != "" {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if len(harness.authenticator.forceLoginCalls) != 1 ||
		!harness.authenticator.forceLoginCalls[0] {
		t.Fatalf("force login calls = %#v", harness.authenticator.forceLoginCalls)
	}
}

func TestCLILogoutRemovesTokenWithoutStartingTUI(t *testing.T) {
	harness := newCLIHarness()

	code, stdout, stderr := runCLI(t, harness, []string{"--logout"}, validCLIEnvironment())

	if code != 0 || stderr != "" {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if harness.authenticator.logoutCalls != 1 ||
		len(harness.authenticator.forceLoginCalls) != 0 ||
		harness.programCalls != 0 {
		t.Fatalf(
			"logout wiring: logout=%d token=%d program=%d",
			harness.authenticator.logoutCalls,
			len(harness.authenticator.forceLoginCalls),
			harness.programCalls,
		)
	}
	if !strings.Contains(stdout, "Logged out") {
		t.Fatalf("logout confirmation = %q", stdout)
	}
}

func TestCLIRejectsUnknownOrConflictingArguments(t *testing.T) {
	for _, args := range [][]string{
		{"--unknown"},
		{"--login", "--logout"},
		{"extra"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			harness := newCLIHarness()
			code, _, stderr := runCLI(t, harness, args, validCLIEnvironment())
			if code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if !strings.Contains(stderr, "Usage:") {
				t.Fatalf("usage missing from stderr: %q", stderr)
			}
			if len(harness.authenticator.forceLoginCalls) != 0 || harness.programCalls != 0 {
				t.Fatal("invalid arguments started the application")
			}
		})
	}
}

func TestCLIHelpExitsWithoutLoadingConfiguration(t *testing.T) {
	harness := newCLIHarness()
	getenvCalled := false

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runWithDependencies(
		context.Background(),
		[]string{"--help"},
		func(string) string {
			getenvCalled = true
			return ""
		},
		&stdout,
		&stderr,
		harness.dependencies(),
	)

	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "Usage:") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if getenvCalled {
		t.Fatal("--help loaded configuration")
	}
}

func TestCLIConfigurationErrorIsActionableAndRedacted(t *testing.T) {
	harness := newCLIHarness()
	getenv := func(name string) string {
		if name == "TRAQ_CLIENT_SECRET" {
			return "super-secret-value"
		}
		return ""
	}

	code, _, stderr := runCLI(t, harness, nil, getenv)

	if code != 1 || !strings.Contains(stderr, "TRAQ_API_BASE_URL") {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	if strings.Contains(stderr, "super-secret-value") {
		t.Fatalf("configuration secret leaked: %q", stderr)
	}
	if harness.programCalls != 0 {
		t.Fatal("configuration failure started TUI")
	}
}

func TestCLIBrowserFailurePrintsSanitizedManualAuthorizationURL(t *testing.T) {
	harness := newCLIHarness()
	harness.browserErr = errors.New("fake browser failure with fake-client-secret")
	harness.authenticator.authorizationURL = "https://auth.example/authorize?client_id=fake-client-id&client_secret=must-not-print&state=fake-state&access_token=must-not-print#refresh_token=must-not-print"

	code, stdout, stderr := runCLI(t, harness, []string{"--login"}, validCLIEnvironment())

	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if harness.browserURL != harness.authenticator.authorizationURL {
		t.Fatalf("browser URL = %q", harness.browserURL)
	}
	if !strings.Contains(stdout, "Opening browser") ||
		!strings.Contains(stderr, "https://auth.example/authorize") ||
		!strings.Contains(stderr, "client_id=fake-client-id") ||
		!strings.Contains(stderr, "state=fake-state") {
		t.Fatalf("manual authorization guidance missing: stdout=%q stderr=%q", stdout, stderr)
	}
	for _, secret := range []string{
		"fake-client-secret",
		"must-not-print",
		"access_token",
		"refresh_token",
		"client_secret",
	} {
		if strings.Contains(stderr, secret) {
			t.Fatalf("authorization output leaked %q: %q", secret, stderr)
		}
	}
}

func TestCLISanitizedAuthorizationURLRejectsNonHTTPURL(t *testing.T) {
	for _, rawURL := range []string{
		"file://auth.example/authorize?state=fake",
		"javascript://auth.example/authorize?state=fake",
		"relative/authorize?state=fake",
	} {
		if got := safeAuthorizationURL(rawURL); got != "(authorization URL unavailable)" {
			t.Errorf("safeAuthorizationURL(%q) = %q", rawURL, got)
		}
	}
}

func TestCLIReportsAuthenticationServiceAndProgramFailures(t *testing.T) {
	testCases := []struct {
		name       string
		configure  func(*cliHarness)
		wantStatus string
	}{
		{
			name: "authentication",
			configure: func(harness *cliHarness) {
				harness.authenticator.tokenErr = errors.New("fake authentication failure")
			},
			wantStatus: "authentication",
		},
		{
			name: "service",
			configure: func(harness *cliHarness) {
				harness.serviceErr = errors.New("fake service failure")
			},
			wantStatus: "traQ service",
		},
		{
			name: "program",
			configure: func(harness *cliHarness) {
				harness.programErr = errors.New("fake program failure")
			},
			wantStatus: "terminal UI",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newCLIHarness()
			testCase.configure(harness)

			code, _, stderr := runCLI(t, harness, nil, validCLIEnvironment())

			if code != 1 || !strings.Contains(stderr, testCase.wantStatus) {
				t.Fatalf("exit=%d stderr=%q", code, stderr)
			}
			if strings.Contains(stderr, "fake-access-token") ||
				strings.Contains(stderr, "fake-refresh-token") {
				t.Fatalf("token leaked: %q", stderr)
			}
		})
	}
}

func TestCLIWorkingDirectoryAndLogoutFailuresAreNonZero(t *testing.T) {
	testCases := []struct {
		name      string
		args      []string
		configure func(*cliHarness)
		want      string
	}{
		{
			name: "working directory",
			configure: func(harness *cliHarness) {
				harness.workingDirErr = errors.New("fake working directory failure")
			},
			want: "working directory",
		},
		{
			name: "logout",
			args: []string{"--logout"},
			configure: func(harness *cliHarness) {
				harness.authenticator.logoutErr = errors.New("fake logout failure")
			},
			want: "logout",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newCLIHarness()
			testCase.configure(harness)

			code, _, stderr := runCLI(t, harness, testCase.args, validCLIEnvironment())

			if code != 1 || !strings.Contains(stderr, testCase.want) {
				t.Fatalf("exit=%d stderr=%q", code, stderr)
			}
		})
	}
}

func TestCLICanceledContextUsesInterruptExitStatus(t *testing.T) {
	harness := newCLIHarness()
	harness.programErr = context.Canceled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := runWithDependencies(
		ctx,
		nil,
		validCLIEnvironment(),
		&stdout,
		&stderr,
		harness.dependencies(),
	)

	if code != 130 {
		t.Fatalf("exit = %d, want 130; stderr=%q", code, stderr.String())
	}
}

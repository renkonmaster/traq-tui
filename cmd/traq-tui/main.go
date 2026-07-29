package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/oauth2"

	"traq-tui/internal/app"
	"traq-tui/internal/auth"
	"traq-tui/internal/config"
	traqapi "traq-tui/internal/traq"
)

const usage = `Usage: traq-tui [--login | --logout]

  --login   force a new OAuth authorization before starting
  --logout  delete the configured OAuth token and exit
  --help    show this help
`

type oauthAuthenticator interface {
	Token(context.Context, bool) (*oauth2.Token, error)
	TokenSource(context.Context, *oauth2.Token) oauth2.TokenSource
	Logout(context.Context) error
}

type runtimeDependencies struct {
	workingDirectory func() (string, error)
	newTokenStore    func(string) auth.TokenStore
	newAuthenticator func(
		config.Config,
		auth.TokenStore,
		auth.VisitURL,
	) (oauthAuthenticator, error)
	newService  func(string, oauth2.TokenSource) (traqapi.Service, error)
	newModel    func(traqapi.Service, time.Duration) tea.Model
	runProgram  func(context.Context, tea.Model, io.Writer) error
	openBrowser func(string) error
}

func productionDependencies() runtimeDependencies {
	return runtimeDependencies{
		workingDirectory: os.Getwd,
		newTokenStore:    auth.NewFileTokenStore,
		newAuthenticator: func(
			cfg config.Config,
			store auth.TokenStore,
			visitor auth.VisitURL,
		) (oauthAuthenticator, error) {
			return auth.NewAuthenticator(cfg, store, visitor)
		},
		newService: traqapi.NewService,
		newModel: func(service traqapi.Service, interval time.Duration) tea.Model {
			return app.New(service, interval)
		},
		runProgram: func(ctx context.Context, model tea.Model, output io.Writer) error {
			program := tea.NewProgram(
				model,
				tea.WithContext(ctx),
				tea.WithOutput(output),
				tea.WithoutSignalHandler(),
			)
			_, err := program.Run()
			return err
		},
		openBrowser: auth.OpenBrowser,
	}
}

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(
	ctx context.Context,
	args []string,
	getenv func(string) string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	return runWithDependencies(
		ctx,
		args,
		getenv,
		stdout,
		stderr,
		productionDependencies(),
	)
}

func runWithDependencies(
	ctx context.Context,
	args []string,
	getenv func(string) string,
	stdout io.Writer,
	stderr io.Writer,
	dependencies runtimeDependencies,
) int {
	if ctx == nil {
		ctx = context.Background()
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	forceLogin, logout, help, valid := parseArguments(args)
	if help {
		_, _ = io.WriteString(stdout, usage)
		return 0
	}
	if !valid {
		_, _ = io.WriteString(stderr, usage)
		return 2
	}
	if getenv == nil {
		_, _ = fmt.Fprintln(stderr, "configuration: environment reader is unavailable")
		return 1
	}

	workingDirectory, err := dependencies.workingDirectory()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "working directory: %v\n", err)
		return 1
	}
	cfg, err := config.Load(getenv, workingDirectory)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "configuration: %v\n", err)
		return 1
	}

	store := dependencies.newTokenStore(cfg.TokenFile)
	visitor := func(authorizationURL string) {
		_, _ = fmt.Fprintln(stdout, "Opening browser for traQ authorization…")
		if err := dependencies.openBrowser(authorizationURL); err == nil {
			return
		}
		_, _ = fmt.Fprintln(
			stderr,
			"Could not open a browser automatically. Open this authorization URL:",
		)
		_, _ = fmt.Fprintln(stderr, safeAuthorizationURL(authorizationURL))
	}
	authenticator, err := dependencies.newAuthenticator(cfg, store, visitor)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "authentication setup: %v\n", err)
		return 1
	}

	if logout {
		if err := authenticator.Logout(ctx); err != nil {
			_, _ = fmt.Fprintf(stderr, "logout: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, "Logged out; the stored OAuth token was removed.")
		return 0
	}

	token, err := authenticator.Token(ctx, forceLogin)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "authentication: %v\n", err)
		return 1
	}
	if token == nil || token.AccessToken == "" {
		_, _ = fmt.Fprintln(stderr, "authentication: OAuth returned an empty token")
		return 1
	}

	service, err := dependencies.newService(
		cfg.APIBaseURL.String(),
		authenticator.TokenSource(ctx, token),
	)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "traQ service: %v\n", err)
		return 1
	}
	model := dependencies.newModel(service, cfg.PollInterval)
	if err := dependencies.runProgram(ctx, model, stdout); err != nil {
		if ctx.Err() != nil {
			return 130
		}
		_, _ = fmt.Fprintf(stderr, "terminal UI: %v\n", err)
		return 1
	}
	return 0
}

func parseArguments(args []string) (forceLogin, logout, help, valid bool) {
	valid = true
	if len(args) == 0 {
		return false, false, false, true
	}
	if len(args) != 1 {
		return false, false, false, false
	}
	switch args[0] {
	case "--login":
		forceLogin = true
	case "--logout":
		logout = true
	case "--help", "-h":
		help = true
	default:
		valid = false
	}
	return forceLogin, logout, help, valid
}

func safeAuthorizationURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil ||
		parsed.Host == "" ||
		(!strings.EqualFold(parsed.Scheme, "http") &&
			!strings.EqualFold(parsed.Scheme, "https")) {
		return "(authorization URL unavailable)"
	}

	query := parsed.Query()
	for key := range query {
		lowerKey := strings.ToLower(key)
		if lowerKey == "code" ||
			strings.Contains(lowerKey, "secret") ||
			strings.Contains(lowerKey, "token") {
			query.Del(key)
		}
	}
	parsed.RawQuery = query.Encode()
	parsed.Fragment = ""
	parsed.User = nil
	return parsed.String()
}

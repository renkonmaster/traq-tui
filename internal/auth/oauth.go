package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	traqoauth2 "github.com/traPtitech/go-traq-oauth2"
	"golang.org/x/oauth2"

	"traq-tui/internal/config"
)

const (
	loginTimeout       = 3 * time.Minute
	shutdownTimeout    = time.Second
	oauthStateByteSize = 32
)

// VisitURL receives the authorization URL after the loopback server is ready.
type VisitURL func(string)

// Authenticator obtains and refreshes user OAuth tokens.
type Authenticator struct {
	oauthConfig oauth2.Config
	store       TokenStore
	visit       VisitURL
	listen      func(network, address string) (net.Listener, error)
	timeout     time.Duration
}

// NewAuthenticator creates a traQ OAuth Authorization Code authenticator.
func NewAuthenticator(cfg config.Config, store TokenStore, visit VisitURL) (*Authenticator, error) {
	if store == nil {
		return nil, errors.New("OAuth token store is required")
	}
	if visit == nil {
		return nil, errors.New("OAuth authorization URL visitor is required")
	}
	if cfg.APIBaseURL == nil || cfg.RedirectURL == nil {
		return nil, errors.New("OAuth URLs are required")
	}

	endpoint, err := traqoauth2.New(cfg.APIBaseURL.String())
	if err != nil {
		return nil, errors.New("create traQ OAuth endpoint")
	}

	return &Authenticator{
		oauthConfig: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     endpoint,
			RedirectURL:  cfg.RedirectURL.String(),
			Scopes:       []string{traqoauth2.ScopeRead, traqoauth2.ScopeWrite},
		},
		store:   store,
		visit:   visit,
		listen:  net.Listen,
		timeout: loginTimeout,
	}, nil
}

// Token returns a stored token when possible and otherwise completes a new
// loopback Authorization Code login. forceLogin always starts a new login.
func (a *Authenticator) Token(ctx context.Context, forceLogin bool) (*oauth2.Token, error) {
	if !forceLogin {
		token, err := a.store.Load(ctx)
		switch {
		case err == nil:
			return token, nil
		case errors.Is(err, ErrTokenNotFound):
		default:
			return nil, fmt.Errorf("load OAuth token: %w", err)
		}
	}
	return a.login(ctx)
}

func (a *Authenticator) login(ctx context.Context) (*oauth2.Token, error) {
	timeout := a.timeout
	if timeout <= 0 {
		timeout = loginTimeout
	}
	loginCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	redirectURL, err := url.Parse(a.oauthConfig.RedirectURL)
	if err != nil {
		return nil, errors.New("parse OAuth redirect URL")
	}
	listener, err := a.listen("tcp", redirectURL.Host)
	if err != nil {
		return nil, fmt.Errorf("listen for OAuth callback: %w", err)
	}

	state, err := newOAuthState()
	if err != nil {
		_ = listener.Close()
		return nil, err
	}

	type callbackResult struct {
		code string
		err  error
	}
	results := make(chan callbackResult, 1)
	var resultOnce sync.Once
	sendResult := func(result callbackResult) {
		resultOnce.Do(func() {
			results <- result
		})
	}

	mux := http.NewServeMux()
	mux.HandleFunc(redirectURL.Path, func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
			return
		}
		query := request.URL.Query()
		if subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(state)) != 1 {
			http.Error(w, "OAuth state validation failed. Return to the terminal.", http.StatusBadRequest)
			sendResult(callbackResult{err: errors.New("OAuth state validation failed")})
			return
		}
		if query.Get("error") != "" {
			http.Error(w, "Authorization was denied. Return to the terminal.", http.StatusBadRequest)
			sendResult(callbackResult{err: errors.New("OAuth provider returned an authorization error")})
			return
		}
		code := query.Get("code")
		if code == "" {
			http.Error(w, "Authorization code is missing. Return to the terminal.", http.StatusBadRequest)
			sendResult(callbackResult{err: errors.New("OAuth callback did not include a code")})
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<!doctype html><title>traq-tui</title><p>Authorization received. You can return to the terminal.</p>"))
		sendResult(callbackResult{code: code})
	})

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	serveErrors := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrors <- err
		}
	}()

	a.visit(a.oauthConfig.AuthCodeURL(state))

	var result callbackResult
	select {
	case result = <-results:
	case err := <-serveErrors:
		result.err = fmt.Errorf("serve OAuth callback: %w", err)
	case <-loginCtx.Done():
		result.err = loginCtx.Err()
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	_ = server.Shutdown(shutdownCtx)
	shutdownCancel()

	if result.err != nil {
		return nil, result.err
	}

	token, err := a.oauthConfig.Exchange(loginCtx, result.code)
	if err != nil {
		return nil, fmt.Errorf("exchange OAuth authorization code: %w", err)
	}
	if token.AccessToken == "" {
		return nil, errors.New("OAuth provider returned an empty access token")
	}
	if err := a.store.Save(loginCtx, token); err != nil {
		return nil, fmt.Errorf("save OAuth token: %w", err)
	}
	return token, nil
}

func newOAuthState() (string, error) {
	raw := make([]byte, oauthStateByteSize)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("generate OAuth state")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// TokenSource returns an OAuth token source that persists refreshed tokens.
func (a *Authenticator) TokenSource(ctx context.Context, token *oauth2.Token) oauth2.TokenSource {
	return &persistingTokenSource{
		ctx:    ctx,
		source: a.oauthConfig.TokenSource(ctx, token),
		store:  a.store,
		last:   cloneToken(token),
	}
}

// Logout removes the configured persisted token.
func (a *Authenticator) Logout(ctx context.Context) error {
	return a.store.Delete(ctx)
}

type persistingTokenSource struct {
	mu     sync.Mutex
	ctx    context.Context
	source oauth2.TokenSource
	store  TokenStore
	last   *oauth2.Token
}

func (s *persistingTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	token, err := s.source.Token()
	if err != nil {
		return nil, err
	}
	if sameToken(s.last, token) {
		return token, nil
	}
	if err := s.store.Save(s.ctx, token); err != nil {
		return nil, fmt.Errorf("save refreshed OAuth token: %w", err)
	}
	s.last = cloneToken(token)
	return token, nil
}

func sameToken(left, right *oauth2.Token) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.AccessToken == right.AccessToken &&
		left.TokenType == right.TokenType &&
		left.RefreshToken == right.RefreshToken &&
		left.Expiry.Equal(right.Expiry)
}

func cloneToken(token *oauth2.Token) *oauth2.Token {
	if token == nil {
		return nil
	}
	copy := *token
	return &copy
}

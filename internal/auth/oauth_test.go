package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"traq-tui/internal/config"
)

type memoryTokenStore struct {
	mu      sync.Mutex
	token   *oauth2.Token
	loadErr error
	saves   int
	deletes int
}

func (s *memoryTokenStore) Load(ctx context.Context) (*oauth2.Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	if s.token == nil {
		return nil, ErrTokenNotFound
	}
	copy := *s.token
	return &copy, nil
}

func (s *memoryTokenStore) Save(ctx context.Context, token *oauth2.Token) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *token
	s.token = &copy
	s.saves++
	return nil
}

func (s *memoryTokenStore) Delete(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = nil
	s.deletes++
	return nil
}

func (s *memoryTokenStore) snapshot() (*oauth2.Token, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var token *oauth2.Token
	if s.token != nil {
		copy := *s.token
		token = &copy
	}
	return token, s.saves, s.deletes
}

type oauthTestRig struct {
	authenticator *Authenticator
	redirectURL   string
	visited       chan string
	store         *memoryTokenStore
}

func newOAuthTestRig(t *testing.T, tokenHandler http.Handler) oauthTestRig {
	t.Helper()

	provider := httptest.NewServer(tokenHandler)
	t.Cleanup(provider.Close)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})

	redirectURL := "http://" + listener.Addr().String() + "/callback"
	visited := make(chan string, 1)
	store := &memoryTokenStore{}
	listenOnce := sync.Once{}
	authenticator := &Authenticator{
		oauthConfig: oauth2.Config{
			ClientID:     "fake-client-id",
			ClientSecret: "fake-client-secret",
			RedirectURL:  redirectURL,
			Scopes:       []string{"read", "write"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  provider.URL + "/authorize",
				TokenURL: provider.URL + "/token",
			},
		},
		store: store,
		visit: func(rawURL string) {
			visited <- rawURL
		},
		listen: func(_, _ string) (net.Listener, error) {
			var result net.Listener
			listenOnce.Do(func() {
				result = listener
			})
			if result == nil {
				return nil, errors.New("listener requested more than once")
			}
			return result, nil
		},
		timeout: time.Second,
	}

	return oauthTestRig{
		authenticator: authenticator,
		redirectURL:   redirectURL,
		visited:       visited,
		store:         store,
	}
}

func successfulTokenHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("code") != "fake-code" {
			t.Errorf("code = %q", r.Form.Get("code"))
		}
		if r.Form.Get("redirect_uri") == "" {
			t.Error("redirect_uri was omitted")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"access_token":"fake-access-token",
			"refresh_token":"fake-refresh-token",
			"token_type":"Bearer",
			"expires_in":3600
		}`)
	})
}

func beginLogin(t *testing.T, rig oauthTestRig) (<-chan error, *url.URL) {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		_, err := rig.authenticator.Token(context.Background(), true)
		result <- err
	}()

	var rawAuthorizationURL string
	select {
	case rawAuthorizationURL = <-rig.visited:
	case <-time.After(time.Second):
		t.Fatal("authorization URL was not visited")
	}
	authorizationURL, err := url.Parse(rawAuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	if authorizationURL.Query().Get("state") == "" {
		t.Fatal("authorization URL omitted state")
	}
	if authorizationURL.Query().Get("scope") != "read write" {
		t.Fatalf("scope = %q", authorizationURL.Query().Get("scope"))
	}
	return result, authorizationURL
}

func TestAuthenticatorReusesStoredToken(t *testing.T) {
	store := &memoryTokenStore{
		token: &oauth2.Token{AccessToken: "fake-stored-token"},
	}
	visited := false
	authenticator := &Authenticator{
		store: store,
		visit: func(string) {
			visited = true
		},
	}

	got, err := authenticator.Token(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "fake-stored-token" {
		t.Fatal("stored access token was not returned")
	}
	if visited {
		t.Fatal("authorization URL was visited despite a stored token")
	}
}

func TestNewAuthenticatorConfiguresTraQOAuth(t *testing.T) {
	apiBaseURL, err := url.Parse("https://q.example.test/api/v3")
	if err != nil {
		t.Fatal(err)
	}
	redirectURL, err := url.Parse("http://127.0.0.1:18080/callback")
	if err != nil {
		t.Fatal(err)
	}

	authenticator, err := NewAuthenticator(config.Config{
		APIBaseURL:   apiBaseURL,
		ClientID:     "fake-client-id",
		ClientSecret: "fake-client-secret",
		RedirectURL:  redirectURL,
	}, &memoryTokenStore{}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}

	if got := authenticator.oauthConfig.Endpoint.AuthURL; got != "https://q.example.test/api/v3/oauth2/authorize" {
		t.Fatalf("authorization endpoint = %q", got)
	}
	if got := authenticator.oauthConfig.Endpoint.TokenURL; got != "https://q.example.test/api/v3/oauth2/token" {
		t.Fatalf("token endpoint = %q", got)
	}
	if got := strings.Join(authenticator.oauthConfig.Scopes, " "); got != "read write" {
		t.Fatalf("scopes = %q", got)
	}
}

func TestAuthenticatorForcedLoginExchangesAndSavesToken(t *testing.T) {
	rig := newOAuthTestRig(t, successfulTokenHandler(t))
	rig.store.token = &oauth2.Token{AccessToken: "fake-stored-token"}

	result := make(chan struct {
		token *oauth2.Token
		err   error
	}, 1)
	go func() {
		token, err := rig.authenticator.Token(context.Background(), true)
		result <- struct {
			token *oauth2.Token
			err   error
		}{token: token, err: err}
	}()

	authorizationURL := <-rig.visited
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	callback := rig.redirectURL + "?code=fake-code&state=" + url.QueryEscape(parsed.Query().Get("state"))
	response, err := http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d", response.StatusCode)
	}

	loginResult := <-result
	if loginResult.err != nil {
		t.Fatal(loginResult.err)
	}
	if loginResult.token.AccessToken != "fake-access-token" {
		t.Fatal("exchanged access token was not returned")
	}
	saved, saves, _ := rig.store.snapshot()
	if saves != 1 || saved == nil || saved.AccessToken != "fake-access-token" {
		t.Fatalf("saved token = %#v, saves = %d", saved, saves)
	}
}

func TestAuthenticatorRejectsMismatchedState(t *testing.T) {
	rig := newOAuthTestRig(t, successfulTokenHandler(t))
	result, authorizationURL := beginLogin(t, rig)

	callback := rig.redirectURL + "?code=fake-code&state=" +
		url.QueryEscape(authorizationURL.Query().Get("state")+"-wrong")
	response, err := http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("callback status = %d", response.StatusCode)
	}

	if err := <-result; err == nil || !strings.Contains(strings.ToLower(err.Error()), "state") {
		t.Fatalf("error = %v, want state error", err)
	}
	if _, saves, _ := rig.store.snapshot(); saves != 0 {
		t.Fatalf("saved %d tokens after invalid state", saves)
	}
}

func TestAuthenticatorRejectsProviderErrorAndMissingCode(t *testing.T) {
	for name, query := range map[string]string{
		"provider error": "error=access_denied",
		"missing code":   "",
	} {
		t.Run(name, func(t *testing.T) {
			rig := newOAuthTestRig(t, successfulTokenHandler(t))
			result, authorizationURL := beginLogin(t, rig)
			separator := "?"
			if query != "" {
				query += "&"
			}
			callback := rig.redirectURL + separator + query + "state=" +
				url.QueryEscape(authorizationURL.Query().Get("state"))
			response, err := http.Get(callback)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()

			if err := <-result; err == nil {
				t.Fatal("expected callback error")
			}
			if _, saves, _ := rig.store.snapshot(); saves != 0 {
				t.Fatalf("saved %d tokens after callback error", saves)
			}
		})
	}
}

func TestAuthenticatorStopsWaitingWhenContextIsCanceled(t *testing.T) {
	rig := newOAuthTestRig(t, successfulTokenHandler(t))
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := rig.authenticator.Token(ctx, true)
		result <- err
	}()
	<-rig.visited
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("login did not stop after cancellation")
	}
}

func TestAuthenticatorTimesOutWhileWaitingForCallback(t *testing.T) {
	rig := newOAuthTestRig(t, successfulTokenHandler(t))
	rig.authenticator.timeout = 20 * time.Millisecond
	result := make(chan error, 1)
	go func() {
		_, err := rig.authenticator.Token(context.Background(), true)
		result <- err
	}()
	<-rig.visited

	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("login did not time out")
	}
}

type sequenceTokenSource struct {
	tokens []*oauth2.Token
	index  int
}

func (s *sequenceTokenSource) Token() (*oauth2.Token, error) {
	if s.index >= len(s.tokens) {
		return nil, errors.New("no token available")
	}
	token := *s.tokens[s.index]
	s.index++
	return &token, nil
}

func TestPersistingTokenSourceSavesOnlyChangedTokens(t *testing.T) {
	initial := &oauth2.Token{
		AccessToken:  "fake-access-1",
		RefreshToken: "fake-refresh",
		TokenType:    "Bearer",
		Expiry:       time.Unix(100, 0),
	}
	unchanged := *initial
	changed := *initial
	changed.AccessToken = "fake-access-2"
	changed.Expiry = time.Unix(200, 0)
	store := &memoryTokenStore{}
	source := &persistingTokenSource{
		ctx:    context.Background(),
		source: &sequenceTokenSource{tokens: []*oauth2.Token{&unchanged, &changed}},
		store:  store,
		last:   initial,
	}

	if _, err := source.Token(); err != nil {
		t.Fatal(err)
	}
	if _, saves, _ := store.snapshot(); saves != 0 {
		t.Fatalf("saved unchanged token %d times", saves)
	}
	if _, err := source.Token(); err != nil {
		t.Fatal(err)
	}
	saved, saves, _ := store.snapshot()
	if saves != 1 || saved.AccessToken != "fake-access-2" {
		t.Fatalf("saved token = %#v, saves = %d", saved, saves)
	}
}

func TestAuthenticatorLogoutDeletesStoredToken(t *testing.T) {
	store := &memoryTokenStore{token: &oauth2.Token{AccessToken: "fake-token"}}
	authenticator := &Authenticator{store: store}

	if err := authenticator.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	token, _, deletes := store.snapshot()
	if token != nil || deletes != 1 {
		t.Fatalf("token = %#v, deletes = %d", token, deletes)
	}
}

func TestAuthenticatorReturnsStoreLoadFailure(t *testing.T) {
	store := &memoryTokenStore{loadErr: errors.New("fake storage failure")}
	authenticator := &Authenticator{store: store}

	_, err := authenticator.Token(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "storage") {
		t.Fatalf("error = %v", err)
	}
}

func TestTokenComparisonIncludesAllOAuthFields(t *testing.T) {
	base := &oauth2.Token{
		AccessToken:  "fake-access",
		TokenType:    "Bearer",
		RefreshToken: "fake-refresh",
		Expiry:       time.Unix(100, 0),
	}
	tests := []*oauth2.Token{
		{AccessToken: "fake-changed", TokenType: base.TokenType, RefreshToken: base.RefreshToken, Expiry: base.Expiry},
		{AccessToken: base.AccessToken, TokenType: "MAC", RefreshToken: base.RefreshToken, Expiry: base.Expiry},
		{AccessToken: base.AccessToken, TokenType: base.TokenType, RefreshToken: "fake-changed", Expiry: base.Expiry},
		{AccessToken: base.AccessToken, TokenType: base.TokenType, RefreshToken: base.RefreshToken, Expiry: time.Unix(200, 0)},
	}
	for index, changed := range tests {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			if sameToken(base, changed) {
				t.Fatal("different tokens compared equal")
			}
		})
	}
}

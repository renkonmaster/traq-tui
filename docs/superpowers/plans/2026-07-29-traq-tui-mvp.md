# traQ TUI MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Do not dispatch subagents unless the user explicitly requests delegation.

**Goal:** Build a Linux-first unofficial traQ TUI that authenticates a user through OAuth, persists the token, reads recent channel messages, and posts to the selected channel.

**Architecture:** A small Go command wires configuration, OAuth, and the official traQ SDK into a Bubble Tea application. Authentication and API access sit behind narrow interfaces, while all TUI state changes occur in Bubble Tea's update loop.

**Tech Stack:** Go 1.26, `golang.org/x/oauth2`, `github.com/traPtitech/go-traq-oauth2`, `github.com/traPtitech/go-traq`, Bubble Tea v2, Bubbles v2, Lip Gloss v2, Go standard-library tests.

## Global Constraints

- Write only inside `/home/renkon/develop/traq-tui` or `/tmp`.
- Do not inspect SSH keys, credential stores, shell history, broad environment dumps, or unrelated sensitive files.
- Do not initialize Git; the user will create and connect the repository later.
- If Git exists when a commit step is reached, commit only files from that task. Otherwise record that the commit was skipped.
- Never put a credential or token in source, tests, logs, command output, or documentation.
- Use current stable dependency releases; reject preview, beta, and release-candidate versions.
- Use test-driven development: observe the relevant test fail before implementation and pass afterward.
- Keep remote traQ content out of terminal control sequences by sanitizing it before rendering.
- Do not claim live traQ success until the user has locally supplied OAuth environment variables and confirmed the manual check.

---

## File Map

- `go.mod`, `go.sum`: Go module and resolved dependencies.
- `.gitignore`: ignores `.env`, `.traq-tui/`, local binaries, and coverage output.
- `.env.example`: documents required variables using obviously fake sample values.
- `cmd/traq-tui/main.go`: CLI parsing, dependency wiring, and process exit behavior.
- `internal/config/config.go`: environment parsing and validation.
- `internal/auth/store.go`: atomic owner-only OAuth token persistence.
- `internal/auth/oauth.go`: Authorization Code Flow and loopback callback.
- `internal/auth/browser.go`: Linux browser opening with a printable fallback URL.
- `internal/traq/types.go`: small application-facing channel, user, and message values.
- `internal/traq/client.go`: narrow API interface and official SDK adapter.
- `internal/app/model.go`: TUI state, focus, constructors, and typed event messages.
- `internal/app/commands.go`: asynchronous API and polling commands.
- `internal/app/update.go`: keyboard and result-driven state transitions.
- `internal/app/view.go`: responsive panes, help, status, and composer rendering.
- `internal/app/sanitize.go`: removal of terminal control sequences.
- `README.md`: OAuth setup, configuration, usage, keys, security, and MVP limits.
- Matching `_test.go` files: unit, `httptest`, and Bubble Tea state tests.

### Task 1: Project Foundation and Validated Configuration

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `.env.example`
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `type Config struct { APIBaseURL *url.URL; ClientID string; ClientSecret string; RedirectURL *url.URL; TokenFile string; PollInterval time.Duration }`
  - `func Load(getenv func(string) string, workingDir string) (Config, error)`

- [ ] **Step 1: Write failing table-driven configuration tests**

Cover a complete valid configuration, missing required variables, an API URL
without `/api/v3`, a non-loopback redirect, an invalid polling duration, the
default workspace-relative token file, and a user-provided token path.

```go
func TestLoadValid(t *testing.T) {
    env := map[string]string{
        "TRAQ_API_BASE_URL": "https://q.example.test/api/v3",
        "TRAQ_CLIENT_ID": "client-id",
        "TRAQ_CLIENT_SECRET": "client-secret",
        "TRAQ_REDIRECT_URL": "http://127.0.0.1:18080/callback",
    }
    got, err := Load(func(k string) string { return env[k] }, "/work/traq-tui")
    if err != nil { t.Fatal(err) }
    if got.TokenFile != "/work/traq-tui/.traq-tui/token.json" { t.Fatalf("token file = %q", got.TokenFile) }
    if got.PollInterval != 5*time.Second { t.Fatalf("poll interval = %s", got.PollInterval) }
}
```

- [ ] **Step 2: Run the focused test and confirm failure**

Run: `go test ./internal/config`

Expected: FAIL because the module/package or `Load` does not exist.

- [ ] **Step 3: Create the module and implement strict configuration parsing**

Initialize the local module as `module traq-tui`. Make errors name the missing
variable without echoing its value. Accept only `https` API URLs ending in
`/api/v3`. Accept only `http` redirect URLs with `localhost`, `127.0.0.1`, or
`::1` and a non-zero explicit port. Resolve a relative token path under
`workingDir`.

```go
func Load(getenv func(string) string, workingDir string) (Config, error) {
    required := func(name string) (string, error) {
        value := strings.TrimSpace(getenv(name))
        if value == "" {
            return "", fmt.Errorf("%s is required", name)
        }
        return value, nil
    }
    apiRaw, err := required("TRAQ_API_BASE_URL")
    if err != nil { return Config{}, err }
    clientID, err := required("TRAQ_CLIENT_ID")
    if err != nil { return Config{}, err }
    clientSecret, err := required("TRAQ_CLIENT_SECRET")
    if err != nil { return Config{}, err }
    redirectRaw, err := required("TRAQ_REDIRECT_URL")
    if err != nil { return Config{}, err }
    apiURL, err := url.Parse(apiRaw)
    if err != nil || apiURL.Scheme != "https" || !strings.HasSuffix(strings.TrimRight(apiURL.Path, "/"), "/api/v3") {
        return Config{}, errors.New("TRAQ_API_BASE_URL must be an https URL ending in /api/v3")
    }
    redirectURL, err := url.Parse(redirectRaw)
    host := redirectURL.Hostname()
    if err != nil || redirectURL.Scheme != "http" || redirectURL.Port() == "" ||
        (host != "localhost" && host != "127.0.0.1" && host != "::1") {
        return Config{}, errors.New("TRAQ_REDIRECT_URL must be an http loopback URL with an explicit port")
    }
    tokenFile := strings.TrimSpace(getenv("TRAQ_TUI_TOKEN_FILE"))
    if tokenFile == "" { tokenFile = filepath.Join(workingDir, ".traq-tui", "token.json") }
    if !filepath.IsAbs(tokenFile) { tokenFile = filepath.Join(workingDir, tokenFile) }
    poll := 5 * time.Second
    if raw := strings.TrimSpace(getenv("TRAQ_TUI_POLL_INTERVAL")); raw != "" {
        poll, err = time.ParseDuration(raw)
        if err != nil || poll <= 0 { return Config{}, errors.New("TRAQ_TUI_POLL_INTERVAL must be a positive duration") }
    }
    return Config{APIBaseURL: apiURL, ClientID: clientID, ClientSecret: clientSecret, RedirectURL: redirectURL, TokenFile: filepath.Clean(tokenFile), PollInterval: poll}, nil
}
```

- [ ] **Step 4: Add ignore and environment example files**

`.gitignore` must contain `.env`, `.traq-tui/`, `/traq-tui`, `coverage.out`,
and no broad patterns that hide source. `.env.example` contains variable names
and safe example URLs only.

- [ ] **Step 5: Verify Task 1**

Run: `gofmt -w internal/config && go test ./internal/config`

Expected: PASS.

- [ ] **Step 6: Commit if Git exists**

```bash
git add go.mod .gitignore .env.example internal/config
git commit -m "feat: add validated traQ configuration"
```

### Task 2: Atomic Token Store

**Files:**
- Create: `internal/auth/store.go`
- Test: `internal/auth/store_test.go`

**Interfaces:**
- Produces:
  - `var ErrTokenNotFound error`
  - `type TokenStore interface { Load(context.Context) (*oauth2.Token, error); Save(context.Context, *oauth2.Token) error; Delete(context.Context) error }`
  - `func NewFileTokenStore(path string) TokenStore`

- [ ] **Step 1: Write failing token-store tests**

Test missing files, save/load round trips, directory mode `0700`, file mode
`0600`, replacement of an existing token, corrupt JSON, and deletion that
returns nil when the file is already absent.

```go
func TestFileTokenStorePermissions(t *testing.T) {
    path := filepath.Join(t.TempDir(), "private", "token.json")
    store := NewFileTokenStore(path)
    if err := store.Save(context.Background(), &oauth2.Token{AccessToken: "test-token"}); err != nil { t.Fatal(err) }
    info, err := os.Stat(path)
    if err != nil { t.Fatal(err) }
    if info.Mode().Perm() != 0o600 { t.Fatalf("mode = %o", info.Mode().Perm()) }
}
```

- [ ] **Step 2: Run the focused tests and confirm failure**

Run: `go test ./internal/auth -run TokenStore`

Expected: FAIL because the store is undefined.

- [ ] **Step 3: Implement atomic owner-only persistence**

Create the parent directory with `0700`, reject a symlink token target, encode
to a temporary file in the same directory, `Chmod(0600)`, `Sync`, close, and
rename. Never include token contents in an error.

- [ ] **Step 4: Verify Task 2**

Run: `gofmt -w internal/auth && go test ./internal/auth -run TokenStore`

Expected: PASS.

- [ ] **Step 5: Commit if Git exists**

```bash
git add internal/auth/store.go internal/auth/store_test.go go.mod go.sum
git commit -m "feat: persist OAuth tokens safely"
```

### Task 3: OAuth Authorization Code Flow

**Files:**
- Create: `internal/auth/oauth.go`
- Test: `internal/auth/oauth_test.go`

**Interfaces:**
- Consumes: `config.Config`, `TokenStore`
- Produces:
  - `type VisitURL func(string)`
  - `type Authenticator struct`
  - `func NewAuthenticator(cfg config.Config, store TokenStore, visit VisitURL) (*Authenticator, error)`
  - `func (a *Authenticator) Token(ctx context.Context, forceLogin bool) (*oauth2.Token, error)`
  - `func (a *Authenticator) TokenSource(ctx context.Context, token *oauth2.Token) oauth2.TokenSource`
  - `func (a *Authenticator) Logout(ctx context.Context) error`

- [ ] **Step 1: Write failing callback and token-reuse tests**

Use an `httptest.Server` as the authorization/token provider and an injected
opener that captures the URL. Cover loading a saved token, forced login,
successful callback/exchange/save, mismatched state, provider error, missing
code, cancellation, and a three-minute timeout represented by an injected
context deadline in tests.

```go
func TestAuthenticatorRejectsMismatchedState(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), time.Second)
    defer cancel()
    visited := make(chan string, 1)
    auth := newTestAuthenticator(t, func(rawURL string) { visited <- rawURL })
    result := make(chan error, 1)
    go func() {
        _, err := auth.Token(ctx, true)
        result <- err
    }()
    authorizationURL := <-visited
    parsed, err := url.Parse(authorizationURL)
    if err != nil { t.Fatal(err) }
    callback := auth.testRedirectURL + "?code=fake-code&state=" + url.QueryEscape(parsed.Query().Get("state")+"-wrong")
    response, err := http.Get(callback)
    if err != nil { t.Fatal(err) }
    response.Body.Close()
    if err := <-result; err == nil || !strings.Contains(err.Error(), "state") {
        t.Fatalf("error = %v", err)
    }
}
```

Define `newTestAuthenticator` in the test file to create an isolated loopback
listener, an `httptest.Server` token endpoint, and an in-memory `TokenStore`;
its returned test wrapper exposes only `Token` and the registered
`testRedirectURL`.

- [ ] **Step 2: Run the focused tests and confirm failure**

Run: `go test ./internal/auth -run Authenticator`

Expected: FAIL because `Authenticator` is undefined.

- [ ] **Step 3: Implement OAuth configuration**

Build the provider endpoints with `traqoauth2.New(cfg.APIBaseURL.String())`.
Configure `read` and `write` scopes, the client ID/secret, and the exact
redirect URL. Generate `state` from 32 random bytes and encode it with
base64url.

- [ ] **Step 4: Implement the loopback callback lifecycle**

Listen only on the configured loopback address, register only the configured
path, start the HTTP server before calling `VisitURL`, validate state using
`subtle.ConstantTimeCompare`, exchange the code, show a short success/failure
HTML response, shut down the server, and save the token. The CLI supplies a
`VisitURL` implementation that tries the browser and prints the authorization
URL when browser startup fails, so the callback continues waiting either way.

- [ ] **Step 5: Persist refreshed tokens**

Wrap `oauth2.Config.TokenSource` with a token source that calls `Save` only
when the access token, refresh token, token type, or expiry changes.

- [ ] **Step 6: Verify Task 3**

Run: `gofmt -w internal/auth && go test -race ./internal/auth`

Expected: PASS without a browser launch or external network request.

- [ ] **Step 7: Commit if Git exists**

```bash
git add internal/auth go.mod go.sum
git commit -m "feat: add loopback OAuth login"
```

### Task 4: Narrow traQ API Adapter

**Files:**
- Create: `internal/traq/types.go`
- Create: `internal/traq/client.go`
- Test: `internal/traq/client_test.go`

**Interfaces:**
- Produces:
  - `type Channel struct { ID string; Name string; Path string }`
  - `type User struct { ID string; Name string; DisplayName string }`
  - `type Message struct { ID string; ChannelID string; UserID string; Content string; CreatedAt time.Time }`
  - `type Service interface { Channels(context.Context) ([]Channel, error); Users(context.Context) (map[string]User, error); Messages(context.Context, string, int) ([]Message, error); Post(context.Context, string, string) (Message, error) }`
  - `func NewService(apiBaseURL string, tokens oauth2.TokenSource) (Service, error)`

- [ ] **Step 1: Write failing HTTP mapping tests**

Serve minimal JSON for the four required endpoints. Assert authorization uses
`Bearer <latest token>`, the base URL is configurable, messages request
`limit=50` with descending API order but are returned oldest-to-newest, blank
posts are rejected before HTTP, and non-2xx responses become redacted errors.

```go
func TestServiceMessagesMapsAndOrders(t *testing.T) {
    server := newTraQTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/channels/channel-1/messages" { http.NotFound(w, r); return }
        if r.URL.Query().Get("limit") != "50" { t.Errorf("limit = %q", r.URL.Query().Get("limit")) }
        if r.Header.Get("Authorization") != "Bearer access-token" { t.Errorf("authorization header missing") }
        w.Header().Set("Content-Type", "application/json")
        io.WriteString(w, `[
          {"id":"new","userId":"user-1","channelId":"channel-1","content":"second","createdAt":"2026-07-29T01:01:00Z","updatedAt":"2026-07-29T01:01:00Z","pinned":false,"stamps":[],"threadId":null},
          {"id":"old","userId":"user-1","channelId":"channel-1","content":"first","createdAt":"2026-07-29T01:00:00Z","updatedAt":"2026-07-29T01:00:00Z","pinned":false,"stamps":[],"threadId":null}
        ]`)
    }))
    service, err := NewService(server.URL, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "access-token"}))
    if err != nil { t.Fatal(err) }
    messages, err := service.Messages(context.Background(), "channel-1", 50)
    if err != nil { t.Fatal(err) }
    if len(messages) != 2 || messages[0].ID != "old" || messages[1].ID != "new" {
        t.Fatalf("messages = %#v", messages)
    }
}
```

Define `newTraQTestServer` in the same test file as a thin wrapper around
`httptest.NewServer`, and ensure `t.Cleanup(server.Close)` is registered.

- [ ] **Step 2: Run the focused tests and confirm failure**

Run: `go test ./internal/traq`

Expected: FAIL because the service does not exist.

- [ ] **Step 3: Implement the official SDK adapter**

Use `github.com/traPtitech/go-traq`. Before each call, obtain the latest token
from `oauth2.TokenSource` and add it with
`context.WithValue(ctx, traq.ContextAccessToken, token.AccessToken)`. Configure
the generated client's server URL for the supplied base. Use the generated
Channel, User, and Message API methods rather than handwritten production HTTP.

- [ ] **Step 4: Normalize channels and API errors**

Flatten the channel tree into stable path order for display. Preserve the
original channel UUID for calls. Error strings may contain HTTP status and
operation but never request authorization headers, token values, or full
response bodies.

- [ ] **Step 5: Verify Task 4**

Run: `gofmt -w internal/traq && go test -race ./internal/traq`

Expected: PASS.

- [ ] **Step 6: Commit if Git exists**

```bash
git add internal/traq go.mod go.sum
git commit -m "feat: wrap the traQ message API"
```

### Task 5: TUI State, Channel Navigation, and Message Loading

**Files:**
- Create: `internal/app/model.go`
- Create: `internal/app/commands.go`
- Create: `internal/app/update.go`
- Test: `internal/app/update_test.go`

**Interfaces:**
- Consumes: `traq.Service`
- Produces:
  - `type Model struct`
  - `func New(service traq.Service, pollInterval time.Duration) Model`
  - Bubble Tea `Init`, `Update`, and later `View` methods

- [ ] **Step 1: Write failing state-transition tests**

Use a fake `traq.Service`. Test initial concurrent channel/user loads, channel
selection, `j`/`k` and arrow navigation, `/` filter focus, `enter` selection,
manual `r` refresh, `q` only quitting outside input modes, loading/error status,
and ignoring a message response for a channel that is no longer selected.

```go
func TestStaleMessageResultIsIgnored(t *testing.T) {
    m := newReadyTestModel()
    m.selectedChannelID = "new-channel"
    next, _ := m.Update(messagesLoadedMsg{channelID: "old-channel", messages: []traq.Message{{ID: "stale"}}})
    if len(next.(Model).messages) != 0 { t.Fatal("stale response replaced current messages") }
}
```

Define `newReadyTestModel` in the test file with a fake `traq.Service`, an
80×24 window, loaded empty user/message maps, and `selectedChannelID` left
empty so each test explicitly selects its channel.

- [ ] **Step 2: Run the focused tests and confirm failure**

Run: `go test ./internal/app -run 'Navigation|Load|Stale|Refresh'`

Expected: FAIL because `Model` is undefined.

- [ ] **Step 3: Implement typed commands and state**

Define typed result messages for channel, user, message, and API error results.
Represent focus as an enum with channel, messages, filter, composer, and help.
All service calls run inside `tea.Cmd`; `Update` alone mutates `Model`.

- [ ] **Step 4: Implement channel filtering and message loading**

Filter case-insensitively by full channel path. Preserve selection when possible.
Selecting a channel launches a request for the latest 50 messages and records
the request channel ID so late responses cannot replace another channel.

- [ ] **Step 5: Verify Task 5**

Run: `gofmt -w internal/app && go test -race ./internal/app`

Expected: PASS.

- [ ] **Step 6: Commit if Git exists**

```bash
git add internal/app go.mod go.sum
git commit -m "feat: add channel and message state"
```

### Task 6: Responsive Rendering and Remote-Text Sanitization

**Files:**
- Create: `internal/app/view.go`
- Create: `internal/app/sanitize.go`
- Test: `internal/app/view_test.go`
- Test: `internal/app/sanitize_test.go`

**Interfaces:**
- Consumes: `Model`
- Produces: `func (m Model) View() tea.View`
- Produces: `func sanitizeRemoteText(string) string`

- [ ] **Step 1: Write failing rendering and sanitization tests**

Test 80×24 output includes channel, author, timestamp, wrapped content, and
status; smaller dimensions show a resize message; absent users fall back to a
short user ID; control sequences such as CSI color, OSC hyperlinks, carriage
returns, and non-printing C0 bytes cannot alter the terminal.

```go
func TestSanitizeRemoteTextRemovesTerminalControls(t *testing.T) {
    got := sanitizeRemoteText("safe\x1b[31mred\x1b[0m\rreplace\x07")
    if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\r') {
        t.Fatalf("unsafe output %q", got)
    }
}
```

- [ ] **Step 2: Run the focused tests and confirm failure**

Run: `go test ./internal/app -run 'View|Sanitize'`

Expected: FAIL because rendering and sanitization are missing.

- [ ] **Step 3: Implement sanitization**

Remove CSI and OSC sequences, map line-safe tabs/newlines intentionally, drop
other control characters, and leave printable Unicode intact. Sanitize channel
names, display names, status text derived from remote errors, and message
content.

- [ ] **Step 4: Implement the responsive view**

Use Bubble Tea v2 and Lip Gloss v2. Render an approximately 30/70 split with
minimum widths, a bordered selected pane, wrapped messages, a compact status
line, and a help overlay. Return a `tea.View` with `AltScreen = true` and a
stable window title.

- [ ] **Step 5: Verify Task 6**

Run: `gofmt -w internal/app && go test -race ./internal/app`

Expected: PASS.

- [ ] **Step 6: Commit if Git exists**

```bash
git add internal/app go.mod go.sum
git commit -m "feat: render the traQ terminal interface"
```

### Task 7: Composer, Posting, and Polling

**Files:**
- Modify: `internal/app/model.go`
- Modify: `internal/app/commands.go`
- Modify: `internal/app/update.go`
- Modify: `internal/app/view.go`
- Test: `internal/app/post_test.go`
- Test: `internal/app/poll_test.go`

**Interfaces:**
- Consumes: `traq.Service.Post`, `traq.Service.Messages`
- Produces internal typed messages `postSucceededMsg`, `postFailedMsg`, and
  `pollTickMsg`

- [ ] **Step 1: Write failing composer and posting tests**

Test `i` enters composer only with a selected channel, `esc` preserves no sent
side effect, `ctrl+s` rejects whitespace, one in-flight post suppresses
duplicates, failure retains the draft, success clears it and refreshes, and
`q` types normally inside the composer.

- [ ] **Step 2: Write failing polling tests**

Test one poll timer begins after channel selection, only the active channel is
requested, selecting another channel invalidates the old tick/result, equal
message windows do not jump scroll position, and polling errors retain data.

- [ ] **Step 3: Run focused tests and confirm failure**

Run: `go test ./internal/app -run 'Composer|Post|Poll'`

Expected: FAIL for the new behaviors.

- [ ] **Step 4: Implement composer and post commands**

Use the Bubbles v2 textarea. Bind `ctrl+s` to submit and `esc` to cancel.
Disable submit while a post is in flight. On success clear the draft, show a
short confirmation, and fetch the active channel immediately.

- [ ] **Step 5: Implement polling**

Use `tea.Tick(pollInterval, ...)`. Carry the channel ID and a monotonically
increasing generation in tick and result messages. Schedule the next tick after
handling the current tick; do not accumulate independent ticker goroutines.

- [ ] **Step 6: Verify Task 7**

Run: `gofmt -w internal/app && go test -race ./internal/app`

Expected: PASS.

- [ ] **Step 7: Commit if Git exists**

```bash
git add internal/app
git commit -m "feat: post and refresh channel messages"
```

### Task 8: CLI Wiring and Browser Fallback

**Files:**
- Create: `internal/auth/browser.go`
- Test: `internal/auth/browser_test.go`
- Create: `cmd/traq-tui/main.go`
- Test: `cmd/traq-tui/main_test.go`

**Interfaces:**
- Produces: `func OpenBrowser(url string) error`
- Produces: `func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int`

- [ ] **Step 1: Write failing CLI tests**

Test default startup wiring through injected factories, `--login`,
`--logout`, unknown arguments, configuration errors, browser-open failure
printing the authorization URL without secrets, and non-zero exit codes.

- [ ] **Step 2: Run focused tests and confirm failure**

Run: `go test ./cmd/traq-tui ./internal/auth -run 'CLI|Browser'`

Expected: FAIL because the command and opener are missing.

- [ ] **Step 3: Implement Linux browser opening**

Validate that the input is an `http` or `https` URL and start
`xdg-open <url>` without a shell. Return the start error so OAuth startup can
print a safe fallback URL. Do not log the callback code or token.

- [ ] **Step 4: Wire the command**

Load config, create the file token store and authenticator, handle logout
without starting the TUI, obtain a token, build the official API service, and
run Bubble Tea. Handle SIGINT through context cancellation and restore the
terminal on every exit path.

- [ ] **Step 5: Verify Task 8**

Run: `gofmt -w cmd internal && go test -race ./cmd/traq-tui ./internal/auth`

Expected: PASS.

- [ ] **Step 6: Commit if Git exists**

```bash
git add cmd internal/auth
git commit -m "feat: wire OAuth login into the TUI"
```

### Task 9: Documentation and Full Verification

**Files:**
- Create: `README.md`
- Modify: `.env.example`
- Modify: any source or test file required by verification findings

**Interfaces:**
- Consumes: all prior tasks
- Produces: buildable `./cmd/traq-tui` and reproducible setup instructions

- [ ] **Step 1: Write the README**

Document the product estimate, Linux requirement, OAuth client registration
with `read` and `write`, exact environment variables, first login, token file
permissions, `--login`, `--logout`, complete key map, API polling behavior,
MVP exclusions, troubleshooting for callback/401/network errors, and the
manual end-to-end check. Do not include a real host, client ID, secret, code,
or token supplied by the user.

- [ ] **Step 2: Format and inspect generated changes**

Run: `gofmt -w cmd internal`

Run: `gofmt -l .`

Expected: the second command prints nothing.

- [ ] **Step 3: Run all automated verification**

Run: `go test -race ./...`

Expected: PASS.

Run: `go vet ./...`

Expected: PASS with no diagnostics.

Run: `go build -o /tmp/traq-tui ./cmd/traq-tui`

Expected: PASS and `/tmp/traq-tui` exists.

- [ ] **Step 4: Perform the secret scan**

Run searches only inside the repository for PEM headers, private-key filenames,
OAuth bearer-token patterns, and accidental `.env`/token files. Do not print
the process environment or search outside the repository. Any detected fixture
must use an unmistakably fake value.

- [ ] **Step 5: Run the manual end-to-end acceptance check when the user is ready**

Ask the user to export the four required OAuth variables locally; never ask
them to paste values into chat. Run the binary, complete browser authorization,
select the user-designated test channel, read recent messages, post a unique
harmless message, and have the user confirm it in the official client.

Expected: the post appears under the authenticated user's identity and the TUI
refresh shows it exactly once.

- [ ] **Step 6: Commit if Git exists**

```bash
git add README.md .env.example .gitignore cmd internal go.mod go.sum
git commit -m "docs: finish the traQ TUI MVP"
```

- [ ] **Step 7: Report completion truthfully**

Summarize files, automated verification output, manual acceptance result, and
remaining MVP exclusions. If live OAuth credentials were not available, state
that implementation and mock verification are complete but live traQ
acceptance is still pending.

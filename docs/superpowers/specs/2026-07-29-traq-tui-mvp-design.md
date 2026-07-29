# traQ TUI MVP Design

## Purpose

Build a small unofficial traQ client that stays usable beside normal terminal
work. It is a personal client, not an attempt to reproduce the official web
application.

The MVP succeeds when a user can authenticate as themselves, browse channels,
read recent messages in a selected channel, and post a message to that channel.

## Product Estimate

The result will be a Linux-first Go binary named `traq-tui`.

On its first run, it will:

1. validate OAuth configuration supplied through environment variables;
2. start a short-lived callback server on the configured loopback URL;
3. open the traQ authorization page in the default browser, or print the URL
   when opening a browser fails;
4. exchange the returned authorization code for a token; and
5. save that token beneath `.traq-tui/` with owner-only permissions.

After login, the full-screen TUI will contain:

- a searchable channel pane on the left;
- a scrollable recent-message pane on the right;
- a one-line status/help area; and
- a multiline composer at the bottom while composing.

Keyboard interaction will be intentionally small: `j`/`k` or arrow keys move,
`enter` selects a channel, `/` filters channels, `i` opens the composer,
`ctrl+s` posts, `esc` cancels, `r` refreshes, `?` shows help, and `q` quits
outside text-entry mode. The active channel will refresh every five seconds.

Expected implementation size is roughly 1,500–2,500 lines of Go plus tests and
documentation. A focused engineer would normally need about one to two working
days for the implementation and mock-server verification, followed by a short
manual OAuth and posting check against the target traQ instance.

This MVP will not include WebSocket updates, unread management, stamps,
attachments, message editing/deletion, threads, search, direct messages,
desktop notifications, or full traQ Markdown rendering.

## Chosen Approach

Use Go 1.26 with Bubble Tea v2, Bubbles v2, and Lip Gloss v2.

This was selected over Rust/Ratatui and a TypeScript TUI because traQ maintains
an official Go API client and an OAuth endpoint helper. Go therefore minimizes
custom protocol code while still producing a small single binary. Bubble Tea's
message/update/view model also gives network results and terminal input a clear
single state transition path.

Dependencies must use current stable versions resolved when implementation
starts. Do not pin preview, release-candidate, or beta versions.

## Configuration and Secrets

The program reads:

- `TRAQ_API_BASE_URL` — required traQ v3 base URL, including `/api/v3`;
- `TRAQ_CLIENT_ID` — required OAuth client ID;
- `TRAQ_CLIENT_SECRET` — required OAuth client secret;
- `TRAQ_REDIRECT_URL` — required registered loopback callback URL;
- `TRAQ_TUI_TOKEN_FILE` — optional token path, default
  `.traq-tui/token.json`; and
- `TRAQ_TUI_POLL_INTERVAL` — optional Go duration, default `5s`.

OAuth uses Authorization Code Flow with `read` and `write` scopes. The
redirect URL must use `http` and a loopback host (`127.0.0.1`, `[::1]`, or
`localhost`). The callback must validate a cryptographically random `state`
value and time out after three minutes.

Never hard-code, print, log, or commit credentials or tokens. The token
directory is mode `0700`, the token file is mode `0600`, and updates are
atomic. `.traq-tui/` and `.env` are ignored by Git. The repository includes
only an `.env.example` with obviously fake sample values.

During implementation, write only inside this repository or `/tmp`. Reading
outside the repository is allowed only for ordinary documentation and
toolchain files. Do not inspect SSH keys, keyrings, credential stores, shell
history, broad environment dumps, or similarly sensitive locations.

## Architecture

### Configuration

`internal/config` parses and validates environment-backed configuration. It
does not read arbitrary dotenv files.

### Authentication

`internal/auth` owns OAuth login, loopback callback handling, browser opening,
token persistence, and token refresh persistence. Browser opening and token
storage are injected so tests do not launch programs or touch real secrets.

### traQ API Boundary

`internal/traq` wraps the official `github.com/traPtitech/go-traq` client behind
a narrow application interface. It maps generated SDK values into small local
`Channel`, `Message`, and `User` types and attaches the latest OAuth access
token to every request.

The application uses only these API operations:

- list channels;
- list users needed to display message authors;
- get the latest 50 messages for one channel; and
- post one plain-text message to one channel.

### TUI Application

`internal/app` owns Bubble Tea state and rendering. Network operations run as
Bubble Tea commands and return typed messages; `Update` remains the only place
that mutates application state. Only the selected channel is polled.

The UI must remain usable at 80×24. Below the minimum supported size it shows a
resize message instead of rendering broken panes. Message content is displayed
as wrapped plain text; ANSI/control sequences from remote data are sanitized.

## Data Flow

Startup loads configuration, obtains a stored or newly authorized token,
constructs the API adapter, then starts Bubble Tea. Initialization concurrently
loads channels and users. Selecting a channel fetches its recent messages.
Polling fetches the same recent window and replaces it without duplicating
messages. Posting disables duplicate submission, sends the content, clears the
composer only after success, and immediately refreshes the channel.

## Error Handling

Configuration and pre-TUI OAuth failures return concise actionable terminal
errors. Runtime API failures appear in the status line without destroying
already loaded data. A `401` instructs the user to run with `--login`; rate
limits and network failures retain the current screen and allow retry.

The binary supports:

- `traq-tui` — use a stored token or perform login, then start the TUI;
- `traq-tui --login` — force a new OAuth login before starting; and
- `traq-tui --logout` — remove only the configured token file and exit.

## Verification

Tests use fakes and `httptest.Server`; they never contact a real traQ instance
or open a browser. Required coverage includes configuration validation, OAuth
state and callback behavior, atomic token storage and permissions, API mapping
and errors, keyboard/focus state transitions, stale response rejection,
polling, and posting success/failure.

Automated gates are:

```bash
gofmt -l .
go test -race ./...
go vet ./...
go build ./cmd/traq-tui
```

The final manual check is performed only after the user supplies environment
variables locally: authorize in the browser, select a designated test channel,
verify recent messages, post a unique harmless test message, and confirm it in
the official client. The implementation must never request that a client
secret or token be pasted into chat.

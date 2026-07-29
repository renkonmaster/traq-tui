package traq

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

const fakeBearerToken = "fake-api-access-token"

func newServiceTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func requireFakeAuthorization(t *testing.T, request *http.Request) {
	t.Helper()
	if got := request.Header.Get("Authorization"); got != "Bearer "+fakeBearerToken {
		t.Fatalf("Authorization = %q", got)
	}
}

func newTestService(t *testing.T, handler http.Handler) Service {
	t.Helper()
	server := newServiceTestServer(t, handler)
	service, err := NewService(
		server.URL,
		oauth2.StaticTokenSource(&oauth2.Token{AccessToken: fakeBearerToken}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestServiceChannelsFlattensAndSortsPublicPaths(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requireFakeAuthorization(t, request)
		if request.Method != http.MethodGet || request.URL.Path != "/channels" {
			http.NotFound(w, request)
			return
		}
		if got := request.URL.Query().Get("include-dm"); got != "false" {
			t.Errorf("include-dm = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"public": [
				{"id":"child","parentId":"root","archived":false,"force":false,"topic":"","name":"dev","children":[]},
				{"id":"random","parentId":null,"archived":false,"force":false,"topic":"","name":"random","children":[]},
				{"id":"root","parentId":null,"archived":false,"force":false,"topic":"","name":"general","children":["child"]}
			],
			"dm": []
		}`)
	}))

	got, err := service.Channels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Channel{
		{ID: "root", Name: "general", Path: "general"},
		{ID: "child", Name: "dev", Path: "general/dev"},
		{ID: "random", Name: "random", Path: "random"},
	}
	if len(got) != len(want) {
		t.Fatalf("channels = %#v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("channel %d = %#v, want %#v", index, got[index], want[index])
		}
	}
}

func TestServiceChannelsHandlesMissingParentsAndCycles(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"public": [
				{"id":"orphan","parentId":"missing","archived":false,"force":false,"topic":"","name":"orphan","children":[]},
				{"id":"a","parentId":"b","archived":false,"force":false,"topic":"","name":"a","children":["b"]},
				{"id":"b","parentId":"a","archived":false,"force":false,"topic":"","name":"b","children":["a"]}
			]
		}`)
	}))

	got, err := service.Channels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("channels = %#v", got)
	}
	for _, channel := range got {
		if channel.Path == "" || strings.Count(channel.Path, "/") > 2 {
			t.Fatalf("unsafe derived path %q", channel.Path)
		}
	}
}

func TestServiceUsersMapsByID(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requireFakeAuthorization(t, request)
		if request.URL.Path != "/users" {
			http.NotFound(w, request)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[
			{"id":"user-1","name":"alice","displayName":"Alice","iconFileId":"icon-1","bot":false,"state":1,"updatedAt":"2026-07-29T01:00:00Z"},
			{"id":"user-2","name":"bob","displayName":"Bob","iconFileId":"icon-2","bot":false,"state":1,"updatedAt":"2026-07-29T01:01:00Z"}
		]`)
	}))

	got, err := service.Users(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("users = %#v", got)
	}
	if got["user-1"] != (User{ID: "user-1", Name: "alice", DisplayName: "Alice"}) {
		t.Fatalf("user-1 = %#v", got["user-1"])
	}
}

func TestServiceMessagesRequestsLimitAndReturnsOldestFirst(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requireFakeAuthorization(t, request)
		if request.URL.Path != "/channels/channel-1/messages" {
			http.NotFound(w, request)
			return
		}
		if got := request.URL.Query().Get("limit"); got != "50" {
			t.Errorf("limit = %q", got)
		}
		if got := request.URL.Query().Get("order"); got != "desc" {
			t.Errorf("order = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[
			{"id":"new","userId":"user-1","channelId":"channel-1","content":"second","createdAt":"2026-07-29T01:01:00Z","updatedAt":"2026-07-29T01:01:00Z","pinned":false,"stamps":[],"threadId":null},
			{"id":"old","userId":"user-1","channelId":"channel-1","content":"first","createdAt":"2026-07-29T01:00:00Z","updatedAt":"2026-07-29T01:00:00Z","pinned":false,"stamps":[],"threadId":null}
		]`)
	}))

	got, err := service.Messages(context.Background(), "channel-1", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "old" || got[1].ID != "new" {
		t.Fatalf("messages = %#v", got)
	}
	if got[0].Content != "first" ||
		got[0].UserID != "user-1" ||
		got[0].ChannelID != "channel-1" ||
		!got[0].CreatedAt.Equal(time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("oldest message = %#v", got[0])
	}
}

func TestServiceMessagesRejectsInvalidInputsWithoutRequest(t *testing.T) {
	requests := 0
	service := newTestService(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))

	for _, test := range []struct {
		channelID string
		limit     int
	}{
		{channelID: "", limit: 50},
		{channelID: "channel-1", limit: 0},
		{channelID: "channel-1", limit: 101},
	} {
		if _, err := service.Messages(context.Background(), test.channelID, test.limit); err == nil {
			t.Fatalf("accepted channel %q limit %d", test.channelID, test.limit)
		}
	}
	if requests != 0 {
		t.Fatalf("made %d requests for invalid inputs", requests)
	}
}

func TestServicePostSendsPlainTextAndMapsResponse(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requireFakeAuthorization(t, request)
		if request.Method != http.MethodPost || request.URL.Path != "/channels/channel-1/messages" {
			http.NotFound(w, request)
			return
		}
		var body struct {
			Content string `json:"content"`
			Embed   bool   `json:"embed"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Content != " hello traQ " || body.Embed {
			t.Fatalf("post body = %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{
			"id":"posted","userId":"user-1","channelId":"channel-1","content":" hello traQ ",
			"createdAt":"2026-07-29T01:02:00Z","updatedAt":"2026-07-29T01:02:00Z",
			"pinned":false,"stamps":[],"threadId":null
		}`)
	}))

	got, err := service.Post(context.Background(), "channel-1", " hello traQ ")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "posted" || got.Content != " hello traQ " || got.UserID != "user-1" {
		t.Fatalf("posted message = %#v", got)
	}
}

func TestServicePostRejectsBlankContentWithoutRequest(t *testing.T) {
	requests := 0
	service := newTestService(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))

	for _, content := range []string{"", " ", "\n\t"} {
		if _, err := service.Post(context.Background(), "channel-1", content); err == nil {
			t.Fatalf("accepted blank content %q", content)
		}
	}
	if _, err := service.Post(context.Background(), "", "message"); err == nil {
		t.Fatal("accepted blank channel ID")
	}
	if requests != 0 {
		t.Fatalf("made %d requests for invalid posts", requests)
	}
}

func TestServiceRedactsErrorResponsesAndExplainsUnauthorized(t *testing.T) {
	const responseSecret = "fake-sensitive-response-value"
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, responseSecret, http.StatusUnauthorized)
	}))

	_, err := service.Messages(context.Background(), "channel-1", 50)
	if err == nil {
		t.Fatal("expected API error")
	}
	if strings.Contains(err.Error(), responseSecret) || strings.Contains(err.Error(), fakeBearerToken) {
		t.Fatalf("error leaked sensitive data: %v", err)
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "--login") {
		t.Fatalf("unauthorized error is not actionable: %v", err)
	}
}

type rotatingTokenSource struct {
	mu     sync.Mutex
	tokens []string
}

func (s *rotatingTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.tokens) == 0 {
		return nil, errors.New("fake token source exhausted")
	}
	value := s.tokens[0]
	s.tokens = s.tokens[1:]
	return &oauth2.Token{AccessToken: value}, nil
}

func TestServiceUsesLatestTokenForEveryRequest(t *testing.T) {
	var authorizations []string
	server := newServiceTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		authorizations = append(authorizations, request.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"public":[]}`)
	}))
	service, err := NewService(server.URL, &rotatingTokenSource{
		tokens: []string{"fake-token-1", "fake-token-2"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Channels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Channels(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"Bearer fake-token-1", "Bearer fake-token-2"}
	if len(authorizations) != len(want) {
		t.Fatalf("authorizations = %#v", authorizations)
	}
	for index := range want {
		if authorizations[index] != want[index] {
			t.Fatalf("authorization %d = %q, want %q", index, authorizations[index], want[index])
		}
	}
}

func TestNewServiceRejectsUnsafeBaseURLsAndMissingTokenSource(t *testing.T) {
	for _, rawURL := range []string{
		"",
		"file:///tmp/traq",
		"https://user@example.test/api/v3",
		"https://example.test/api/v3?query=true",
	} {
		if _, err := NewService(rawURL, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "fake"})); err == nil {
			t.Fatalf("accepted base URL %q", rawURL)
		}
	}
	if _, err := NewService("https://example.test/api/v3", nil); err == nil {
		t.Fatal("accepted nil token source")
	}
}

func TestServiceEscapesChannelIDAsOnePathSegment(t *testing.T) {
	var escapedPath string
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		escapedPath = request.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	}))

	if _, err := service.Messages(context.Background(), "channel/with/slash", 50); err != nil {
		t.Fatal(err)
	}
	if escapedPath != "/channels/channel%2Fwith%2Fslash/messages" {
		t.Fatalf("escaped path = %q", escapedPath)
	}
}

func TestNewServiceAcceptsHTTPOnlyForLoopbackOrTestHosts(t *testing.T) {
	for _, rawURL := range []string{
		"http://127.0.0.1:3000/api/v3",
		"http://[::1]:3000/api/v3",
		"http://localhost:3000/api/v3",
	} {
		if _, err := NewService(rawURL, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "fake"})); err != nil {
			t.Fatalf("rejected %q: %v", rawURL, err)
		}
	}
	if _, err := NewService("http://example.test/api/v3", oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "fake"})); err == nil {
		t.Fatal("accepted non-loopback HTTP URL")
	}
}

func TestServiceAPIErrorDoesNotExposeRequestURLQuery(t *testing.T) {
	server := newServiceTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "fake body", http.StatusInternalServerError)
	}))
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(parsed.String(), oauth2.StaticTokenSource(&oauth2.Token{AccessToken: fakeBearerToken}))
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Messages(context.Background(), "channel-1", 50)
	if err == nil {
		t.Fatal("expected API error")
	}
	if strings.Contains(err.Error(), server.URL) {
		t.Fatalf("error exposed API URL: %v", err)
	}
}

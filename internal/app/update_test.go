package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	traqapi "traq-tui/internal/traq"
)

type fakeService struct {
	mu sync.Mutex

	channels    []traqapi.Channel
	users       map[string]traqapi.User
	messages    map[string][]traqapi.Message
	channelsErr error
	usersErr    error
	messagesErr error
	postErr     error

	channelCalls int
	userCalls    int
	messageCalls []string
	postCalls    []string
}

func (s *fakeService) Channels(context.Context) ([]traqapi.Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channelCalls++
	return append([]traqapi.Channel(nil), s.channels...), s.channelsErr
}

func (s *fakeService) Users(context.Context) (map[string]traqapi.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userCalls++
	result := make(map[string]traqapi.User, len(s.users))
	for id, user := range s.users {
		result[id] = user
	}
	return result, s.usersErr
}

func (s *fakeService) Messages(_ context.Context, channelID string, _ int) ([]traqapi.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messageCalls = append(s.messageCalls, channelID)
	return append([]traqapi.Message(nil), s.messages[channelID]...), s.messagesErr
}

func (s *fakeService) Post(_ context.Context, channelID, content string) (traqapi.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.postCalls = append(s.postCalls, channelID+":"+content)
	return traqapi.Message{}, s.postErr
}

func testService() *fakeService {
	return &fakeService{
		channels: []traqapi.Channel{
			{ID: "general", Name: "general", Path: "general"},
			{ID: "dev", Name: "dev", Path: "general/dev"},
			{ID: "random", Name: "random", Path: "random"},
		},
		users: map[string]traqapi.User{
			"user-1": {ID: "user-1", Name: "alice", DisplayName: "Alice"},
		},
		messages: map[string][]traqapi.Message{
			"general": {
				{ID: "message-1", ChannelID: "general", UserID: "user-1", Content: "hello"},
			},
			"dev": {
				{ID: "message-2", ChannelID: "dev", UserID: "user-1", Content: "develop"},
			},
		},
	}
}

func testModel(service traqapi.Service) Model {
	model := New(service, 5*time.Second)
	model.width = 80
	model.height = 24
	return model
}

func keyPress(text string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: []rune(text)[0], Text: text}
}

func specialKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code}
}

func updateModel(t *testing.T, model Model, message tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, command := model.Update(message)
	typed, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T", next)
	}
	return typed, command
}

func executeBatch(t *testing.T, command tea.Cmd) []tea.Msg {
	t.Helper()
	if command == nil {
		t.Fatal("expected command")
	}
	message := command()
	batch, ok := message.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{message}
	}
	results := make([]tea.Msg, 0, len(batch))
	for _, child := range batch {
		if child != nil {
			results = append(results, child())
		}
	}
	return results
}

func readyModel(t *testing.T) (Model, *fakeService) {
	t.Helper()
	service := testService()
	model := testModel(service)
	for _, message := range executeBatch(t, model.Init()) {
		model, _ = updateModel(t, model, message)
	}
	return model, service
}

func TestInitLoadsChannelsAndUsersThroughCommands(t *testing.T) {
	service := testService()
	model := testModel(service)

	results := executeBatch(t, model.Init())
	if len(results) != 2 {
		t.Fatalf("initial command produced %d messages", len(results))
	}
	for _, message := range results {
		model, _ = updateModel(t, model, message)
	}

	if len(model.channels) != 3 || len(model.filteredChannels) != 3 {
		t.Fatalf("channels = %#v", model.channels)
	}
	if model.users["user-1"].DisplayName != "Alice" {
		t.Fatalf("users = %#v", model.users)
	}
	if model.loadingChannels || model.loadingUsers {
		t.Fatal("initial loading flags were not cleared")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.channelCalls != 1 || service.userCalls != 1 {
		t.Fatalf("channel calls = %d, user calls = %d", service.channelCalls, service.userCalls)
	}
}

func TestChannelNavigationSupportsVimAndArrowKeysWithoutOverflow(t *testing.T) {
	model, _ := readyModel(t)

	for _, key := range []tea.KeyPressMsg{
		keyPress("j"),
		specialKey(tea.KeyDown),
		specialKey(tea.KeyDown),
	} {
		model, _ = updateModel(t, model, key)
	}
	if model.channelCursor != 2 {
		t.Fatalf("cursor = %d, want 2", model.channelCursor)
	}

	for _, key := range []tea.KeyPressMsg{
		keyPress("k"),
		specialKey(tea.KeyUp),
		specialKey(tea.KeyUp),
	} {
		model, _ = updateModel(t, model, key)
	}
	if model.channelCursor != 0 {
		t.Fatalf("cursor = %d, want 0", model.channelCursor)
	}
}

func TestEnterSelectsChannelAndLoadsMessages(t *testing.T) {
	model, service := readyModel(t)

	model, command := updateModel(t, model, specialKey(tea.KeyEnter))
	if model.selectedChannelID != "general" || !model.loadingMessages {
		t.Fatalf("selected = %q, loading = %v", model.selectedChannelID, model.loadingMessages)
	}
	if command == nil {
		t.Fatal("channel selection did not request messages")
	}
	model, _ = updateModel(t, model, command())

	if len(model.messages) != 1 || model.messages[0].ID != "message-1" {
		t.Fatalf("messages = %#v", model.messages)
	}
	if model.loadingMessages {
		t.Fatal("message loading flag was not cleared")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.messageCalls) != 1 || service.messageCalls[0] != "general" {
		t.Fatalf("message calls = %#v", service.messageCalls)
	}
}

func TestFilterNarrowsChannelsAndEnterSelectsResult(t *testing.T) {
	model, _ := readyModel(t)

	model, _ = updateModel(t, model, keyPress("/"))
	if model.focus != focusFilter || !model.filter.Focused() {
		t.Fatal("filter did not receive focus")
	}
	for _, letter := range []string{"d", "e", "v"} {
		model, _ = updateModel(t, model, keyPress(letter))
	}
	if len(model.filteredChannels) != 1 || model.filteredChannels[0].ID != "dev" {
		t.Fatalf("filtered channels = %#v", model.filteredChannels)
	}

	model, command := updateModel(t, model, specialKey(tea.KeyEnter))
	if model.focus != focusChannels || model.filter.Focused() {
		t.Fatal("filter focus was not released")
	}
	if model.selectedChannelID != "dev" || command == nil {
		t.Fatalf("selected = %q, command = %v", model.selectedChannelID, command != nil)
	}
}

func TestEscapeLeavesFilterAndRetainsQuery(t *testing.T) {
	model, _ := readyModel(t)
	model, _ = updateModel(t, model, keyPress("/"))
	model, _ = updateModel(t, model, keyPress("d"))
	model, _ = updateModel(t, model, specialKey(tea.KeyEscape))

	if model.focus != focusChannels || model.filter.Focused() {
		t.Fatal("escape did not leave filter")
	}
	if model.filter.Value() != "d" {
		t.Fatalf("filter value = %q", model.filter.Value())
	}
}

func TestManualRefreshReloadsOnlySelectedChannel(t *testing.T) {
	model, service := readyModel(t)
	model.selectedChannelID = "dev"
	service.messageCalls = nil

	model, command := updateModel(t, model, keyPress("r"))
	if command == nil || !model.loadingMessages {
		t.Fatal("refresh did not start loading")
	}
	model, _ = updateModel(t, model, command())
	if len(model.messages) != 1 || model.messages[0].ChannelID != "dev" {
		t.Fatalf("messages = %#v", model.messages)
	}
}

func TestStaleMessageResultIsIgnored(t *testing.T) {
	model, _ := readyModel(t)
	model.selectedChannelID = "dev"
	model.messages = []traqapi.Message{{ID: "current", ChannelID: "dev"}}

	model, _ = updateModel(t, model, messagesLoadedMsg{
		channelID: "general",
		messages:  []traqapi.Message{{ID: "stale", ChannelID: "general"}},
	})

	if len(model.messages) != 1 || model.messages[0].ID != "current" {
		t.Fatalf("stale response replaced messages: %#v", model.messages)
	}
}

func TestMessageFailureRetainsExistingMessages(t *testing.T) {
	model, service := readyModel(t)
	model.selectedChannelID = "general"
	model.messages = []traqapi.Message{{ID: "existing", ChannelID: "general"}}
	service.messagesErr = errors.New("fake network failure")

	model, command := updateModel(t, model, keyPress("r"))
	model, _ = updateModel(t, model, command())

	if len(model.messages) != 1 || model.messages[0].ID != "existing" {
		t.Fatalf("messages were cleared: %#v", model.messages)
	}
	if !model.hasError || model.status == "" || model.loadingMessages {
		t.Fatalf("error state = %v, status = %q, loading = %v", model.hasError, model.status, model.loadingMessages)
	}
}

func TestQQuitsOutsideInputButTypesInsideFilter(t *testing.T) {
	model, _ := readyModel(t)

	_, command := updateModel(t, model, keyPress("q"))
	if command == nil {
		t.Fatal("q did not return a quit command")
	}
	if _, ok := command().(tea.QuitMsg); !ok {
		t.Fatalf("q command returned %T", command())
	}

	model, _ = updateModel(t, model, keyPress("/"))
	model, command = updateModel(t, model, keyPress("q"))
	if model.filter.Value() != "q" {
		t.Fatalf("filter value = %q", model.filter.Value())
	}
}

func TestWindowSizeUpdatesModelDimensions(t *testing.T) {
	model := testModel(testService())

	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 120, Height: 40})

	if model.width != 120 || model.height != 40 {
		t.Fatalf("size = %dx%d", model.width, model.height)
	}
}

func TestFilterPreservesHighlightedChannelWhenItStillMatches(t *testing.T) {
	model, _ := readyModel(t)
	model.channelCursor = 1

	model, _ = updateModel(t, model, keyPress("/"))
	model, _ = updateModel(t, model, keyPress("g"))

	if model.filteredChannels[model.channelCursor].ID != "dev" {
		t.Fatalf("highlighted channel = %#v", model.filteredChannels[model.channelCursor])
	}
}

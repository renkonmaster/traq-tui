package app

import (
	"errors"
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	traqapi "traq-tui/internal/traq"
)

type recordedPollScheduler struct {
	delays []time.Duration
	ticks  []pollTickMsg
}

func (s *recordedPollScheduler) Tick(
	delay time.Duration,
	message func(time.Time) tea.Msg,
) tea.Cmd {
	tick, ok := message(time.Unix(0, 0)).(pollTickMsg)
	if !ok {
		panic("poll scheduler callback did not return pollTickMsg")
	}
	s.delays = append(s.delays, delay)
	s.ticks = append(s.ticks, tick)
	return func() tea.Msg { return tick }
}

func TestPollTimerStartsOnceWhenChannelIsSelected(t *testing.T) {
	model, _ := readyModel(t)
	scheduler := &recordedPollScheduler{}
	model.tick = scheduler.Tick

	model, command := updateModel(t, model, specialKey(tea.KeyEnter))

	if model.pollGeneration != 1 {
		t.Fatalf("poll generation = %d, want 1", model.pollGeneration)
	}
	if len(scheduler.ticks) != 1 {
		t.Fatalf("scheduled polls = %d, want 1", len(scheduler.ticks))
	}
	if scheduler.delays[0] != model.pollInterval {
		t.Fatalf("poll delay = %v, want %v", scheduler.delays[0], model.pollInterval)
	}
	if scheduler.ticks[0].channelID != "general" || scheduler.ticks[0].generation != 1 {
		t.Fatalf("scheduled tick = %#v", scheduler.ticks[0])
	}
	batch, ok := command().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("selection command = %T with %d children, want two-command batch", command(), len(batch))
	}
}

func TestPollTickRequestsOnlyActiveChannelAndSchedulesOneSuccessor(t *testing.T) {
	model, service := modelWithSelectedChannel(t)
	scheduler := &recordedPollScheduler{}
	model.tick = scheduler.Tick
	service.messageCalls = nil

	model, command := updateModel(t, model, pollTickMsg{
		channelID:  "general",
		generation: model.pollGeneration,
	})
	results := executeBatch(t, command)

	if len(scheduler.ticks) != 1 {
		t.Fatalf("successor polls = %d, want 1", len(scheduler.ticks))
	}
	service.mu.Lock()
	messageCalls := append([]string(nil), service.messageCalls...)
	service.mu.Unlock()
	if len(messageCalls) != 1 || messageCalls[0] != "general" {
		t.Fatalf("message calls = %#v", messageCalls)
	}
	loaded := false
	for _, result := range results {
		if message, ok := result.(messagesLoadedMsg); ok {
			loaded = true
			if message.channelID != "general" || message.generation != model.pollGeneration {
				t.Fatalf("poll result = %#v", message)
			}
		}
	}
	if !loaded {
		t.Fatalf("poll batch did not load messages: %#v", results)
	}

	_, staleCommand := updateModel(t, model, pollTickMsg{
		channelID:  "dev",
		generation: model.pollGeneration - 1,
	})
	if staleCommand != nil {
		t.Fatal("stale poll tick returned a command")
	}
	if len(scheduler.ticks) != 1 {
		t.Fatalf("stale tick scheduled another poll: %d", len(scheduler.ticks))
	}
}

func TestPollGenerationInvalidatesOldTickAndSameChannelResult(t *testing.T) {
	model, _ := readyModel(t)
	scheduler := &recordedPollScheduler{}
	model.tick = scheduler.Tick

	model, _ = updateModel(t, model, specialKey(tea.KeyEnter))
	oldGeneration := model.pollGeneration
	model.channelCursor = 1
	model, _ = updateModel(t, model, specialKey(tea.KeyEnter))
	if model.selectedChannelID != "dev" || model.pollGeneration <= oldGeneration {
		t.Fatalf("new selection = %q generation %d", model.selectedChannelID, model.pollGeneration)
	}

	_, staleTickCommand := updateModel(t, model, pollTickMsg{
		channelID:  "general",
		generation: oldGeneration,
	})
	if staleTickCommand != nil {
		t.Fatal("old channel tick returned a command")
	}

	model.messages = []traqapi.Message{{ID: "current", ChannelID: "dev"}}
	model, _ = updateModel(t, model, messagesLoadedMsg{
		channelID:  "dev",
		generation: oldGeneration,
		messages:   []traqapi.Message{{ID: "stale", ChannelID: "dev"}},
	})
	if len(model.messages) != 1 || model.messages[0].ID != "current" {
		t.Fatalf("old generation replaced messages: %#v", model.messages)
	}
}

func TestPollWithEqualWindowDoesNotJumpScrollPosition(t *testing.T) {
	model, _ := modelWithSelectedChannel(t)
	window := []traqapi.Message{
		{ID: "one", ChannelID: "general", Content: "one"},
		{ID: "two", ChannelID: "general", Content: "two"},
	}
	model.messages = append([]traqapi.Message(nil), window...)
	model.messageScroll = 7

	model, _ = updateModel(t, model, messagesLoadedMsg{
		channelID:  "general",
		generation: model.pollGeneration,
		messages:   append([]traqapi.Message(nil), window...),
	})

	if model.messageScroll != 7 {
		t.Fatalf("message scroll jumped to %d", model.messageScroll)
	}
	if !reflect.DeepEqual(model.messages, window) {
		t.Fatalf("messages changed: %#v", model.messages)
	}
}

func TestPollFailureRetainsExistingMessageWindow(t *testing.T) {
	model, service := modelWithSelectedChannel(t)
	scheduler := &recordedPollScheduler{}
	model.tick = scheduler.Tick
	service.messagesErr = errors.New("fake polling failure")
	model.messages = []traqapi.Message{{ID: "existing", ChannelID: "general"}}

	model, command := updateModel(t, model, pollTickMsg{
		channelID:  "general",
		generation: model.pollGeneration,
	})
	for _, result := range executeBatch(t, command) {
		if _, ok := result.(messagesFailedMsg); ok {
			model, _ = updateModel(t, model, result)
		}
	}

	if len(model.messages) != 1 || model.messages[0].ID != "existing" {
		t.Fatalf("poll error cleared messages: %#v", model.messages)
	}
	if !model.hasError || model.loadingMessages {
		t.Fatalf("poll error state: error=%v loading=%v", model.hasError, model.loadingMessages)
	}
}

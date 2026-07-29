package app

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func modelWithSelectedChannel(t *testing.T) (Model, *fakeService) {
	t.Helper()
	model, service := readyModel(t)
	model.selectedChannelID = "general"
	model.pollGeneration = 1
	model.focus = focusMessages
	return model, service
}

func TestComposerOpensOnlyForSelectedChannel(t *testing.T) {
	model, _ := readyModel(t)

	model, command := updateModel(t, model, keyPress("i"))
	if model.focus == focusComposer || model.composer.Focused() || command != nil {
		t.Fatalf("composer opened without a selected channel: focus=%v command=%v", model.focus, command != nil)
	}
	if !strings.Contains(model.status, "Select a channel") {
		t.Fatalf("missing channel guidance: %q", model.status)
	}

	model.selectedChannelID = "general"
	model, command = updateModel(t, model, keyPress("i"))
	if model.focus != focusComposer || !model.composer.Focused() || command == nil {
		t.Fatalf("composer did not open: focus=%v focused=%v command=%v", model.focus, model.composer.Focused(), command != nil)
	}
}

func TestComposerEscapeCancelsWithoutPostingAndPreservesDraft(t *testing.T) {
	model, service := modelWithSelectedChannel(t)
	model, _ = updateModel(t, model, keyPress("i"))
	for _, character := range []string{"d", "r", "a", "f", "t"} {
		model, _ = updateModel(t, model, keyPress(character))
	}

	model, command := updateModel(t, model, specialKey(tea.KeyEscape))

	if command != nil {
		t.Fatal("escape returned a side-effecting command")
	}
	if model.focus != focusMessages || model.composer.Focused() {
		t.Fatalf("composer remained focused: focus=%v focused=%v", model.focus, model.composer.Focused())
	}
	if model.composer.Value() != "draft" {
		t.Fatalf("draft = %q, want draft", model.composer.Value())
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.postCalls) != 0 {
		t.Fatalf("escape posted a message: %#v", service.postCalls)
	}
}

func TestComposerQTypesNormally(t *testing.T) {
	model, _ := modelWithSelectedChannel(t)
	model, _ = updateModel(t, model, keyPress("i"))

	model, command := updateModel(t, model, keyPress("q"))

	if model.composer.Value() != "q" {
		t.Fatalf("composer value = %q, want q", model.composer.Value())
	}
	if command != nil {
		if _, quit := command().(tea.QuitMsg); quit {
			t.Fatal("q quit while composer was focused")
		}
	}
}

func TestComposerViewShowsDraftAndSendHint(t *testing.T) {
	model, _ := modelWithSelectedChannel(t)
	model, _ = updateModel(t, model, keyPress("i"))
	model.composer.SetValue("hello from the terminal")

	_, plain := plainView(model)

	for _, expected := range []string{"Compose message", "hello from the terminal", "ctrl+s send"} {
		if !strings.Contains(plain, expected) {
			t.Errorf("composer view does not contain %q:\n%s", expected, plain)
		}
	}
	assertFitsTerminal(t, plain, model.width, model.height)
}

func TestPostRejectsWhitespaceOnlyDraft(t *testing.T) {
	model, service := modelWithSelectedChannel(t)
	model, _ = updateModel(t, model, keyPress("i"))
	model.composer.SetValue(" \n\t ")

	model, command := updateModel(t, model, controlKey('s'))

	if command != nil || model.posting {
		t.Fatalf("blank post started: command=%v posting=%v", command != nil, model.posting)
	}
	if !model.hasError || !strings.Contains(model.status, "empty") {
		t.Fatalf("blank post status = %q, error=%v", model.status, model.hasError)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.postCalls) != 0 {
		t.Fatalf("blank post reached service: %#v", service.postCalls)
	}
}

func TestPostSuppressesDuplicateWhileRequestIsInFlight(t *testing.T) {
	model, service := modelWithSelectedChannel(t)
	model, _ = updateModel(t, model, keyPress("i"))
	model.composer.SetValue("send once")

	model, firstCommand := updateModel(t, model, controlKey('s'))
	if firstCommand == nil || !model.posting {
		t.Fatal("first submission did not start")
	}
	model, duplicateCommand := updateModel(t, model, controlKey('s'))
	if duplicateCommand != nil {
		t.Fatal("duplicate submission returned a command")
	}

	result := firstCommand()
	service.mu.Lock()
	postCalls := append([]string(nil), service.postCalls...)
	service.mu.Unlock()
	if len(postCalls) != 1 || postCalls[0] != "general:send once" {
		t.Fatalf("post calls = %#v", postCalls)
	}
	if _, ok := result.(postSucceededMsg); !ok {
		t.Fatalf("post command returned %T", result)
	}
}

func TestPostFailureRetainsDraftForRetry(t *testing.T) {
	model, service := modelWithSelectedChannel(t)
	service.postErr = errors.New("fake post failure")
	model, _ = updateModel(t, model, keyPress("i"))
	model.composer.SetValue("please retry")

	model, command := updateModel(t, model, controlKey('s'))
	model, refreshCommand := updateModel(t, model, command())

	if refreshCommand != nil {
		t.Fatal("failed post requested a refresh")
	}
	if model.posting || !model.hasError || model.focus != focusComposer {
		t.Fatalf("post failure state: posting=%v error=%v focus=%v", model.posting, model.hasError, model.focus)
	}
	if model.composer.Value() != "please retry" || !model.composer.Focused() {
		t.Fatalf("draft was not retained: %q focused=%v", model.composer.Value(), model.composer.Focused())
	}
	if !strings.Contains(model.status, "Could not post") {
		t.Fatalf("failure status = %q", model.status)
	}
}

func TestPostSuccessClearsDraftAndRefreshesActiveChannel(t *testing.T) {
	model, service := modelWithSelectedChannel(t)
	model, _ = updateModel(t, model, keyPress("i"))
	model.composer.SetValue("  preserve formatting\n")

	model, command := updateModel(t, model, controlKey('s'))
	model, refreshCommand := updateModel(t, model, command())

	if refreshCommand == nil || !model.loadingMessages {
		t.Fatal("successful post did not request an immediate refresh")
	}
	if model.posting || model.hasError || model.focus != focusMessages {
		t.Fatalf("post success state: posting=%v error=%v focus=%v", model.posting, model.hasError, model.focus)
	}
	if model.composer.Value() != "" || model.composer.Focused() {
		t.Fatalf("composer was not cleared: %q focused=%v", model.composer.Value(), model.composer.Focused())
	}
	if !strings.Contains(model.status, "Posted") {
		t.Fatalf("success status = %q", model.status)
	}

	model, _ = updateModel(t, model, refreshCommand())
	service.mu.Lock()
	postCalls := append([]string(nil), service.postCalls...)
	messageCalls := append([]string(nil), service.messageCalls...)
	service.mu.Unlock()
	if len(postCalls) != 1 || postCalls[0] != "general:  preserve formatting\n" {
		t.Fatalf("post calls = %#v", postCalls)
	}
	if len(messageCalls) != 1 || messageCalls[0] != "general" {
		t.Fatalf("refresh calls = %#v", messageCalls)
	}
	if len(model.messages) != 1 || model.messages[0].ID != "message-1" {
		t.Fatalf("refreshed messages = %#v", model.messages)
	}
}

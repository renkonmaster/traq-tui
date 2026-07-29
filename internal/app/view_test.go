package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	traqapi "traq-tui/internal/traq"
)

func renderedModel() Model {
	service := testService()
	model := testModel(service)
	model.loadingChannels = false
	model.loadingUsers = false
	model.channels = append([]traqapi.Channel(nil), service.channels...)
	model.filteredChannels = append([]traqapi.Channel(nil), service.channels...)
	model.selectedChannelID = "general"
	model.users = service.users
	model.messages = []traqapi.Message{
		{
			ID:        "message-1",
			ChannelID: "general",
			UserID:    "user-1",
			Content:   "This message is deliberately long enough to wrap inside the message pane without losing its terminal control words.",
			CreatedAt: time.Date(2026, time.July, 29, 12, 34, 0, 0, time.Local),
		},
	}
	model.status = "Ready · 3 channels"
	return model
}

func plainView(model Model) (tea.View, string) {
	view := model.View()
	return view, ansi.Strip(view.Content)
}

func TestViewRendersUsable80x24Layout(t *testing.T) {
	model := renderedModel()

	view, plain := plainView(model)

	for _, expected := range []string{
		"Channels",
		"#general",
		"Alice",
		"12:34",
		"This message is deliberately",
		"inside the message pane",
		"control words.",
		"Ready · 3 channels",
	} {
		if !strings.Contains(plain, expected) {
			t.Errorf("view does not contain %q:\n%s", expected, plain)
		}
	}
	start := strings.Index(plain, "This message")
	end := strings.Index(plain, "control words.")
	if start < 0 || end < start || !strings.Contains(plain[start:end], "\n") {
		t.Errorf("long message was not wrapped:\n%s", plain)
	}
	if view.WindowTitle != "traq-tui" {
		t.Errorf("WindowTitle = %q, want traq-tui", view.WindowTitle)
	}
	if !view.AltScreen {
		t.Error("AltScreen is false")
	}
	assertFitsTerminal(t, plain, 80, 24)
}

func TestViewRequestsResizeBelowMinimumDimensions(t *testing.T) {
	model := renderedModel()
	model.width = 79
	model.height = 23

	_, plain := plainView(model)

	if !strings.Contains(plain, "Resize terminal") ||
		!strings.Contains(plain, "80×24") ||
		!strings.Contains(plain, "79×23") {
		t.Fatalf("resize guidance missing:\n%s", plain)
	}
	if strings.Contains(plain, "terminal control words") {
		t.Fatalf("normal panes rendered below minimum dimensions:\n%s", plain)
	}
}

func TestViewUsesShortUserIDWhenDirectoryEntryIsAbsent(t *testing.T) {
	model := renderedModel()
	model.users = nil
	model.messages[0].UserID = "1234567890abcdef"

	_, plain := plainView(model)

	if !strings.Contains(plain, "12345678") {
		t.Fatalf("short user ID missing:\n%s", plain)
	}
	if strings.Contains(plain, "1234567890abcdef") {
		t.Fatalf("full unknown user ID leaked into layout:\n%s", plain)
	}
}

func TestViewSanitizesEveryRemoteTextSurface(t *testing.T) {
	model := renderedModel()
	model.channels[0].Path = "gen\x1b]8;;https://attacker.invalid/channel\x1b\\eral\x1b]8;;\x1b\\\nforged"
	model.filteredChannels[0] = model.channels[0]
	model.users["user-1"] = traqapi.User{
		ID:          "user-1",
		DisplayName: "\x1b[31mAlice\x1b[0m\radmin",
	}
	model.messages[0].Content = "hello\x1b]8;;https://attacker.invalid/message\x1b\\world\x1b]8;;\x1b\\\a"
	model.status = "failed\rforged\x1b[2J"

	view, plain := plainView(model)

	if strings.Contains(view.Content, "attacker.invalid") {
		t.Fatalf("remote OSC data survived in rendered bytes: %q", view.Content)
	}
	if strings.ContainsAny(plain, "\r\a") {
		t.Fatalf("remote control byte survived: %q", plain)
	}
	for _, expected := range []string{"#general forged", "Aliceadmin", "helloworld", "failedforged"} {
		if !strings.Contains(plain, expected) {
			t.Errorf("sanitized label %q missing:\n%s", expected, plain)
		}
	}
}

func TestViewShowsHelpOverlay(t *testing.T) {
	model := renderedModel()
	model.focus = focusHelp

	_, plain := plainView(model)

	for _, expected := range []string{"Keyboard shortcuts", "filter channels", "quit"} {
		if !strings.Contains(plain, expected) {
			t.Errorf("help overlay does not contain %q:\n%s", expected, plain)
		}
	}
}

func TestMessagePaneSupportsLineScrolling(t *testing.T) {
	model := renderedModel()
	model.focus = focusMessages
	model.messages = make([]traqapi.Message, 30)
	for index := range model.messages {
		model.messages[index] = traqapi.Message{
			ID:        fmt.Sprintf("message-%02d", index),
			ChannelID: "general",
			UserID:    "user-1",
			Content:   fmt.Sprintf("message-%02d", index),
			CreatedAt: time.Date(2026, time.July, 29, 12, index%60, 0, 0, time.Local),
		}
	}

	_, atBottom := plainView(model)
	if !strings.Contains(atBottom, "message-29") || strings.Contains(atBottom, "message-00") {
		t.Fatalf("message pane did not start at latest messages:\n%s", atBottom)
	}

	model, _ = updateModel(t, model, keyPress("k"))
	if model.messageScroll != 1 {
		t.Fatalf("messageScroll after k = %d, want 1", model.messageScroll)
	}
	model, _ = updateModel(t, model, keyPress("j"))
	if model.messageScroll != 0 {
		t.Fatalf("messageScroll after j = %d, want 0", model.messageScroll)
	}
	model, _ = updateModel(t, model, keyPress("g"))
	if model.messageScroll == 0 {
		t.Fatal("g did not scroll to the oldest message")
	}
	_, atTop := plainView(model)
	if !strings.Contains(atTop, "message-00") || strings.Contains(atTop, "message-29") {
		t.Fatalf("message pane did not show oldest messages:\n%s", atTop)
	}
	model, _ = updateModel(t, model, keyPress("G"))
	if model.messageScroll != 0 {
		t.Fatalf("messageScroll after G = %d, want 0", model.messageScroll)
	}
}

func assertFitsTerminal(t *testing.T, content string, width, height int) {
	t.Helper()
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		t.Errorf("view height = %d, want <= %d", len(lines), height)
	}
	for index, line := range lines {
		if lineWidth := ansi.StringWidth(line); lineWidth > width {
			t.Errorf("line %d width = %d, want <= %d: %q", index, lineWidth, width, line)
		}
	}
}

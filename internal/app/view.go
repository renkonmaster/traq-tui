package app

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	minimumTerminalWidth  = 80
	minimumTerminalHeight = 24
)

var (
	accentColor = lipgloss.Color("#8b5cf6")
	mutedColor  = lipgloss.Color("#737373")
	errorColor  = lipgloss.Color("#ef4444")

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(accentColor)
	mutedStyle = lipgloss.NewStyle().
			Foreground(mutedColor)
	selectedChannelStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(accentColor)
)

// View renders the responsive traQ interface.
func (m Model) View() tea.View {
	var content string
	if m.width < minimumTerminalWidth || m.height < minimumTerminalHeight {
		content = m.resizeView()
	} else {
		content = m.applicationView()
	}

	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "traq-tui"
	return view
}

func (m Model) resizeView() string {
	message := fmt.Sprintf(
		"Resize terminal to at least %d×%d (current: %d×%d)",
		minimumTerminalWidth,
		minimumTerminalHeight,
		m.width,
		m.height,
	)
	width := max(m.width, 1)
	height := max(m.height, 1)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, message)
}

func (m Model) applicationView() string {
	leftWidth, rightWidth, bodyHeight := m.layoutDimensions()
	leftFocused := m.focus == focusChannels || m.focus == focusFilter
	rightFocused := m.focus == focusMessages || m.focus == focusComposer || m.focus == focusHelp

	left := paneStyle(leftFocused, leftWidth, bodyHeight).Render(
		m.channelPane(leftWidth-2, bodyHeight-2),
	)
	right := paneStyle(rightFocused, rightWidth, bodyHeight).Render(
		m.messagePane(rightWidth-2, bodyHeight-2),
	)

	statusStyle := lipgloss.NewStyle().Width(m.width)
	if m.hasError {
		statusStyle = statusStyle.Foreground(errorColor)
	} else {
		statusStyle = statusStyle.Foreground(mutedColor)
	}
	status := statusStyle.Render(
		ansi.Truncate(sanitizeRemoteLine(m.status), m.width, "…"),
	)
	hints := mutedStyle.Width(m.width).Render(
		ansi.Truncate(m.keyHints(), m.width, "…"),
	)

	return lipgloss.JoinVertical(
		lipgloss.Left,
		lipgloss.JoinHorizontal(lipgloss.Top, left, right),
		status,
		hints,
	)
}

func paneStyle(focused bool, width, height int) lipgloss.Style {
	borderColor := mutedColor
	if focused {
		borderColor = accentColor
	}
	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor)
}

func (m Model) layoutDimensions() (leftWidth, rightWidth, bodyHeight int) {
	leftWidth = m.width * 3 / 10
	leftWidth = max(leftWidth, 24)
	rightWidth = m.width - leftWidth
	if rightWidth < 40 {
		rightWidth = 40
		leftWidth = m.width - rightWidth
	}
	return leftWidth, rightWidth, m.height - 2
}

func (m Model) channelPane(width, height int) string {
	lines := []string{titleStyle.Render("Channels")}
	if m.focus == focusFilter {
		filterValue := sanitizeRemoteLine(m.filter.Value())
		lines = append(lines, ansi.Truncate("/ "+filterValue+"▌", width, "…"))
	} else {
		lines = append(lines, mutedStyle.Render("/ filter channels"))
	}

	available := max(height-len(lines), 0)
	if len(m.filteredChannels) == 0 {
		lines = append(lines, mutedStyle.Render("(no matching channels)"))
		return strings.Join(lines, "\n")
	}

	start := 0
	if m.channelCursor >= available && available > 0 {
		start = m.channelCursor - available + 1
	}
	end := min(start+available, len(m.filteredChannels))
	for index := start; index < end; index++ {
		channel := m.filteredChannels[index]
		cursor := "  "
		if index == m.channelCursor {
			cursor = "› "
		}
		selected := "  "
		if channel.ID == m.selectedChannelID {
			selected = "● "
		}
		line := cursor + selected + "#" + sanitizeRemoteLine(channel.Path)
		line = ansi.Truncate(line, width, "…")
		if index == m.channelCursor {
			line = selectedChannelStyle.Render(line)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m Model) messagePane(width, height int) string {
	if m.focus == focusHelp {
		return m.helpOverlay(width)
	}

	channel := m.selectedChannelName()
	if channel == "" {
		return strings.Join([]string{
			titleStyle.Render("Messages"),
			"",
			mutedStyle.Render("Select a channel to read its latest messages."),
		}, "\n")
	}

	lines := []string{titleStyle.Render(ansi.Truncate("#"+channel, width, "…")), ""}
	composerHeight := 0
	if m.focus == focusComposer || m.posting {
		composerHeight = 5
	}
	available := max(height-len(lines)-composerHeight, 0)
	messageLines, _ := m.visibleMessageLines(width, available)
	if len(messageLines) == 0 {
		switch {
		case m.loadingMessages:
			messageLines = []string{mutedStyle.Render("Loading messages…")}
		default:
			messageLines = []string{mutedStyle.Render("No messages yet.")}
		}
	}
	lines = append(lines, messageLines...)
	if composerHeight > 0 {
		lines = append(lines, m.composerLines(width)...)
	}
	return strings.Join(lines, "\n")
}

func (m Model) composerLines(width int) []string {
	state := "Compose message · ctrl+s send · esc close"
	if m.posting {
		state = "Posting message…"
	}
	lines := []string{
		mutedStyle.Render(strings.Repeat("─", max(width, 0))),
		titleStyle.Render(ansi.Truncate(state, width, "…")),
	}
	for line := range strings.SplitSeq(m.composer.View(), "\n") {
		lines = append(lines, ansi.Truncate(line, width, "…"))
	}
	for len(lines) < 5 {
		lines = append(lines, "")
	}
	if len(lines) > 5 {
		lines = lines[:5]
	}
	return lines
}

func (m Model) visibleMessageLines(width, height int) ([]string, int) {
	lines := m.allMessageLines(width)
	maxScroll := max(len(lines)-height, 0)
	scroll := min(max(m.messageScroll, 0), maxScroll)
	end := len(lines) - scroll
	start := max(end-height, 0)
	if start > end {
		start = end
	}
	return append([]string(nil), lines[start:end]...), maxScroll
}

func (m Model) allMessageLines(width int) []string {
	if width < 1 {
		return nil
	}

	var lines []string
	for _, message := range m.messages {
		author := m.messageAuthor(message.UserID)
		header := message.CreatedAt.Format("15:04") + " " + author
		lines = append(lines, titleStyle.Render(ansi.Truncate(header, width, "…")))

		contentWidth := max(width-2, 1)
		content := sanitizeRemoteText(message.Content)
		if content == "" {
			content = "(empty message)"
		}
		for line := range strings.SplitSeq(ansi.Wrap(content, contentWidth, ""), "\n") {
			lines = append(lines, "  "+ansi.Truncate(line, contentWidth, "…"))
		}
		lines = append(lines, "")
	}
	return lines
}

func (m Model) maxMessageScroll() int {
	if m.width < minimumTerminalWidth || m.height < minimumTerminalHeight {
		return 0
	}
	_, rightWidth, bodyHeight := m.layoutDimensions()
	messageWidth := rightWidth - 2
	messageHeight := bodyHeight - 2
	available := max(messageHeight-2, 0)
	_, maxScroll := m.visibleMessageLines(messageWidth, available)
	return maxScroll
}

func (m Model) selectedChannelName() string {
	if m.selectedChannelID == "" {
		return ""
	}
	for _, channel := range m.channels {
		if channel.ID == m.selectedChannelID {
			return sanitizeRemoteLine(channel.Path)
		}
	}
	return sanitizeRemoteLine(shortIdentifier(m.selectedChannelID))
}

func (m Model) messageAuthor(userID string) string {
	if user, exists := m.users[userID]; exists {
		switch {
		case user.DisplayName != "":
			return sanitizeRemoteLine(user.DisplayName)
		case user.Name != "":
			return sanitizeRemoteLine(user.Name)
		}
	}
	return sanitizeRemoteLine(shortIdentifier(userID))
}

func shortIdentifier(identifier string) string {
	if ansi.StringWidth(identifier) <= 8 {
		return identifier
	}
	return ansi.Truncate(identifier, 8, "")
}

func (m Model) helpOverlay(width int) string {
	lines := []string{
		titleStyle.Render("Keyboard shortcuts"),
		"",
		"j/k, ↑/↓  move or scroll",
		"g/G        oldest/latest",
		"enter      select channel",
		"tab        switch pane",
		"/          filter channels",
		"r          refresh messages",
		"?          close help",
		"q          quit",
	}
	for index, line := range lines {
		lines[index] = ansi.Truncate(line, width, "…")
	}
	return strings.Join(lines, "\n")
}

func (m Model) keyHints() string {
	switch m.focus {
	case focusFilter:
		return "type to filter · enter select · esc cancel"
	case focusMessages:
		return "j/k scroll · g/G oldest/latest · tab channels · r refresh · ? help · q quit"
	case focusComposer:
		return "ctrl+s send · enter newline · esc keep draft · ctrl+c quit"
	case focusHelp:
		return "? close help · q quit"
	default:
		return "j/k move · enter select · / filter · tab messages · r refresh · ? help · q quit"
	}
}

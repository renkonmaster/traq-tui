package app

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	traqapi "traq-tui/internal/traq"
)

// Update applies exactly one event to the application state.
func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.width = message.Width
		m.height = message.Height
		m.messageScroll = min(m.messageScroll, m.maxMessageScroll())
		return m, nil

	case channelsLoadedMsg:
		m.loadingChannels = false
		m.channels = append([]traqapi.Channel(nil), message.channels...)
		m.applyChannelFilter()
		m.updateReadyStatus()
		return m, nil

	case channelsFailedMsg:
		m.loadingChannels = false
		m.hasError = true
		m.status = "Could not load channels: " + message.err.Error()
		return m, nil

	case usersLoadedMsg:
		m.loadingUsers = false
		m.users = message.users
		if m.users == nil {
			m.users = make(map[string]traqapi.User)
		}
		m.updateReadyStatus()
		return m, nil

	case usersFailedMsg:
		m.loadingUsers = false
		m.hasError = true
		m.status = "Could not load users: " + message.err.Error()
		return m, nil

	case messagesLoadedMsg:
		if message.channelID != m.selectedChannelID {
			return m, nil
		}
		m.loadingMessages = false
		m.hasError = false
		m.messages = append([]traqapi.Message(nil), message.messages...)
		m.status = fmt.Sprintf("Loaded %d messages", len(m.messages))
		return m, nil

	case messagesFailedMsg:
		if message.channelID != m.selectedChannelID {
			return m, nil
		}
		m.loadingMessages = false
		m.hasError = true
		m.status = "Could not load messages: " + message.err.Error()
		return m, nil

	case tea.KeyPressMsg:
		return m.updateKey(message)
	}
	return m, nil
}

func (m Model) updateKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.focus == focusFilter {
		switch key.String() {
		case "esc":
			m.filter.Blur()
			m.focus = focusChannels
			return m, nil
		case "enter":
			m.filter.Blur()
			m.focus = focusChannels
			return m.selectHighlightedChannel()
		default:
			previousID := m.highlightedChannelID()
			var command tea.Cmd
			m.filter, command = m.filter.Update(key)
			m.applyChannelFilterKeeping(previousID)
			return m, command
		}
	}

	switch key.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "/":
		m.focus = focusFilter
		return m, m.filter.Focus()
	case "?":
		if m.focus == focusHelp {
			m.focus = focusChannels
		} else {
			m.focus = focusHelp
		}
		return m, nil
	case "tab":
		if m.focus == focusChannels && m.selectedChannelID != "" {
			m.focus = focusMessages
		} else {
			m.focus = focusChannels
		}
		return m, nil
	case "r":
		if m.selectedChannelID == "" {
			return m, nil
		}
		m.loadingMessages = true
		m.hasError = false
		m.status = "Refreshing messages…"
		return m, loadMessages(m.service, m.selectedChannelID)
	case "enter":
		if m.focus == focusChannels {
			return m.selectHighlightedChannel()
		}
	case "j", "down":
		if m.focus == focusChannels && m.channelCursor+1 < len(m.filteredChannels) {
			m.channelCursor++
		} else if m.focus == focusMessages && m.messageScroll > 0 {
			m.messageScroll--
		}
	case "k", "up":
		if m.focus == focusChannels && m.channelCursor > 0 {
			m.channelCursor--
		} else if m.focus == focusMessages {
			m.messageScroll = min(m.messageScroll+1, m.maxMessageScroll())
		}
	case "pgdown", "ctrl+d":
		if m.focus == focusMessages {
			m.messageScroll = max(m.messageScroll-m.messagePageSize(), 0)
		}
	case "pgup", "ctrl+u":
		if m.focus == focusMessages {
			m.messageScroll = min(m.messageScroll+m.messagePageSize(), m.maxMessageScroll())
		}
	case "g":
		if m.focus == focusMessages {
			m.messageScroll = m.maxMessageScroll()
		}
	case "G":
		if m.focus == focusMessages {
			m.messageScroll = 0
		}
	}
	return m, nil
}

func (m Model) selectHighlightedChannel() (tea.Model, tea.Cmd) {
	if len(m.filteredChannels) == 0 ||
		m.channelCursor < 0 ||
		m.channelCursor >= len(m.filteredChannels) {
		return m, nil
	}
	channel := m.filteredChannels[m.channelCursor]
	m.selectedChannelID = channel.ID
	m.messages = nil
	m.messageScroll = 0
	m.loadingMessages = true
	m.hasError = false
	m.status = "Loading #" + channel.Path + "…"
	return m, loadMessages(m.service, channel.ID)
}

func (m Model) messagePageSize() int {
	if m.height < minimumTerminalHeight {
		return 1
	}
	_, _, bodyHeight := m.layoutDimensions()
	return max(bodyHeight-6, 1)
}

func (m *Model) applyChannelFilter() {
	m.applyChannelFilterKeeping(m.highlightedChannelID())
}

func (m *Model) applyChannelFilterKeeping(preferredID string) {
	query := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	m.filteredChannels = m.filteredChannels[:0]
	for _, channel := range m.channels {
		if query == "" || strings.Contains(strings.ToLower(channel.Path), query) {
			m.filteredChannels = append(m.filteredChannels, channel)
		}
	}

	if len(m.filteredChannels) == 0 {
		m.channelCursor = 0
		return
	}
	for index, channel := range m.filteredChannels {
		if channel.ID == preferredID {
			m.channelCursor = index
			return
		}
	}
	if m.channelCursor >= len(m.filteredChannels) {
		m.channelCursor = len(m.filteredChannels) - 1
	}
	if m.channelCursor < 0 {
		m.channelCursor = 0
	}
}

func (m Model) highlightedChannelID() string {
	if m.channelCursor < 0 || m.channelCursor >= len(m.filteredChannels) {
		return ""
	}
	return m.filteredChannels[m.channelCursor].ID
}

func (m *Model) updateReadyStatus() {
	if m.hasError {
		return
	}
	switch {
	case m.loadingChannels && m.loadingUsers:
		m.status = "Loading channels and users…"
	case m.loadingChannels:
		m.status = "Loading channels…"
	case m.loadingUsers:
		m.status = "Loading users…"
	default:
		m.status = fmt.Sprintf("Ready · %d channels", len(m.channels))
	}
}

package app

import (
	"context"

	tea "charm.land/bubbletea/v2"

	traqapi "traq-tui/internal/traq"
)

type channelsLoadedMsg struct {
	channels []traqapi.Channel
}

type channelsFailedMsg struct {
	err error
}

type usersLoadedMsg struct {
	users map[string]traqapi.User
}

type usersFailedMsg struct {
	err error
}

type messagesLoadedMsg struct {
	channelID string
	messages  []traqapi.Message
}

type messagesFailedMsg struct {
	channelID string
	err       error
}

func loadChannels(service traqapi.Service) tea.Cmd {
	return func() tea.Msg {
		channels, err := service.Channels(context.Background())
		if err != nil {
			return channelsFailedMsg{err: err}
		}
		return channelsLoadedMsg{channels: channels}
	}
}

func loadUsers(service traqapi.Service) tea.Cmd {
	return func() tea.Msg {
		users, err := service.Users(context.Background())
		if err != nil {
			return usersFailedMsg{err: err}
		}
		return usersLoadedMsg{users: users}
	}
}

func loadMessages(service traqapi.Service, channelID string) tea.Cmd {
	return func() tea.Msg {
		messages, err := service.Messages(context.Background(), channelID, 50)
		if err != nil {
			return messagesFailedMsg{channelID: channelID, err: err}
		}
		return messagesLoadedMsg{channelID: channelID, messages: messages}
	}
}

package app

import (
	"context"
	"time"

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
	channelID  string
	generation uint64
	messages   []traqapi.Message
}

type messagesFailedMsg struct {
	channelID  string
	generation uint64
	err        error
}

type postSucceededMsg struct {
	channelID  string
	generation uint64
	message    traqapi.Message
}

type postFailedMsg struct {
	channelID  string
	generation uint64
	err        error
}

type pollTickMsg struct {
	channelID  string
	generation uint64
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

func loadMessages(service traqapi.Service, channelID string, generation uint64) tea.Cmd {
	return func() tea.Msg {
		messages, err := service.Messages(context.Background(), channelID, 50)
		if err != nil {
			return messagesFailedMsg{
				channelID:  channelID,
				generation: generation,
				err:        err,
			}
		}
		return messagesLoadedMsg{
			channelID:  channelID,
			generation: generation,
			messages:   messages,
		}
	}
}

func postMessage(
	service traqapi.Service,
	channelID string,
	generation uint64,
	content string,
) tea.Cmd {
	return func() tea.Msg {
		message, err := service.Post(context.Background(), channelID, content)
		if err != nil {
			return postFailedMsg{
				channelID:  channelID,
				generation: generation,
				err:        err,
			}
		}
		return postSucceededMsg{
			channelID:  channelID,
			generation: generation,
			message:    message,
		}
	}
}

func schedulePoll(
	tick tickFunc,
	interval time.Duration,
	channelID string,
	generation uint64,
) tea.Cmd {
	if tick == nil || interval <= 0 || channelID == "" {
		return nil
	}
	return tick(interval, func(time.Time) tea.Msg {
		return pollTickMsg{channelID: channelID, generation: generation}
	})
}

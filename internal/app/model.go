// Package app contains the Bubble Tea state machine and rendering.
package app

import (
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	traqapi "traq-tui/internal/traq"
)

type focusArea uint8

const (
	focusChannels focusArea = iota
	focusMessages
	focusFilter
	focusComposer
	focusHelp
)

// Model is the complete state of the traq-tui interface.
type Model struct {
	service      traqapi.Service
	pollInterval time.Duration

	width  int
	height int
	focus  focusArea

	channels          []traqapi.Channel
	filteredChannels  []traqapi.Channel
	channelCursor     int
	selectedChannelID string

	users    map[string]traqapi.User
	messages []traqapi.Message
	// messageScroll is the number of rendered lines above the latest view.
	messageScroll int

	filter textinput.Model

	loadingChannels bool
	loadingUsers    bool
	loadingMessages bool
	hasError        bool
	status          string
}

var _ tea.Model = Model{}

// New creates an unloaded TUI model.
func New(service traqapi.Service, pollInterval time.Duration) Model {
	filter := textinput.New()
	filter.Prompt = "/ "
	filter.Placeholder = "filter channels"
	filter.CharLimit = 128

	return Model{
		service:         service,
		pollInterval:    pollInterval,
		focus:           focusChannels,
		users:           make(map[string]traqapi.User),
		filter:          filter,
		loadingChannels: true,
		loadingUsers:    true,
		status:          "Loading channels and users…",
	}
}

// Init loads the channel and user directories concurrently.
func (m Model) Init() tea.Cmd {
	return tea.Batch(loadChannels(m.service), loadUsers(m.service))
}

// Package traq provides the narrow API boundary used by the TUI.
package traq

import (
	"context"
	"time"
)

// Channel is the channel information needed by the TUI.
type Channel struct {
	ID   string
	Name string
	Path string
}

// User is the user information needed to label messages.
type User struct {
	ID          string
	Name        string
	DisplayName string
}

// Message is a channel message rendered by the TUI.
type Message struct {
	ID        string
	ChannelID string
	UserID    string
	Content   string
	CreatedAt time.Time
}

// Service is the complete traQ API surface used by the MVP.
type Service interface {
	Channels(context.Context) ([]Channel, error)
	Users(context.Context) (map[string]User, error)
	Messages(context.Context, string, int) ([]Message, error)
	Post(context.Context, string, string) (Message, error)
}

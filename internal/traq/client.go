package traq

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"

	sdk "github.com/traPtitech/go-traq"
	"golang.org/x/oauth2"
)

const maximumMessageLimit = 100

type service struct {
	client *sdk.APIClient
	tokens oauth2.TokenSource
}

// NewService builds a Service using the official generated traQ client.
func NewService(apiBaseURL string, tokens oauth2.TokenSource) (Service, error) {
	if tokens == nil {
		return nil, errors.New("OAuth token source is required")
	}
	parsed, err := url.Parse(apiBaseURL)
	if err != nil || !validServiceBaseURL(parsed) {
		return nil, errors.New("traQ API base URL must be an https URL or a loopback http URL")
	}

	configuration := sdk.NewConfiguration()
	configuration.UserAgent = "traq-tui"
	configuration.Servers = sdk.ServerConfigurations{{
		URL:         strings.TrimRight(parsed.String(), "/"),
		Description: "configured traQ instance",
	}}

	return &service{
		client: sdk.NewAPIClient(configuration),
		tokens: tokens,
	}, nil
}

func validServiceBaseURL(parsed *url.URL) bool {
	if parsed == nil ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	if parsed.Scheme != "http" {
		return false
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *service) authenticatedContext(ctx context.Context) (context.Context, error) {
	token, err := s.tokens.Token()
	if err != nil {
		return nil, errors.New("obtain OAuth access token: token source failed")
	}
	if token == nil || token.AccessToken == "" {
		return nil, errors.New("obtain OAuth access token: empty token")
	}
	return context.WithValue(ctx, sdk.ContextAccessToken, token.AccessToken), nil
}

func (s *service) Channels(ctx context.Context) ([]Channel, error) {
	authenticated, err := s.authenticatedContext(ctx)
	if err != nil {
		return nil, err
	}
	result, response, err := s.client.ChannelAPI.
		GetChannels(authenticated).
		IncludeDm(false).
		Execute()
	if err != nil {
		return nil, serviceAPIError("list channels", response, err)
	}
	if result == nil {
		return nil, errors.New("list channels failed: empty response")
	}
	return flattenChannels(result.Public), nil
}

func flattenChannels(source []sdk.Channel) []Channel {
	byID := make(map[string]sdk.Channel, len(source))
	for _, channel := range source {
		byID[channel.Id] = channel
	}

	memo := make(map[string]string, len(source))
	visiting := make(map[string]bool, len(source))
	var pathFor func(string) string
	pathFor = func(id string) string {
		if path, ok := memo[id]; ok {
			return path
		}
		channel, ok := byID[id]
		if !ok {
			return ""
		}
		if visiting[id] {
			return channel.Name
		}

		visiting[id] = true
		path := channel.Name
		if parentID, present := channel.ParentId.Get(), channel.ParentId.IsSet(); present &&
			parentID != nil &&
			*parentID != "" &&
			*parentID != id {
			if parentPath := pathFor(*parentID); parentPath != "" {
				path = parentPath + "/" + channel.Name
			}
		}
		delete(visiting, id)
		memo[id] = path
		return path
	}

	channels := make([]Channel, 0, len(source))
	for _, channel := range source {
		channels = append(channels, Channel{
			ID:   channel.Id,
			Name: channel.Name,
			Path: pathFor(channel.Id),
		})
	}
	sort.Slice(channels, func(left, right int) bool {
		leftFolded := strings.ToLower(channels[left].Path)
		rightFolded := strings.ToLower(channels[right].Path)
		if leftFolded == rightFolded {
			if channels[left].Path == channels[right].Path {
				return channels[left].ID < channels[right].ID
			}
			return channels[left].Path < channels[right].Path
		}
		return leftFolded < rightFolded
	})
	return channels
}

func (s *service) Users(ctx context.Context) (map[string]User, error) {
	authenticated, err := s.authenticatedContext(ctx)
	if err != nil {
		return nil, err
	}
	result, response, err := s.client.UserAPI.GetUsers(authenticated).Execute()
	if err != nil {
		return nil, serviceAPIError("list users", response, err)
	}

	users := make(map[string]User, len(result))
	for _, user := range result {
		users[user.Id] = User{
			ID:          user.Id,
			Name:        user.Name,
			DisplayName: user.DisplayName,
		}
	}
	return users, nil
}

func (s *service) Messages(ctx context.Context, channelID string, limit int) ([]Message, error) {
	if strings.TrimSpace(channelID) == "" {
		return nil, errors.New("channel ID is required")
	}
	if limit <= 0 || limit > maximumMessageLimit {
		return nil, fmt.Errorf("message limit must be between 1 and %d", maximumMessageLimit)
	}
	authenticated, err := s.authenticatedContext(ctx)
	if err != nil {
		return nil, err
	}
	result, response, err := s.client.ChannelAPI.
		GetMessages(authenticated, channelID).
		Limit(int32(limit)).
		Order("desc").
		Execute()
	if err != nil {
		return nil, serviceAPIError("get channel messages", response, err)
	}

	messages := make([]Message, len(result))
	for index, message := range result {
		messages[len(result)-1-index] = mapMessage(message)
	}
	return messages, nil
}

func (s *service) Post(ctx context.Context, channelID, content string) (Message, error) {
	if strings.TrimSpace(channelID) == "" {
		return Message{}, errors.New("channel ID is required")
	}
	if strings.TrimSpace(content) == "" {
		return Message{}, errors.New("message content must not be blank")
	}
	authenticated, err := s.authenticatedContext(ctx)
	if err != nil {
		return Message{}, err
	}
	request := sdk.NewPostMessageRequest(content)
	result, response, err := s.client.ChannelAPI.
		PostMessage(authenticated, channelID).
		PostMessageRequest(*request).
		Execute()
	if err != nil {
		return Message{}, serviceAPIError("post channel message", response, err)
	}
	if result == nil {
		return Message{}, errors.New("post channel message failed: empty response")
	}
	return mapMessage(*result), nil
}

func mapMessage(message sdk.Message) Message {
	return Message{
		ID:        message.Id,
		ChannelID: message.ChannelId,
		UserID:    message.UserId,
		Content:   message.Content,
		CreatedAt: message.CreatedAt,
	}
}

func serviceAPIError(operation string, response *http.Response, err error) error {
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%s canceled: %w", operation, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s timed out: %w", operation, context.DeadlineExceeded)
	}
	if response == nil {
		return fmt.Errorf("%s failed: transport error", operation)
	}
	if response.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%s failed: HTTP 401; run traq-tui --login to authenticate again", operation)
	}
	return fmt.Errorf("%s failed: HTTP %d", operation, response.StatusCode)
}

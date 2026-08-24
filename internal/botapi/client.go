package botapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gotd/td/tg"
)

type Client struct {
	httpClient *http.Client
	baseURL    string
}

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username,omitempty"`
}

type MessageEntity struct {
	Type          string `json:"type"`
	Offset        int    `json:"offset"`
	Length        int    `json:"length"`
	URL           string `json:"url,omitempty"`
	Language      string `json:"language,omitempty"`
	CustomEmojiID string `json:"custom_emoji_id,omitempty"`
}

type apiResponse[T any] struct {
	OK          bool   `json:"ok"`
	Result      T      `json:"result"`
	Description string `json:"description"`
	ErrorCode   int    `json:"error_code"`
}

func New(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &Client{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    "https://api.telegram.org",
	}
}

func NewWithHTTPClient(client *http.Client, baseURL string) *Client {
	return &Client{httpClient: client, baseURL: baseURL}
}

func (c *Client) GetMe(ctx context.Context, token string) (User, error) {
	return call[User](ctx, c, token, "getMe", nil)
}

func (c *Client) SendMessage(ctx context.Context, token string, chatID int64, text string, entities []tg.MessageEntityClass) error {
	payload := struct {
		ChatID   int64           `json:"chat_id"`
		Text     string          `json:"text"`
		Entities []MessageEntity `json:"entities,omitempty"`
	}{
		ChatID:   chatID,
		Text:     text,
		Entities: ConvertEntities(entities),
	}
	_, err := call[json.RawMessage](ctx, c, token, "sendMessage", payload)
	return err
}

func call[T any](ctx context.Context, c *Client, token, method string, payload any) (T, error) {
	var zero T
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return zero, fmt.Errorf("encode Bot API %s request: %w", method, err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+token+"/"+method, body)
	if err != nil {
		return zero, fmt.Errorf("create Bot API %s request: %w", method, err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		// net/http wraps transport errors in url.Error, whose Error method includes
		// the full request URL (and therefore the bot token). Keep only its cause.
		var urlError *url.Error
		if errors.As(err, &urlError) {
			return zero, fmt.Errorf("call Bot API %s: %w", method, urlError.Err)
		}
		return zero, fmt.Errorf("call Bot API %s: request failed", method)
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, 1<<20)
	var decoded apiResponse[T]
	if err := json.NewDecoder(limited).Decode(&decoded); err != nil {
		return zero, fmt.Errorf("decode Bot API %s response (HTTP %d): %w", method, resp.StatusCode, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !decoded.OK {
		return zero, fmt.Errorf("Bot API %s failed (HTTP %d, code %d): %s", method, resp.StatusCode, decoded.ErrorCode, decoded.Description)
	}
	return decoded.Result, nil
}

func ConvertEntities(entities []tg.MessageEntityClass) []MessageEntity {
	converted := make([]MessageEntity, 0, len(entities))
	for _, entity := range entities {
		item := MessageEntity{Offset: entity.GetOffset(), Length: entity.GetLength()}
		switch e := entity.(type) {
		case *tg.MessageEntityMention:
			item.Type = "mention"
		case *tg.MessageEntityHashtag:
			item.Type = "hashtag"
		case *tg.MessageEntityBotCommand:
			item.Type = "bot_command"
		case *tg.MessageEntityURL:
			item.Type = "url"
		case *tg.MessageEntityEmail:
			item.Type = "email"
		case *tg.MessageEntityBold:
			item.Type = "bold"
		case *tg.MessageEntityItalic:
			item.Type = "italic"
		case *tg.MessageEntityCode:
			item.Type = "code"
		case *tg.MessageEntityPre:
			item.Type = "pre"
			item.Language = e.Language
		case *tg.MessageEntityTextURL:
			item.Type = "text_link"
			item.URL = e.URL
		case *tg.MessageEntityMentionName:
			item.Type = "text_link"
			item.URL = "tg://user?id=" + strconv.FormatInt(e.UserID, 10)
		case *tg.InputMessageEntityMentionName:
			// Input entities carry an InputUserClass rather than a stable bare ID.
			// They are not expected on incoming messages, so omit them safely.
			continue
		case *tg.MessageEntityPhone:
			item.Type = "phone_number"
		case *tg.MessageEntityCashtag:
			item.Type = "cashtag"
		case *tg.MessageEntityUnderline:
			item.Type = "underline"
		case *tg.MessageEntityStrike:
			item.Type = "strikethrough"
		case *tg.MessageEntitySpoiler:
			item.Type = "spoiler"
		case *tg.MessageEntityCustomEmoji:
			item.Type = "custom_emoji"
			item.CustomEmojiID = strconv.FormatInt(e.DocumentID, 10)
		case *tg.MessageEntityBlockquote:
			if e.Collapsed {
				item.Type = "expandable_blockquote"
			} else {
				item.Type = "blockquote"
			}
		default:
			continue
		}
		converted = append(converted, item)
	}
	return converted
}

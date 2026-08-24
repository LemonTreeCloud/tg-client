package telegram

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gotd/td/tg"
)

type AccountInfo struct {
	ID          int64  `json:"id"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name,omitempty"`
	Username    string `json:"username,omitempty"`
	Phone       string `json:"phone,omitempty"`
	DisplayName string `json:"display_name"`
	Premium     bool   `json:"premium"`
}

type DialogInfo struct {
	PeerInfo
	TopMessageID int          `json:"top_message_id,omitempty"`
	UnreadCount  int          `json:"unread_count"`
	Pinned       bool         `json:"pinned"`
	LastMessage  *MessageInfo `json:"last_message,omitempty"`
}

type MessageInfo struct {
	ID         int          `json:"id"`
	ChatID     int64        `json:"chat_id"`
	SenderID   int64        `json:"sender_id,omitempty"`
	SenderName string       `json:"sender_name,omitempty"`
	Text       string       `json:"text"`
	Date       string       `json:"date"`
	Outgoing   bool         `json:"outgoing"`
	MediaType  string       `json:"media_type,omitempty"`
	Entities   []EntityInfo `json:"entities,omitempty"`
	Buttons    []ButtonInfo `json:"buttons,omitempty"`
}

type EntityInfo struct {
	Type          string `json:"type"`
	Offset        int    `json:"offset"`
	Length        int    `json:"length"`
	URL           string `json:"url,omitempty"`
	UserID        int64  `json:"user_id,omitempty"`
	Language      string `json:"language,omitempty"`
	CustomEmojiID int64  `json:"custom_emoji_id,omitempty"`
}

type ButtonInfo struct {
	Text  string `json:"text"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

func MessageLink(peer PeerInfo, messageID int) string {
	if peer.Username != "" {
		return fmt.Sprintf("https://t.me/%s/%d", peer.Username, messageID)
	}
	if channelID, ok := ChatIDToChannelID(peer.ChatID); ok {
		return fmt.Sprintf("https://t.me/c/%d/%d", channelID, messageID)
	}
	if peer.Type == "private" || peer.Type == "bot" {
		query := url.Values{}
		query.Set("user_id", strconv.FormatInt(peer.PeerID, 10))
		query.Set("message_id", strconv.Itoa(messageID))
		return "tg://openmessage?" + query.Encode()
	}
	return "unavailable"
}

func EntitiesInfo(entities []tg.MessageEntityClass) []EntityInfo {
	result := make([]EntityInfo, 0, len(entities))
	for _, entity := range entities {
		item := EntityInfo{
			Type:   entity.TypeName(),
			Offset: entity.GetOffset(),
			Length: entity.GetLength(),
		}
		switch e := entity.(type) {
		case *tg.MessageEntityTextURL:
			item.URL = e.URL
		case *tg.MessageEntityMentionName:
			item.UserID = e.UserID
		case *tg.MessageEntityPre:
			item.Language = e.Language
		case *tg.MessageEntityCustomEmoji:
			item.CustomEmojiID = e.DocumentID
		}
		result = append(result, item)
	}
	return result
}

func ExtractButtons(markup tg.ReplyMarkupClass) []ButtonInfo {
	inline, ok := markup.(*tg.ReplyInlineMarkup)
	if !ok {
		return nil
	}
	var result []ButtonInfo
	for _, row := range inline.Rows {
		for _, button := range row.Buttons {
			var item ButtonInfo
			switch b := button.(type) {
			case *tg.KeyboardButtonURL:
				item = ButtonInfo{Text: b.Text, Type: "url", Value: b.URL}
			case *tg.KeyboardButtonURLAuth:
				item = ButtonInfo{Text: b.Text, Type: "url_auth", Value: b.URL}
			case *tg.KeyboardButtonCallback:
				item = ButtonInfo{Text: b.Text, Type: "callback_data", Value: printableBytes(b.Data)}
			case *tg.KeyboardButtonSwitchInline:
				item = ButtonInfo{Text: b.Text, Type: "switch_inline_query", Value: b.Query}
			case *tg.KeyboardButtonWebView:
				item = ButtonInfo{Text: b.Text, Type: "web_app", Value: b.URL}
			case *tg.KeyboardButtonSimpleWebView:
				item = ButtonInfo{Text: b.Text, Type: "web_app", Value: b.URL}
			case *tg.KeyboardButtonCopy:
				item = ButtonInfo{Text: b.Text, Type: "copy_text", Value: b.CopyText}
			default:
				continue
			}
			result = append(result, item)
		}
	}
	return result
}

func printableBytes(value []byte) string {
	if utf8.Valid(value) {
		return string(value)
	}
	return "base64:" + base64.StdEncoding.EncodeToString(value)
}

func FormatButtons(buttons []ButtonInfo) string {
	if len(buttons) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("Button parameters:\n")
	for _, button := range buttons {
		fmt.Fprintf(&builder, "- %s [%s]: %s\n", button.Text, button.Type, button.Value)
	}
	return strings.TrimSuffix(builder.String(), "\n")
}

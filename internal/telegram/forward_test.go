package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"tg-client/internal/config"
)

func TestRuleMatches(t *testing.T) {
	rule := config.AutoForwardRule{Match: []config.MatchCondition{
		{SenderUserID: 42, Keywords: []string{"ALERT"}},
		{Keywords: []string{"fallback"}},
	}}
	message := &tg.Message{FromID: &tg.PeerUser{UserID: 42}, Message: "an alert occurred"}
	if !RuleMatches(rule, message) {
		t.Fatal("expected first condition to match case-insensitively")
	}
	message.FromID = &tg.PeerUser{UserID: 7}
	if RuleMatches(rule, message) {
		t.Fatal("sender and keyword in one condition must both match")
	}
	message.Message = "use fallback"
	if !RuleMatches(rule, message) {
		t.Fatal("conditions should be ORed")
	}
}

func TestBuildForwardMessagePreservesEntitiesAndAddsMetadata(t *testing.T) {
	bold := &tg.MessageEntityBold{Offset: 0, Length: 4}
	message := &tg.Message{
		ID:       99,
		Message:  "😀bold",
		Entities: []tg.MessageEntityClass{bold},
		ReplyMarkup: &tg.ReplyInlineMarkup{Rows: []tg.KeyboardButtonRow{{
			Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{Text: "run", Data: []byte("id=7")}},
		}}},
	}
	peer := PeerInfo{ChatID: -1_001_583_113_861, PeerID: 1_583_113_861, Name: "Source", Type: "supergroup"}
	text, entities := BuildForwardMessage(message, peer, time.Date(2026, 8, 24, 15, 55, 0, 0, time.UTC))
	for _, part := range []string{"😀bold", "id=7", "Source", "https://t.me/c/1583113861/99", "2026-08-24 15:55:00"} {
		if !strings.Contains(text, part) {
			t.Fatalf("text does not contain %q: %s", part, text)
		}
	}
	if len(entities) != 2 || entities[0] != bold {
		t.Fatalf("original entities not preserved: %#v", entities)
	}
	linkEntity, ok := entities[1].(*tg.MessageEntityURL)
	if !ok || linkEntity.Offset <= utf16Length(message.Message) || linkEntity.Length == 0 {
		t.Fatalf("bad link entity: %#v", entities[1])
	}
}

func TestRuleMatchesAnonymousAdmin(t *testing.T) {
	rule := config.AutoForwardRule{Match: []config.MatchCondition{{AnonymousAdmin: true, Keywords: []string{"alert"}}}}
	for _, tc := range []struct {
		name   string
		sender tg.PeerClass
		peer   tg.PeerClass
		text   string
		post   bool
		want   bool
	}{
		{"anonymous with keyword", &tg.PeerChannel{ChannelID: 123}, &tg.PeerChannel{ChannelID: 123}, "ALERT now", false, true},
		{"anonymous without keyword", &tg.PeerChannel{ChannelID: 123}, &tg.PeerChannel{ChannelID: 123}, "hello", false, false},
		{"ordinary user", &tg.PeerUser{UserID: 42}, &tg.PeerChannel{ChannelID: 123}, "alert", false, false},
		{"other channel identity", &tg.PeerChannel{ChannelID: 456}, &tg.PeerChannel{ChannelID: 123}, "alert", false, false},
		{"broadcast post", &tg.PeerChannel{ChannelID: 123}, &tg.PeerChannel{ChannelID: 123}, "alert", true, false},
		{"missing sender", nil, &tg.PeerChannel{ChannelID: 123}, "alert", false, false},
		{"missing peer", &tg.PeerChannel{ChannelID: 123}, nil, "alert", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := &tg.Message{FromID: tc.sender, PeerID: tc.peer, Message: tc.text, Post: tc.post}
			if got := RuleMatches(rule, message); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

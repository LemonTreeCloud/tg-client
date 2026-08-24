package telegram

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestMessageFromShortPrivate(t *testing.T) {
	entity := &tg.MessageEntityBold{Offset: 0, Length: 5}
	message := messageFromShortPrivate(&tg.UpdateShortMessage{
		ID:       12,
		UserID:   42,
		Message:  "hello",
		Date:     100,
		Entities: []tg.MessageEntityClass{entity},
	})
	if message.ID != 12 || message.Message != "hello" || len(message.Entities) != 1 {
		t.Fatalf("unexpected message: %#v", message)
	}
	if got, ok := ChatIDFromPeer(message.PeerID); !ok || got != 42 {
		t.Fatalf("unexpected peer: %d, %v", got, ok)
	}
	if got := SenderUserID(message.FromID); got != 42 {
		t.Fatalf("unexpected sender: %d", got)
	}
}

func TestMessageFromShortChat(t *testing.T) {
	message := messageFromShortChat(&tg.UpdateShortChatMessage{
		ID:      13,
		FromID:  7,
		ChatID:  99,
		Message: "group message",
	})
	if got, ok := ChatIDFromPeer(message.PeerID); !ok || got != -99 {
		t.Fatalf("unexpected peer: %d, %v", got, ok)
	}
	if got := SenderUserID(message.FromID); got != 7 {
		t.Fatalf("unexpected sender: %d", got)
	}
}

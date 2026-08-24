package telegram

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestChatIDConversions(t *testing.T) {
	const channelID int64 = 2_377_083_049
	chatID := ChannelIDToChatID(channelID)
	if chatID != -1_002_377_083_049 {
		t.Fatalf("got %d", chatID)
	}
	got, ok := ChatIDToChannelID(chatID)
	if !ok || got != channelID {
		t.Fatalf("got %d, %v", got, ok)
	}
	if got, ok := ChatIDFromPeer(&tg.PeerChat{ChatID: 42}); !ok || got != -42 {
		t.Fatalf("basic group got %d, %v", got, ok)
	}
}

func TestMessageLink(t *testing.T) {
	peer := PeerInfo{ChatID: -1_001_583_113_861, PeerID: 1_583_113_861, Type: "supergroup"}
	if got := MessageLink(peer, 863750); got != "https://t.me/c/1583113861/863750" {
		t.Fatalf("got %q", got)
	}
}

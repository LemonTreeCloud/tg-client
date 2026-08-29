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

func TestPeerStoreDoesNotReplaceFullChannelWithMin(t *testing.T) {
	const (
		channelID = int64(1_232_079_473)
		fullHash  = int64(111)
		minHash   = int64(222)
	)
	store := NewPeerStore()
	full := &tg.Channel{ID: channelID, Title: "Full", Megagroup: true}
	full.SetAccessHash(fullHash)
	store.Update(nil, nil, map[int64]*tg.Channel{channelID: full})

	min := &tg.Channel{ID: channelID, Title: "Min", Megagroup: true, Min: true}
	min.SetAccessHash(minHash)
	store.Update(nil, nil, map[int64]*tg.Channel{channelID: min})

	peer, ok := store.InputPeer(ChannelIDToChatID(channelID))
	if !ok {
		t.Fatal("expected a cached input peer")
	}
	channelPeer, ok := peer.(*tg.InputPeerChannel)
	if !ok {
		t.Fatalf("expected InputPeerChannel, got %T", peer)
	}
	if channelPeer.AccessHash != fullHash {
		t.Fatalf("full access hash was replaced: got %d, want %d", channelPeer.AccessHash, fullHash)
	}
}

func TestPeerStoreDoesNotUseFirstMinChannelAsInputPeer(t *testing.T) {
	const channelID = int64(1_232_079_473)
	store := NewPeerStore()
	min := &tg.Channel{ID: channelID, Title: "Min", Megagroup: true, Min: true}
	min.SetAccessHash(222)
	store.Update(nil, nil, map[int64]*tg.Channel{channelID: min})

	if _, ok := store.InputPeer(ChannelIDToChatID(channelID)); ok {
		t.Fatal("a min channel must not be used as an input peer")
	}
	if info, ok := store.Get(ChannelIDToChatID(channelID)); !ok || info.Type != "supergroup" {
		t.Fatalf("expected min channel metadata to remain available, got %#v, %v", info, ok)
	}
}

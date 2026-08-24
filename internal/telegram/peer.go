package telegram

import (
	"fmt"
	"strings"
	"sync"

	"github.com/gotd/td/tg"
)

const channelChatIDPrefix int64 = 1_000_000_000_000

type PeerInfo struct {
	ChatID    int64  `json:"chat_id"`
	PeerID    int64  `json:"peer_id"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Username  string `json:"username,omitempty"`
	IsBot     bool   `json:"is_bot,omitempty"`
	inputPeer tg.InputPeerClass
}

type PeerStore struct {
	mu    sync.RWMutex
	peers map[int64]PeerInfo
}

func NewPeerStore() *PeerStore {
	return &PeerStore{peers: make(map[int64]PeerInfo)}
}

func (s *PeerStore) UpdateEntities(entities tg.Entities) {
	s.Update(entities.Users, entities.Chats, entities.Channels)
}

func (s *PeerStore) Update(users map[int64]*tg.User, chats map[int64]*tg.Chat, channels map[int64]*tg.Channel) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range users {
		if user == nil {
			continue
		}
		username, _ := user.GetUsername()
		kind := "private"
		if user.Bot {
			kind = "bot"
		}
		s.peers[user.ID] = PeerInfo{
			ChatID:    user.ID,
			PeerID:    user.ID,
			Type:      kind,
			Name:      userDisplayName(user),
			Username:  username,
			IsBot:     user.Bot,
			inputPeer: user.AsInputPeer(),
		}
	}
	for _, chat := range chats {
		if chat == nil {
			continue
		}
		chatID := -chat.ID
		s.peers[chatID] = PeerInfo{
			ChatID:    chatID,
			PeerID:    chat.ID,
			Type:      "group",
			Name:      chat.Title,
			inputPeer: chat.AsInputPeer(),
		}
	}
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		chatID := ChannelIDToChatID(channel.ID)
		kind := "channel"
		if channel.Megagroup {
			kind = "supergroup"
		}
		username, _ := channel.GetUsername()
		s.peers[chatID] = PeerInfo{
			ChatID:    chatID,
			PeerID:    channel.ID,
			Type:      kind,
			Name:      channel.Title,
			Username:  username,
			inputPeer: channel.AsInputPeer(),
		}
	}
}

func (s *PeerStore) Get(chatID int64) (PeerInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	peer, ok := s.peers[chatID]
	return peer, ok
}

func (s *PeerStore) InputPeer(chatID int64) (tg.InputPeerClass, bool) {
	peer, ok := s.Get(chatID)
	if !ok || peer.inputPeer == nil {
		return nil, false
	}
	return peer.inputPeer, true
}

func (s *PeerStore) All() []PeerInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]PeerInfo, 0, len(s.peers))
	for _, peer := range s.peers {
		result = append(result, peer)
	}
	return result
}

func userDisplayName(user *tg.User) string {
	name := strings.TrimSpace(strings.Join([]string{user.FirstName, user.LastName}, " "))
	if name != "" {
		return name
	}
	if username, ok := user.GetUsername(); ok && username != "" {
		return "@" + username
	}
	return fmt.Sprintf("User %d", user.ID)
}

func ChatIDFromPeer(peer tg.PeerClass) (int64, bool) {
	switch p := peer.(type) {
	case *tg.PeerUser:
		return p.UserID, true
	case *tg.PeerChat:
		return -p.ChatID, true
	case *tg.PeerChannel:
		return ChannelIDToChatID(p.ChannelID), true
	default:
		return 0, false
	}
}

func SenderUserID(peer tg.PeerClass) int64 {
	if user, ok := peer.(*tg.PeerUser); ok {
		return user.UserID
	}
	return 0
}

func ChannelIDToChatID(channelID int64) int64 {
	return -(channelChatIDPrefix + channelID)
}

func ChatIDToChannelID(chatID int64) (int64, bool) {
	if chatID > -channelChatIDPrefix {
		return 0, false
	}
	return -chatID - channelChatIDPrefix, true
}

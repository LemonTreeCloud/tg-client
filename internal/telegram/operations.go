package telegram

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/tg"
)

const (
	defaultDialogLimit  = 100
	maxDialogLimit      = 500
	defaultMessageLimit = 100
	maxMessageLimit     = 500
)

func (s *Service) Account(ctx context.Context) (AccountInfo, error) {
	if _, err := s.currentAPI(); err != nil {
		return AccountInfo{}, err
	}
	s.apiMu.RLock()
	self := s.self
	s.apiMu.RUnlock()
	if self == nil {
		return AccountInfo{}, errors.New("Telegram account is not authorized")
	}
	username, _ := self.GetUsername()
	phone, _ := self.GetPhone()
	return AccountInfo{
		ID:          self.ID,
		FirstName:   self.FirstName,
		LastName:    self.LastName,
		Username:    username,
		Phone:       phone,
		DisplayName: userDisplayName(self),
		Premium:     self.Premium,
	}, nil
}

func (s *Service) ListDialogs(ctx context.Context, limit int) ([]DialogInfo, error) {
	if limit <= 0 {
		limit = defaultDialogLimit
	}
	if limit > maxDialogLimit {
		return nil, fmt.Errorf("dialog limit must not exceed %d", maxDialogLimit)
	}
	return s.refreshDialogs(ctx, limit)
}

func (s *Service) refreshDialogs(ctx context.Context, limit int) ([]DialogInfo, error) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	api, err := s.currentAPI()
	if err != nil {
		return nil, err
	}
	batchSize := 100
	if limit > 0 && limit < batchSize {
		batchSize = limit
	}
	iterator := query.GetDialogs(api).BatchSize(batchSize).Iter()
	result := make([]DialogInfo, 0)
	for iterator.Next(ctx) {
		elem := iterator.Value()
		s.peers.Update(elem.Entities.Users(), elem.Entities.Chats(), elem.Entities.Channels())
		dialog, ok := elem.Dialog.(*tg.Dialog)
		if !ok {
			continue
		}
		chatID, ok := ChatIDFromPeer(dialog.Peer)
		if !ok {
			continue
		}
		peer, found := s.peers.Get(chatID)
		if !found {
			peer = PeerInfo{ChatID: chatID, Name: fmt.Sprintf("Chat %d", chatID), Type: "unknown", inputPeer: elem.Peer}
		}
		info := DialogInfo{
			PeerInfo:     peer,
			TopMessageID: dialog.TopMessage,
			UnreadCount:  dialog.UnreadCount,
			Pinned:       dialog.Pinned,
		}
		if message, ok := elem.Last.(*tg.Message); ok {
			last := s.messageInfo(message, chatID)
			info.LastMessage = &last
		}
		result = append(result, info)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	if err := iterator.Err(); err != nil {
		return nil, fmt.Errorf("list Telegram dialogs: %w", err)
	}
	return result, nil
}

func (s *Service) Dialog(ctx context.Context, chatID int64) (DialogInfo, error) {
	dialogs, err := s.refreshDialogs(ctx, 0)
	if err != nil {
		return DialogInfo{}, err
	}
	for _, dialog := range dialogs {
		if dialog.ChatID == chatID {
			return dialog, nil
		}
	}
	return DialogInfo{}, fmt.Errorf("dialog %d not found", chatID)
}

func (s *Service) RecentMessages(ctx context.Context, chatID int64, limit int) ([]MessageInfo, error) {
	if limit <= 0 {
		limit = defaultMessageLimit
	}
	if limit > maxMessageLimit {
		return nil, fmt.Errorf("message limit must not exceed %d", maxMessageLimit)
	}
	api, err := s.currentAPI()
	if err != nil {
		return nil, err
	}
	peer, err := s.resolveInputPeer(ctx, chatID)
	if err != nil {
		return nil, err
	}
	response, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:  peer,
		Limit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("get message history for chat %d: %w", chatID, err)
	}
	modified, ok := response.AsModified()
	if !ok {
		return nil, fmt.Errorf("unexpected message history response %T", response)
	}
	users := tg.UserClassArray(modified.GetUsers()).UserToMap()
	chatEntities := tg.ChatClassArray(modified.GetChats())
	s.peers.Update(users, chatEntities.ChatToMap(), chatEntities.ChannelToMap())

	messages := make([]MessageInfo, 0, len(modified.GetMessages()))
	for _, raw := range modified.GetMessages() {
		message, ok := raw.(*tg.Message)
		if !ok {
			continue
		}
		messages = append(messages, s.messageInfo(message, chatID))
	}
	return messages, nil
}

func (s *Service) SendMessage(ctx context.Context, chatID int64, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("message must not be empty")
	}
	return s.sendRichMessage(ctx, chatID, text, nil)
}

func (s *Service) sendRichMessage(ctx context.Context, chatID int64, text string, entities []tg.MessageEntityClass) error {
	api, err := s.currentAPI()
	if err != nil {
		return err
	}
	peer, err := s.resolveInputPeer(ctx, chatID)
	if err != nil {
		return err
	}
	randomID, err := secureRandomID()
	if err != nil {
		return err
	}
	_, err = api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer:     peer,
		Message:  text,
		RandomID: randomID,
		Entities: entities,
	})
	if err != nil {
		return fmt.Errorf("send message to chat %d: %w", chatID, err)
	}
	return nil
}

func (s *Service) resolveInputPeer(ctx context.Context, chatID int64) (tg.InputPeerClass, error) {
	s.apiMu.RLock()
	self := s.self
	s.apiMu.RUnlock()
	if self != nil && self.ID == chatID {
		return &tg.InputPeerSelf{}, nil
	}
	if peer, ok := s.peers.InputPeer(chatID); ok {
		return peer, nil
	}
	if _, err := s.refreshDialogs(ctx, 0); err != nil {
		return nil, err
	}
	if peer, ok := s.peers.InputPeer(chatID); ok {
		return peer, nil
	}
	return nil, fmt.Errorf("chat %d is not present in the account dialog list", chatID)
}

func (s *Service) messageInfo(message *tg.Message, fallbackChatID int64) MessageInfo {
	chatID, ok := ChatIDFromPeer(message.PeerID)
	if !ok {
		chatID = fallbackChatID
	}
	senderID := int64(0)
	senderName := ""
	if id, ok := ChatIDFromPeer(message.FromID); ok {
		senderID = id
		if sender, found := s.peers.Get(id); found {
			senderName = sender.Name
		}
	}
	mediaType := ""
	if media, ok := message.GetMedia(); ok && media != nil {
		mediaType = media.TypeName()
	}
	return MessageInfo{
		ID:         message.ID,
		ChatID:     chatID,
		SenderID:   senderID,
		SenderName: senderName,
		Text:       message.Message,
		Date:       time.Unix(int64(message.Date), 0).In(s.cfg.Location).Format("2006-01-02 15:04:05"),
		Outgoing:   message.Out,
		MediaType:  mediaType,
		Entities:   EntitiesInfo(message.Entities),
		Buttons:    ExtractButtons(message.ReplyMarkup),
	}
}

func secureRandomID() (int64, error) {
	var buffer [8]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return 0, fmt.Errorf("generate Telegram random_id: %w", err)
	}
	value := int64(binary.LittleEndian.Uint64(buffer[:]))
	if value == 0 {
		value = 1
	}
	return value, nil
}

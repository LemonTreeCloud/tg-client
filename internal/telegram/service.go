package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gotd/log/logslog"
	"github.com/gotd/td/session"
	gotdtelegram "github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"

	"tg-client/internal/botapi"
	"tg-client/internal/config"
)

type Service struct {
	cfg        *config.Config
	logger     *slog.Logger
	auth       *AuthBroker
	peers      *PeerStore
	bot        *botapi.Client
	dispatcher tg.UpdateDispatcher
	rules      map[int64][]config.AutoForwardRule
	recentBot  *RecentBotMessages

	apiMu     sync.RWMutex
	api       *tg.Client
	self      *tg.User
	refreshMu sync.Mutex

	botMu  sync.RWMutex
	botIDs map[int64]struct{}

	ready     atomic.Bool
	connected atomic.Int32
	readyCh   chan struct{}
	readyOnce sync.Once
}

func NewService(cfg *config.Config, logger *slog.Logger) *Service {
	dispatcher := tg.NewUpdateDispatcher()
	service := &Service{
		cfg:        cfg,
		logger:     logger,
		auth:       NewAuthBroker(),
		peers:      NewPeerStore(),
		bot:        botapi.New(15 * time.Second),
		dispatcher: dispatcher,
		rules:      make(map[int64][]config.AutoForwardRule),
		recentBot:  NewRecentBotMessages(30 * time.Second),
		botIDs:     make(map[int64]struct{}),
		readyCh:    make(chan struct{}),
	}
	for _, rule := range cfg.AutoForwardRules {
		service.rules[rule.SourceChatID] = append(service.rules[rule.SourceChatID], rule)
	}
	dispatcher.OnNewMessage(service.onNewMessage)
	dispatcher.OnNewChannelMessage(service.onNewChannelMessage)
	service.dispatcher = dispatcher
	return service
}

func (s *Service) Auth() *AuthBroker {
	return s.auth
}

func (s *Service) Ready() <-chan struct{} {
	return s.readyCh
}

func (s *Service) IsReady() bool {
	return s.ready.Load()
}

func (s *Service) ConnectionState() string {
	return gotdtelegram.ConnectionState(s.connected.Load()).String()
}

func (s *Service) Run(ctx context.Context) (runErr error) {
	if err := os.MkdirAll(filepath.Dir(s.cfg.Telegram.SessionPath), 0o700); err != nil {
		return fmt.Errorf("create session directory: %w", err)
	}
	options := gotdtelegram.Options{
		SessionStorage: &session.FileStorage{Path: s.cfg.Telegram.SessionPath},
		UpdateHandler:  gotdtelegram.UpdateHandlerFunc(s.handleUpdates),
		Logger:         logslog.New(s.logger),
		OnConnectionState: func(state gotdtelegram.ConnectionState) {
			s.connected.Store(int32(state))
			s.logger.Debug("Telegram connection state changed", "state", state.String())
		},
	}
	client := gotdtelegram.NewClient(s.cfg.Telegram.ClientID, s.cfg.Telegram.ClientHash, options)
	defer func() {
		s.ready.Store(false)
		s.apiMu.Lock()
		s.api = nil
		s.apiMu.Unlock()
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			s.auth.SetError(runErr)
		}
	}()

	runErr = client.Run(ctx, func(runCtx context.Context) error {
		s.apiMu.Lock()
		s.api = client.API()
		s.apiMu.Unlock()

		self, err := s.ensureAuthorized(runCtx, client.Auth())
		if err != nil {
			return fmt.Errorf("authorize Telegram client: %w", err)
		}
		s.apiMu.Lock()
		s.self = self
		s.apiMu.Unlock()
		s.auth.SetAuthorized(self.ID)
		s.peers.Update(map[int64]*tg.User{self.ID: self}, nil, nil)

		if _, err := s.refreshDialogs(runCtx, 0); err != nil {
			s.logger.Warn("initial dialog refresh failed; API calls will retry", "error", err)
		}
		s.loadConfiguredBotIDs(runCtx)
		s.ready.Store(true)
		s.readyOnce.Do(func() { close(s.readyCh) })
		s.logger.Info("Telegram account is ready", "user_id", self.ID, "name", userDisplayName(self))

		if s.cfg.Telegram.KeepOnline {
			go s.keepOnline(runCtx)
		}
		<-runCtx.Done()
		return runCtx.Err()
	})
	return runErr
}

func (s *Service) ensureAuthorized(ctx context.Context, client *auth.Client) (*tg.User, error) {
	status, err := client.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("get authorization status: %w", err)
	}
	if status.Authorized {
		return status.User, nil
	}

	s.auth.SetSendingCode()
	sent, err := client.SendCode(ctx, s.cfg.Telegram.PhoneNumber, auth.SendCodeOptions{})
	if err != nil {
		return nil, fmt.Errorf("send verification code: %w", err)
	}
	sentCode, ok := sent.(*tg.AuthSentCode)
	if !ok {
		if _, success := sent.(*tg.AuthSentCodeSuccess); success {
			status, err = client.Status(ctx)
			if err != nil || !status.Authorized {
				return nil, fmt.Errorf("authorization succeeded without an account: %w", err)
			}
			return status.User, nil
		}
		return nil, fmt.Errorf("unsupported verification response %T", sent)
	}

	phoneCodeHash := sentCode.PhoneCodeHash
	s.logger.Info("Telegram verification code is required; submit it on stdin or POST /api/v1/auth/code")
	s.auth.SetWaitingCode(nil)
	for {
		request, err := s.auth.nextCode(ctx)
		if err != nil {
			return nil, err
		}
		_, signInErr := client.SignIn(ctx, s.cfg.Telegram.PhoneNumber, request.code, phoneCodeHash)
		if errors.Is(signInErr, auth.ErrPasswordAuthNeeded) {
			if s.cfg.Telegram.TwoFactorPassword == "" {
				signInErr = errors.New("Telegram two-step verification is enabled but telegram.two_factor_password is not configured")
			} else {
				_, signInErr = client.Password(ctx, s.cfg.Telegram.TwoFactorPassword)
			}
		}
		if signInErr != nil {
			if strings.Contains(signInErr.Error(), "PHONE_CODE_EXPIRED") {
				resent, resendErr := client.ResendCode(ctx, s.cfg.Telegram.PhoneNumber, phoneCodeHash)
				if resendErr == nil {
					if code, ok := resent.(*tg.AuthSentCode); ok {
						phoneCodeHash = code.PhoneCodeHash
					}
				} else {
					s.logger.Warn("failed to resend expired Telegram verification code", "error", resendErr)
				}
			}
			s.logger.Warn("Telegram verification code was rejected", "error", signInErr)
			s.auth.complete(request, signInErr, 0)
			continue
		}

		status, err = client.Status(ctx)
		if err != nil || !status.Authorized || status.User == nil {
			if err == nil {
				err = errors.New("Telegram did not return an authorized user")
			}
			s.auth.complete(request, err, 0)
			continue
		}
		s.auth.complete(request, nil, status.User.ID)
		return status.User, nil
	}
}

func (s *Service) keepOnline(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			api, err := s.currentAPI()
			if err != nil {
				continue
			}
			if _, err := api.AccountUpdateStatus(ctx, false); err != nil && !errors.Is(err, context.Canceled) {
				s.logger.Warn("failed to refresh Telegram online status", "error", err)
			} else {
				s.logger.Debug("refreshed Telegram online status")
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Service) loadConfiguredBotIDs(ctx context.Context) {
	seen := make(map[string]struct{})
	for _, rule := range s.cfg.AutoForwardRules {
		if rule.ForwardVia != config.ForwardViaBot {
			continue
		}
		if _, ok := seen[rule.BotToken]; ok {
			continue
		}
		seen[rule.BotToken] = struct{}{}
		user, err := s.bot.GetMe(ctx, rule.BotToken)
		if err != nil {
			s.logger.Warn("failed to identify configured forwarding bot", "rule_id", rule.ID, "error", err)
			continue
		}
		s.botMu.Lock()
		s.botIDs[user.ID] = struct{}{}
		s.botMu.Unlock()
	}
}

func (s *Service) currentAPI() (*tg.Client, error) {
	s.apiMu.RLock()
	defer s.apiMu.RUnlock()
	if s.api == nil {
		return nil, errors.New("Telegram client is not connected")
	}
	return s.api, nil
}

func (s *Service) handleUpdates(ctx context.Context, updates tg.UpdatesClass) error {
	switch update := updates.(type) {
	case *tg.UpdateShortMessage:
		return s.handleUpdateMessage(ctx, tg.Entities{}, messageFromShortPrivate(update))
	case *tg.UpdateShortChatMessage:
		return s.handleUpdateMessage(ctx, tg.Entities{}, messageFromShortChat(update))
	case *tg.UpdateShortSentMessage:
		// This is only an acknowledgement for an outgoing message. Outgoing
		// traffic is deliberately excluded from automatic forwarding.
		return nil
	case *tg.UpdatesTooLong:
		s.logger.Warn("Telegram reported an update gap; live rules resume with the next complete update")
		return nil
	default:
		return s.dispatcher.Handle(ctx, updates)
	}
}

func messageFromShortPrivate(update *tg.UpdateShortMessage) *tg.Message {
	peer := &tg.PeerUser{UserID: update.UserID}
	return &tg.Message{
		Out:         update.Out,
		Mentioned:   update.Mentioned,
		MediaUnread: update.MediaUnread,
		Silent:      update.Silent,
		ID:          update.ID,
		FromID:      peer,
		PeerID:      peer,
		FwdFrom:     update.FwdFrom,
		ViaBotID:    update.ViaBotID,
		ReplyTo:     update.ReplyTo,
		Date:        update.Date,
		Message:     update.Message,
		Entities:    update.Entities,
		TTLPeriod:   update.TTLPeriod,
	}
}

func messageFromShortChat(update *tg.UpdateShortChatMessage) *tg.Message {
	return &tg.Message{
		Out:         update.Out,
		Mentioned:   update.Mentioned,
		MediaUnread: update.MediaUnread,
		Silent:      update.Silent,
		ID:          update.ID,
		FromID:      &tg.PeerUser{UserID: update.FromID},
		PeerID:      &tg.PeerChat{ChatID: update.ChatID},
		FwdFrom:     update.FwdFrom,
		ViaBotID:    update.ViaBotID,
		ReplyTo:     update.ReplyTo,
		Date:        update.Date,
		Message:     update.Message,
		Entities:    update.Entities,
		TTLPeriod:   update.TTLPeriod,
	}
}

func (s *Service) onNewMessage(ctx context.Context, entities tg.Entities, update *tg.UpdateNewMessage) error {
	return s.handleUpdateMessage(ctx, entities, update.Message)
}

func (s *Service) onNewChannelMessage(ctx context.Context, entities tg.Entities, update *tg.UpdateNewChannelMessage) error {
	return s.handleUpdateMessage(ctx, entities, update.Message)
}

func (s *Service) handleUpdateMessage(ctx context.Context, entities tg.Entities, raw tg.MessageClass) error {
	s.peers.UpdateEntities(entities)
	message, ok := raw.(*tg.Message)
	if !ok || message.Out {
		return nil
	}
	chatID, ok := ChatIDFromPeer(message.PeerID)
	if !ok {
		return nil
	}
	rules := s.rules[chatID]
	if len(rules) == 0 {
		return nil
	}
	senderID := SenderUserID(message.FromID)
	if s.isOwnOrConfiguredBot(senderID) || s.recentBot.Seen(chatID, message.Message) {
		return nil
	}
	if message.ViaBotID != 0 && s.isOwnOrConfiguredBot(message.ViaBotID) {
		return nil
	}
	source, found := s.peers.Get(chatID)
	if !found {
		if _, err := s.refreshDialogs(ctx, 0); err != nil {
			s.logger.Warn("could not refresh source dialog metadata", "chat_id", chatID, "error", err)
		} else {
			source, found = s.peers.Get(chatID)
		}
	}
	if !found {
		source = PeerInfo{ChatID: chatID, Name: fmt.Sprintf("Chat %d", chatID), Type: "unknown"}
	}
	receivedAt := time.Now().In(s.cfg.Location)
	for _, rule := range rules {
		if !RuleMatches(rule, message) {
			continue
		}
		text, formattedEntities := BuildForwardMessage(message, source, receivedAt)
		var err error
		if rule.ForwardVia == config.ForwardViaBot {
			fingerprint := s.recentBot.Mark(rule.TargetChatID, text)
			err = s.bot.SendMessage(ctx, rule.BotToken, rule.TargetChatID, text, formattedEntities)
			if err != nil {
				s.recentBot.Forget(fingerprint)
			}
		} else {
			err = s.sendRichMessage(ctx, rule.TargetChatID, text, formattedEntities)
		}
		if err != nil {
			s.logger.Error("automatic message forwarding failed", "rule_id", rule.ID, "source_chat_id", chatID, "target_chat_id", rule.TargetChatID, "forward_via", rule.ForwardVia, "error", err)
			continue
		}
		s.logger.Info("automatically forwarded message", "rule_id", rule.ID, "source_chat_id", chatID, "target_chat_id", rule.TargetChatID, "message_id", message.ID, "forward_via", rule.ForwardVia)
	}
	return nil
}

func (s *Service) isOwnOrConfiguredBot(userID int64) bool {
	if userID == 0 {
		return false
	}
	s.apiMu.RLock()
	self := s.self
	s.apiMu.RUnlock()
	if self != nil && self.ID == userID {
		return true
	}
	s.botMu.RLock()
	_, ok := s.botIDs[userID]
	s.botMu.RUnlock()
	return ok
}

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tg-client/internal/config"
	tgservice "tg-client/internal/telegram"
)

type fakeTelegramService struct {
	auth       *tgservice.AuthBroker
	ready      bool
	sentChatID int64
	sentText   string
}

func (f *fakeTelegramService) Auth() *tgservice.AuthBroker { return f.auth }
func (f *fakeTelegramService) IsReady() bool               { return f.ready }
func (f *fakeTelegramService) ConnectionState() string     { return "ready" }
func (f *fakeTelegramService) Account(context.Context) (tgservice.AccountInfo, error) {
	return tgservice.AccountInfo{ID: 42}, nil
}
func (f *fakeTelegramService) ListDialogs(context.Context, int) ([]tgservice.DialogInfo, error) {
	return []tgservice.DialogInfo{{PeerInfo: tgservice.PeerInfo{ChatID: -1001, Name: "test"}}}, nil
}
func (f *fakeTelegramService) Dialog(context.Context, int64) (tgservice.DialogInfo, error) {
	return tgservice.DialogInfo{}, nil
}
func (f *fakeTelegramService) RecentMessages(context.Context, int64, int) ([]tgservice.MessageInfo, error) {
	return nil, nil
}
func (f *fakeTelegramService) SendMessage(_ context.Context, chatID int64, text string) error {
	f.sentChatID = chatID
	f.sentText = text
	return nil
}

func TestAPIKeyRequired(t *testing.T) {
	service := &fakeTelegramService{auth: tgservice.NewAuthBroker(), ready: true}
	server := newTestServer(service)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/account", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("got HTTP %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/account", nil)
	request.Header.Set("Authorization", "Bearer test-key")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("got HTTP %d: %s", response.Code, response.Body.String())
	}
}

func TestSendMessageEndpoint(t *testing.T) {
	service := &fakeTelegramService{auth: tgservice.NewAuthBroker(), ready: true}
	server := newTestServer(service)
	body, _ := json.Marshal(map[string]string{"message": "hello"})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/dialogs/-1002377083049/messages", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-API-Key", "test-key")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("got HTTP %d: %s", response.Code, response.Body.String())
	}
	if service.sentChatID != -1002377083049 || service.sentText != "hello" {
		t.Fatalf("unexpected send: %d %q", service.sentChatID, service.sentText)
	}
}

func newTestServer(service TelegramService) *Server {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(config.HTTPConfig{Host: "127.0.0.1", Port: 8080, APIKeys: []string{"test-key"}}, time.UTC, service, logger)
}

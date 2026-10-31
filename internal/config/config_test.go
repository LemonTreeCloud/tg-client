package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaultsAndEnvironmentExpansion(t *testing.T) {
	t.Setenv("TEST_CLIENT_HASH", "hash-value")
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	content := `
http:
  api_keys: [test-key]
telegram:
  client_id: 123
  client_hash: ${TEST_CLIENT_HASH}
  phone_number: "+10000000000"
  keep_online: true
timezone: Asia/Singapore
scheduled_message_rules:
  - target_chat_id: -1001234567890
    message: hello
    time: "09:00:00"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Port != 8080 || cfg.Telegram.ClientHash != "hash-value" {
		t.Fatalf("defaults or expansion failed: %#v", cfg)
	}
	if cfg.Location.String() != "Asia/Singapore" {
		t.Fatalf("got timezone %s", cfg.Location)
	}
	if !filepath.IsAbs(cfg.Telegram.SessionPath) || !strings.HasPrefix(cfg.Telegram.SessionPath, directory) {
		t.Fatalf("session path was not resolved relative to config: %s", cfg.Telegram.SessionPath)
	}
}

func TestLoadInvalidTimezoneFallsBackToSystem(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	content := `
http:
  api_keys: [test-key]
telegram:
  client_id: 123
  client_hash: hash
  phone_number: "+10000000000"
timezone: Invalid/Timezone
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Location != time.Local || cfg.TimezoneWarning == "" {
		t.Fatalf("expected system timezone fallback, got %v (%q)", cfg.Location, cfg.TimezoneWarning)
	}
}

func TestValidateBotRuleRequiresToken(t *testing.T) {
	cfg := Config{
		Log:      LogConfig{Level: "info"},
		HTTP:     HTTPConfig{Port: 8080, APIKeys: []string{"key"}},
		Telegram: TelegramConfig{ClientID: 1, ClientHash: "hash", PhoneNumber: "+1"},
		AutoForwardRules: []AutoForwardRule{{
			SourceChatID: -1,
			TargetChatID: -2,
			ForwardVia:   ForwardViaBot,
		}},
	}
	if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), "bot_token") {
		t.Fatalf("got %v", err)
	}
}

func TestLoadAnonymousAdminCondition(t *testing.T) {
	for _, tc := range []struct {
		name, extra string
		wantError   bool
	}{
		{"keyword", "        keywords: [alert]\n", false},
		{"identity only", "", false},
		{"conflicting sender", "        sender_user_id: 42\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			content := `http:
  api_keys: [test-key]
telegram:
  client_id: 123
  client_hash: hash
  phone_number: "+1"
auto_forward_rules:
  - source_chat_id: -1001234567890
    target_chat_id: -1009876543210
    match:
      - anonymous_admin: true
` + tc.extra
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "cannot combine anonymous_admin") {
					t.Fatalf("got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !cfg.AutoForwardRules[0].Match[0].AnonymousAdmin {
				t.Fatal("anonymous_admin not decoded")
			}
		})
	}
}

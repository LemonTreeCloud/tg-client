package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	ForwardViaAccount = "account"
	ForwardViaBot     = "bot"
)

type Config struct {
	Log               LogConfig              `yaml:"log"`
	HTTP              HTTPConfig             `yaml:"http"`
	Telegram          TelegramConfig         `yaml:"telegram"`
	Timezone          string                 `yaml:"timezone"`
	AutoForwardRules  []AutoForwardRule      `yaml:"auto_forward_rules"`
	ScheduledMessages []ScheduledMessageRule `yaml:"scheduled_message_rules"`
	Scheduler         SchedulerConfig        `yaml:"scheduler"`

	Location        *time.Location `yaml:"-"`
	TimezoneWarning string         `yaml:"-"`
}

type LogConfig struct {
	Level string `yaml:"level"`
}

type HTTPConfig struct {
	Host    string   `yaml:"host"`
	Port    int      `yaml:"port"`
	APIKeys []string `yaml:"api_keys"`
}

func (c HTTPConfig) ListenAddress() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

type TelegramConfig struct {
	ClientID          int    `yaml:"client_id"`
	ClientHash        string `yaml:"client_hash"`
	PhoneNumber       string `yaml:"phone_number"`
	TwoFactorPassword string `yaml:"two_factor_password"`
	SessionPath       string `yaml:"session_path"`
	KeepOnline        bool   `yaml:"keep_online"`
}

type MatchCondition struct {
	AnonymousAdmin bool     `yaml:"anonymous_admin"`
	SenderUserID   int64    `yaml:"sender_user_id"`
	Keywords       []string `yaml:"keywords"`
	CaseSensitive  bool     `yaml:"case_sensitive"`
}

type AutoForwardRule struct {
	ID           string           `yaml:"id"`
	SourceChatID int64            `yaml:"source_chat_id"`
	TargetChatID int64            `yaml:"target_chat_id"`
	ForwardVia   string           `yaml:"forward_via"`
	BotToken     string           `yaml:"bot_token"`
	Match        []MatchCondition `yaml:"match"`
}

type TimeRange struct {
	Start string `yaml:"start"`
	End   string `yaml:"end"`
}

type ScheduledMessageRule struct {
	ID           string     `yaml:"id"`
	TargetChatID int64      `yaml:"target_chat_id"`
	Message      string     `yaml:"message"`
	Time         string     `yaml:"time"`
	TimeRange    *TimeRange `yaml:"time_range"`
}

type SchedulerConfig struct {
	StatePath string `yaml:"state_path"`
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	expanded := os.ExpandEnv(string(raw))
	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(expanded))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if err := cfg.resolvePaths(path); err != nil {
		return nil, err
	}
	cfg.resolveTimezone()
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.HTTP.Host == "" {
		c.HTTP.Host = "127.0.0.1"
	}
	if c.HTTP.Port == 0 {
		c.HTTP.Port = 8080
	}
	if c.Telegram.SessionPath == "" {
		c.Telegram.SessionPath = "data/session.json"
	}
	if c.Scheduler.StatePath == "" {
		c.Scheduler.StatePath = "data/scheduler-state.json"
	}
	for i := range c.AutoForwardRules {
		if c.AutoForwardRules[i].ForwardVia == "" {
			c.AutoForwardRules[i].ForwardVia = ForwardViaAccount
		}
	}
}

func (c *Config) resolvePaths(configPath string) error {
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("resolve config path: %w", err)
	}
	base := filepath.Dir(abs)
	if !filepath.IsAbs(c.Telegram.SessionPath) {
		c.Telegram.SessionPath = filepath.Join(base, c.Telegram.SessionPath)
	}
	if !filepath.IsAbs(c.Scheduler.StatePath) {
		c.Scheduler.StatePath = filepath.Join(base, c.Scheduler.StatePath)
	}
	return nil
}

func (c *Config) resolveTimezone() {
	if strings.TrimSpace(c.Timezone) == "" {
		c.Location = time.Local
		c.TimezoneWarning = "timezone is not configured; using system timezone " + time.Local.String()
		return
	}
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		c.Location = time.Local
		c.TimezoneWarning = fmt.Sprintf("invalid timezone %q; using system timezone %s", c.Timezone, time.Local)
		return
	}
	c.Location = loc
}

func (c *Config) validate() error {
	var errs []error
	level := strings.ToLower(c.Log.Level)
	if level != "debug" && level != "info" && level != "warn" && level != "error" {
		errs = append(errs, fmt.Errorf("log.level must be one of debug, info, warn, error"))
	}
	if c.HTTP.Port < 1 || c.HTTP.Port > 65535 {
		errs = append(errs, fmt.Errorf("http.port must be between 1 and 65535"))
	}
	if len(c.HTTP.APIKeys) == 0 {
		errs = append(errs, fmt.Errorf("http.api_keys must contain at least one key"))
	}
	for i, key := range c.HTTP.APIKeys {
		if strings.TrimSpace(key) == "" {
			errs = append(errs, fmt.Errorf("http.api_keys[%d] must not be empty", i))
		}
	}
	if c.Telegram.ClientID <= 0 {
		errs = append(errs, fmt.Errorf("telegram.client_id must be positive"))
	}
	if strings.TrimSpace(c.Telegram.ClientHash) == "" {
		errs = append(errs, fmt.Errorf("telegram.client_hash is required"))
	}
	if strings.TrimSpace(c.Telegram.PhoneNumber) == "" {
		errs = append(errs, fmt.Errorf("telegram.phone_number is required"))
	}

	seenForwardIDs := map[string]struct{}{}
	for i := range c.AutoForwardRules {
		r := &c.AutoForwardRules[i]
		prefix := fmt.Sprintf("auto_forward_rules[%d]", i)
		if r.ID == "" {
			r.ID = fmt.Sprintf("forward-%d", i+1)
		}
		if _, ok := seenForwardIDs[r.ID]; ok {
			errs = append(errs, fmt.Errorf("%s.id %q is duplicated", prefix, r.ID))
		}
		seenForwardIDs[r.ID] = struct{}{}
		if r.SourceChatID == 0 || r.TargetChatID == 0 {
			errs = append(errs, fmt.Errorf("%s source_chat_id and target_chat_id must be non-zero", prefix))
		}
		if r.ForwardVia != ForwardViaAccount && r.ForwardVia != ForwardViaBot {
			errs = append(errs, fmt.Errorf("%s.forward_via must be account or bot", prefix))
		}
		if r.ForwardVia == ForwardViaBot && strings.TrimSpace(r.BotToken) == "" {
			errs = append(errs, fmt.Errorf("%s.bot_token is required when forward_via is bot", prefix))
		}
		for j, m := range r.Match {
			if m.AnonymousAdmin && m.SenderUserID != 0 {
				errs = append(errs, fmt.Errorf("%s.match[%d] cannot combine anonymous_admin with sender_user_id", prefix, j))
			}
			if !m.AnonymousAdmin && m.SenderUserID == 0 && len(m.Keywords) == 0 {
				errs = append(errs, fmt.Errorf("%s.match[%d] must configure anonymous_admin, sender_user_id or keywords", prefix, j))
			}
			for k, keyword := range m.Keywords {
				if keyword == "" {
					errs = append(errs, fmt.Errorf("%s.match[%d].keywords[%d] must not be empty", prefix, j, k))
				}
			}
		}
	}

	seenScheduleIDs := map[string]struct{}{}
	for i := range c.ScheduledMessages {
		r := &c.ScheduledMessages[i]
		prefix := fmt.Sprintf("scheduled_message_rules[%d]", i)
		if r.ID == "" {
			r.ID = fmt.Sprintf("scheduled-%d", i+1)
		}
		if _, ok := seenScheduleIDs[r.ID]; ok {
			errs = append(errs, fmt.Errorf("%s.id %q is duplicated", prefix, r.ID))
		}
		seenScheduleIDs[r.ID] = struct{}{}
		if r.TargetChatID == 0 || r.Message == "" {
			errs = append(errs, fmt.Errorf("%s target_chat_id and message are required", prefix))
		}
		if (r.Time == "") == (r.TimeRange == nil) {
			errs = append(errs, fmt.Errorf("%s must configure exactly one of time or time_range", prefix))
		}
		if r.Time != "" {
			if _, err := parseClock(r.Time); err != nil {
				errs = append(errs, fmt.Errorf("%s.time: %w", prefix, err))
			}
		}
		if r.TimeRange != nil {
			if _, err := parseClock(r.TimeRange.Start); err != nil {
				errs = append(errs, fmt.Errorf("%s.time_range.start: %w", prefix, err))
			}
			if _, err := parseClock(r.TimeRange.End); err != nil {
				errs = append(errs, fmt.Errorf("%s.time_range.end: %w", prefix, err))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid configuration: %w", errors.Join(errs...))
	}
	return nil
}

func parseClock(value string) (time.Time, error) {
	parsed, err := time.Parse("15:04:05", value)
	if err != nil {
		return time.Time{}, fmt.Errorf("must use HH:MM:SS: %w", err)
	}
	return parsed, nil
}

package scheduler

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"

	"tg-client/internal/config"
)

type Sender interface {
	SendMessage(ctx context.Context, chatID int64, text string) error
}

type Scheduler struct {
	rules  []config.ScheduledMessageRule
	loc    *time.Location
	sender Sender
	logger *slog.Logger
	state  *stateStore
	random io.Reader
}

type persistedState struct {
	LastSentCycle map[string]string `json:"last_sent_cycle"`
}

type stateStore struct {
	mu    sync.Mutex
	path  string
	state persistedState
}

type plan struct {
	at       time.Time
	deadline time.Time
	cycle    string
}

func New(rules []config.ScheduledMessageRule, loc *time.Location, statePath string, sender Sender, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		rules:  rules,
		loc:    loc,
		sender: sender,
		logger: logger,
		state: &stateStore{
			path:  statePath,
			state: persistedState{LastSentCycle: make(map[string]string)},
		},
		random: cryptorand.Reader,
	}
}

func (s *Scheduler) Run(ctx context.Context) error {
	if err := s.state.load(); err != nil {
		s.logger.Warn("could not load scheduler state; continuing with an empty state", "error", err)
	}
	var workers sync.WaitGroup
	for _, rule := range s.rules {
		rule := rule
		workers.Add(1)
		go func() {
			defer workers.Done()
			s.runRule(ctx, rule)
		}()
	}
	<-ctx.Done()
	workers.Wait()
	return ctx.Err()
}

func (s *Scheduler) runRule(ctx context.Context, rule config.ScheduledMessageRule) {
	for {
		lastCycle := s.state.lastCycle(rule.ID)
		next, err := s.nextPlan(rule, time.Now().In(s.loc), lastCycle)
		if err != nil {
			s.logger.Error("failed to calculate scheduled message time", "rule_id", rule.ID, "error", err)
			return
		}
		s.logger.Debug("scheduled daily message", "rule_id", rule.ID, "target_chat_id", rule.TargetChatID, "send_at", next.at)
		if !waitUntil(ctx, next.at) {
			return
		}

		for {
			err = s.sender.SendMessage(ctx, rule.TargetChatID, rule.Message)
			if err == nil {
				s.state.markSent(rule.ID, next.cycle)
				if saveErr := s.state.save(); saveErr != nil {
					s.logger.Error("scheduled message was sent but scheduler state could not be persisted", "rule_id", rule.ID, "error", saveErr)
				}
				s.logger.Info("sent scheduled daily message", "rule_id", rule.ID, "target_chat_id", rule.TargetChatID)
				break
			}
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return
			}
			s.logger.Error("failed to send scheduled message; retrying in one minute", "rule_id", rule.ID, "target_chat_id", rule.TargetChatID, "error", err)
			if time.Now().In(s.loc).Add(time.Minute).After(next.deadline) || !waitUntil(ctx, time.Now().Add(time.Minute)) {
				break
			}
		}
	}
}

func (s *Scheduler) nextPlan(rule config.ScheduledMessageRule, now time.Time, lastCycle string) (plan, error) {
	choose := func(n int64) (int64, error) {
		if n <= 1 {
			return 0, nil
		}
		value, err := cryptorand.Int(s.random, big.NewInt(n))
		if err != nil {
			return 0, err
		}
		return value.Int64(), nil
	}
	return calculateNext(rule, now.In(s.loc), lastCycle, choose)
}

func calculateNext(rule config.ScheduledMessageRule, now time.Time, lastCycle string, choose func(int64) (int64, error)) (plan, error) {
	if rule.Time != "" {
		seconds, err := clockSeconds(rule.Time)
		if err != nil {
			return plan{}, err
		}
		for dayOffset := 0; dayOffset < 3; dayOffset++ {
			anchor := dayStart(now).AddDate(0, 0, dayOffset)
			cycle := anchor.Format("2006-01-02")
			at := clockAt(anchor, seconds)
			if cycle == lastCycle || !at.After(now) {
				continue
			}
			return plan{at: at, deadline: anchor.AddDate(0, 0, 1).Add(-time.Second), cycle: cycle}, nil
		}
		return plan{}, errors.New("could not find next fixed send time")
	}

	startSeconds, err := clockSeconds(rule.TimeRange.Start)
	if err != nil {
		return plan{}, err
	}
	endSeconds, err := clockSeconds(rule.TimeRange.End)
	if err != nil {
		return plan{}, err
	}
	for dayOffset := -1; dayOffset < 3; dayOffset++ {
		anchor := dayStart(now).AddDate(0, 0, dayOffset)
		cycle := anchor.Format("2006-01-02")
		if cycle == lastCycle {
			continue
		}
		start := clockAt(anchor, startSeconds)
		endAnchor := anchor
		if endSeconds < startSeconds {
			endAnchor = anchor.AddDate(0, 0, 1)
		}
		end := clockAt(endAnchor, endSeconds)
		if !end.After(now) {
			continue
		}
		lower := start
		if !lower.After(now) {
			lower = now.Add(time.Second)
		}
		if lower.After(end) {
			continue
		}
		spanSeconds := int64(end.Sub(lower)/time.Second) + 1
		offset, err := choose(spanSeconds)
		if err != nil {
			return plan{}, fmt.Errorf("choose random time: %w", err)
		}
		return plan{at: lower.Add(time.Duration(offset) * time.Second), deadline: end, cycle: cycle}, nil
	}
	return plan{}, errors.New("could not find next random-window send time")
}

func dayStart(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, value.Location())
}

func clockAt(day time.Time, seconds int) time.Time {
	year, month, date := day.Date()
	hour := seconds / 3600
	minute := (seconds % 3600) / 60
	second := seconds % 60
	return time.Date(year, month, date, hour, minute, second, 0, day.Location())
}

func clockSeconds(value string) (int, error) {
	parsed, err := time.Parse("15:04:05", value)
	if err != nil {
		return 0, fmt.Errorf("parse time %q: %w", value, err)
	}
	return parsed.Hour()*3600 + parsed.Minute()*60 + parsed.Second(), nil
}

func waitUntil(ctx context.Context, at time.Time) bool {
	delay := time.Until(at)
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *stateStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &s.state); err != nil {
		return fmt.Errorf("decode %s: %w", s.path, err)
	}
	if s.state.LastSentCycle == nil {
		s.state.LastSentCycle = make(map[string]string)
	}
	return nil
}

func (s *stateStore) lastCycle(ruleID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.LastSentCycle[ruleID]
}

func (s *stateStore) markSent(ruleID, cycle string) {
	s.mu.Lock()
	s.state.LastSentCycle[ruleID] = cycle
	s.mu.Unlock()
}

func (s *stateStore) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, s.path)
}

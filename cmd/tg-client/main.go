package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"tg-client/internal/config"
	"tg-client/internal/httpapi"
	"tg-client/internal/scheduler"
	tgservice "tg-client/internal/telegram"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to YAML configuration")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	logger := newLogger(os.Stdout, cfg.Log.Level, cfg.Location)
	if cfg.TimezoneWarning != "" {
		logger.Warn(cfg.TimezoneWarning)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	service := tgservice.NewService(cfg, logger)
	apiServer := httpapi.New(cfg.HTTP, cfg.Location, service, logger)
	dailyScheduler := scheduler.New(cfg.ScheduledMessages, cfg.Location, cfg.Scheduler.StatePath, service, logger)
	go readVerificationCodes(ctx, service.Auth(), logger)

	errCh := make(chan error, 3)
	var workers sync.WaitGroup
	workers.Add(3)
	go func() {
		defer workers.Done()
		errCh <- apiServer.Run(ctx)
	}()
	go func() {
		defer workers.Done()
		errCh <- service.Run(ctx)
	}()
	go func() {
		defer workers.Done()
		select {
		case <-service.Ready():
			errCh <- dailyScheduler.Run(ctx)
		case <-ctx.Done():
			errCh <- ctx.Err()
		}
	}()

	err = <-errCh
	if err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("tg-client stopped because a component failed", "error", err)
	}
	cancel()
	workers.Wait()
}

func newLogger(output io.Writer, level string, location *time.Location) *slog.Logger {
	levels := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	}
	if location == nil {
		location = time.Local
	}
	handler := slog.NewTextHandler(output, &slog.HandlerOptions{
		Level: levels[strings.ToLower(level)],
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if len(groups) == 0 && attr.Key == slog.TimeKey && attr.Value.Kind() == slog.KindTime {
				attr.Value = slog.TimeValue(attr.Value.Time().In(location))
			}
			return attr
		},
	})
	return slog.New(handler)
}

func readVerificationCodes(ctx context.Context, broker *tgservice.AuthBroker, logger *slog.Logger) {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if broker.Snapshot().State != tgservice.AuthStateWaitingCode {
			continue
		}
		submitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		err := broker.SubmitCode(submitCtx, scanner.Text())
		cancel()
		if err != nil {
			logger.Warn("verification code from stdin was rejected", "error", err)
		} else {
			logger.Info("Telegram login succeeded")
		}
	}
}

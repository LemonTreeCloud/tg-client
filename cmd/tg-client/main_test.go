package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestLoggerUsesConfiguredTimezone(t *testing.T) {
	var output bytes.Buffer
	logger := newLogger(&output, "info", time.FixedZone("SGT", 8*60*60))
	logger.Info("timezone test")

	line := output.String()
	if !strings.Contains(line, "+08:00") {
		t.Fatalf("expected configured timezone offset in log, got %q", line)
	}
}

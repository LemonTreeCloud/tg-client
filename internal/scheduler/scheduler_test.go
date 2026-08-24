package scheduler

import (
	"testing"
	"time"

	"tg-client/internal/config"
)

func TestCalculateFixedTime(t *testing.T) {
	loc := time.FixedZone("SGT", 8*60*60)
	now := time.Date(2026, 8, 24, 8, 0, 0, 0, loc)
	rule := config.ScheduledMessageRule{Time: "09:30:00"}
	got, err := calculateNext(rule, now, "", func(int64) (int64, error) { return 0, nil })
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 8, 24, 9, 30, 0, 0, loc)
	if !got.at.Equal(want) || got.cycle != "2026-08-24" {
		t.Fatalf("got %v (%s)", got.at, got.cycle)
	}

	got, err = calculateNext(rule, now, "2026-08-24", func(int64) (int64, error) { return 0, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got.cycle != "2026-08-25" {
		t.Fatalf("expected next day, got %s", got.cycle)
	}
}

func TestCalculateRandomWindowFromNow(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 8, 24, 15, 0, 0, 0, loc)
	rule := config.ScheduledMessageRule{TimeRange: &config.TimeRange{Start: "14:00:00", End: "16:00:00"}}
	got, err := calculateNext(rule, now, "", func(n int64) (int64, error) { return n - 1, nil })
	if err != nil {
		t.Fatal(err)
	}
	if !got.at.Equal(time.Date(2026, 8, 24, 16, 0, 0, 0, loc)) {
		t.Fatalf("got %v", got.at)
	}
}

func TestCalculateOvernightWindow(t *testing.T) {
	now := time.Date(2026, 8, 25, 0, 30, 0, 0, time.UTC)
	rule := config.ScheduledMessageRule{TimeRange: &config.TimeRange{Start: "23:00:00", End: "01:00:00"}}
	got, err := calculateNext(rule, now, "", func(int64) (int64, error) { return 0, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got.cycle != "2026-08-24" || !got.at.After(now) {
		t.Fatalf("got cycle %s at %v", got.cycle, got.at)
	}
}

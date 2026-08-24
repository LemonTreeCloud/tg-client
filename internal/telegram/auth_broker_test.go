package telegram

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAuthBrokerRetry(t *testing.T) {
	b := NewAuthBroker()
	b.SetWaitingCode(nil)

	result := make(chan error, 1)
	go func() { result <- b.SubmitCode(context.Background(), "12345") }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request, err := b.nextCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("bad code")
	b.complete(request, want, 0)
	if err := <-result; !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
	if snapshot := b.Snapshot(); !snapshot.WaitingForCode || snapshot.LastError == "" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
}

func TestAuthBrokerRejectsConcurrentSubmission(t *testing.T) {
	b := NewAuthBroker()
	b.SetWaitingCode(nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = b.SubmitCode(ctx, "12345") }()

	deadline := time.Now().Add(time.Second)
	for b.Snapshot().State != AuthStateAuthenticating && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := b.SubmitCode(context.Background(), "54321"); !errors.Is(err, ErrNotWaitingForCode) {
		t.Fatalf("got %v", err)
	}
}

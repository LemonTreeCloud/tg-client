package botapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/gotd/td/tg"
)

func TestSendMessageDoesNotExposeTokenInError(t *testing.T) {
	const token = "very-secret-token"
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(`{"ok":false,"error_code":400,"description":"bad chat"}`)),
			Request:    r,
		}, nil
	})}
	client := NewWithHTTPClient(httpClient, "https://bot.invalid")
	err := client.SendMessage(context.Background(), token, 1, "hello", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); contains(got, token) {
		t.Fatalf("error leaked token: %s", got)
	}
}

func TestNetworkErrorDoesNotExposeToken(t *testing.T) {
	const token = "very-secret-token"
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("network unavailable")
	})}
	client := NewWithHTTPClient(httpClient, "https://bot.invalid")
	err := client.SendMessage(context.Background(), token, 1, "hello", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); contains(got, token) {
		t.Fatalf("error leaked token: %s", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestConvertEntities(t *testing.T) {
	entities := []tg.MessageEntityClass{
		&tg.MessageEntityBold{Offset: 0, Length: 4},
		&tg.MessageEntityTextURL{Offset: 5, Length: 3, URL: "https://example.com"},
		&tg.MessageEntityBlockquote{Collapsed: true, Offset: 9, Length: 2},
	}
	got := ConvertEntities(entities)
	if len(got) != 3 {
		t.Fatalf("got %d entities", len(got))
	}
	if got[0].Type != "bold" || got[1].Type != "text_link" || got[2].Type != "expandable_blockquote" {
		t.Fatalf("unexpected entities: %#v", got)
	}
}

func contains(s, part string) bool {
	for i := 0; i+len(part) <= len(s); i++ {
		if s[i:i+len(part)] == part {
			return true
		}
	}
	return false
}

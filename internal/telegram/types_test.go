package telegram

import (
	"reflect"
	"testing"

	"github.com/gotd/td/tg"
)

func TestExtractButtons(t *testing.T) {
	markup := &tg.ReplyInlineMarkup{Rows: []tg.KeyboardInlineButtonRow{
		{Buttons: []tg.KeyboardInlineButton{
			{Text: "Link", Type: &tg.InlineButtonTypeURL{URL: "https://example.com"}},
			{Text: "Login", Type: &tg.InlineButtonTypeURLAuth{URL: "https://example.com/login"}},
			{Text: "Run", Type: &tg.InlineButtonTypeCallback{Data: []byte("id=7")}},
			{Text: "Binary", Type: &tg.InlineButtonTypeCallback{Data: []byte{0xff}}},
		}},
		{Buttons: []tg.KeyboardInlineButton{
			{Text: "Search", Type: &tg.InlineButtonTypeSwitchInline{Query: "query"}},
			{Text: "App", Type: &tg.InlineButtonTypeWebView{URL: "https://example.com/app"}},
			{Text: "Copy", Type: &tg.InlineButtonTypeCopy{CopyText: "copied"}},
			{Text: "Unsupported", Type: &tg.InlineButtonTypeGame{}},
			{Text: "Missing type"},
		}},
	}}
	want := []ButtonInfo{
		{Text: "Link", Type: "url", Value: "https://example.com"},
		{Text: "Login", Type: "url_auth", Value: "https://example.com/login"},
		{Text: "Run", Type: "callback_data", Value: "id=7"},
		{Text: "Binary", Type: "callback_data", Value: "base64:/w=="},
		{Text: "Search", Type: "switch_inline_query", Value: "query"},
		{Text: "App", Type: "web_app", Value: "https://example.com/app"},
		{Text: "Copy", Type: "copy_text", Value: "copied"},
	}
	if got := ExtractButtons(markup); !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractButtons() = %#v, want %#v", got, want)
	}
	for _, markup := range []tg.ReplyMarkupClass{nil, &tg.ReplyKeyboardHide{}, &tg.ReplyInlineMarkup{}} {
		if got := ExtractButtons(markup); len(got) != 0 {
			t.Fatalf("expected no buttons for %T, got %#v", markup, got)
		}
	}
}

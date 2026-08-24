package telegram

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/gotd/td/tg"

	"tg-client/internal/config"
)

const forwardSeparator = "\n\n--- tg-client ---\n"

func RuleMatches(rule config.AutoForwardRule, message *tg.Message) bool {
	if len(rule.Match) == 0 {
		return true
	}
	senderID := SenderUserID(message.FromID)
	for _, condition := range rule.Match {
		if condition.SenderUserID != 0 && condition.SenderUserID != senderID {
			continue
		}
		if len(condition.Keywords) > 0 && !matchesAnyKeyword(message.Message, condition.Keywords, condition.CaseSensitive) {
			continue
		}
		return true
	}
	return false
}

func matchesAnyKeyword(message string, keywords []string, caseSensitive bool) bool {
	if !caseSensitive {
		message = strings.ToLower(message)
	}
	for _, keyword := range keywords {
		if !caseSensitive {
			keyword = strings.ToLower(keyword)
		}
		if strings.Contains(message, keyword) {
			return true
		}
	}
	return false
}

func BuildForwardMessage(message *tg.Message, source PeerInfo, receivedAt time.Time) (string, []tg.MessageEntityClass) {
	link := MessageLink(source, message.ID)
	var footer strings.Builder
	footer.WriteString(forwardSeparator)
	buttons := ExtractButtons(message.ReplyMarkup)
	if len(buttons) > 0 {
		footer.WriteString(FormatButtons(buttons))
		footer.WriteByte('\n')
	}
	fmt.Fprintf(&footer, "来源会话：%s (Chat ID: %d)\n", source.Name, source.ChatID)
	footer.WriteString("消息链接：")
	linkOffsetInFooter := utf16Length(footer.String())
	footer.WriteString(link)
	footer.WriteByte('\n')
	fmt.Fprintf(&footer, "收到时间：%s", receivedAt.Format("2006-01-02 15:04:05"))

	text := message.Message + footer.String()
	entities := append([]tg.MessageEntityClass(nil), message.Entities...)
	if link != "unavailable" {
		entities = append(entities, &tg.MessageEntityURL{
			Offset: utf16Length(message.Message) + linkOffsetInFooter,
			Length: utf16Length(link),
		})
	}
	return text, entities
}

func utf16Length(value string) int {
	return len(utf16.Encode([]rune(value)))
}

type recentFingerprint struct {
	chatID int64
	hash   [32]byte
}

type RecentBotMessages struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[recentFingerprint]time.Time
}

func NewRecentBotMessages(ttl time.Duration) *RecentBotMessages {
	return &RecentBotMessages{ttl: ttl, entries: make(map[recentFingerprint]time.Time)}
}

func (r *RecentBotMessages) Mark(chatID int64, text string) recentFingerprint {
	key := recentFingerprint{chatID: chatID, hash: sha256.Sum256([]byte(text))}
	r.mu.Lock()
	r.entries[key] = time.Now().Add(r.ttl)
	r.purgeLocked(time.Now())
	r.mu.Unlock()
	return key
}

func (r *RecentBotMessages) Forget(key recentFingerprint) {
	r.mu.Lock()
	delete(r.entries, key)
	r.mu.Unlock()
}

func (r *RecentBotMessages) Seen(chatID int64, text string) bool {
	key := recentFingerprint{chatID: chatID, hash: sha256.Sum256([]byte(text))}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.purgeLocked(now)
	expires, ok := r.entries[key]
	return ok && now.Before(expires)
}

func (r *RecentBotMessages) purgeLocked(now time.Time) {
	for key, expiry := range r.entries {
		if !now.Before(expiry) {
			delete(r.entries, key)
		}
	}
}

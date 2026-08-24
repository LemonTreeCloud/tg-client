package telegram

import (
	"context"
	"errors"
	"strings"
	"sync"
)

type AuthState string

const (
	AuthStateStarting       AuthState = "starting"
	AuthStateSendingCode    AuthState = "sending_code"
	AuthStateWaitingCode    AuthState = "waiting_code"
	AuthStateAuthenticating AuthState = "authenticating"
	AuthStateAuthorized     AuthState = "authorized"
	AuthStateError          AuthState = "error"
)

var ErrNotWaitingForCode = errors.New("Telegram login is not waiting for a verification code")

type AuthSnapshot struct {
	State          AuthState `json:"state"`
	WaitingForCode bool      `json:"waiting_for_code"`
	Authorized     bool      `json:"authorized"`
	UserID         int64     `json:"user_id,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
}

type codeSubmission struct {
	code   string
	result chan error
}

type AuthBroker struct {
	mu      sync.RWMutex
	state   AuthState
	userID  int64
	lastErr string
	codes   chan *codeSubmission
}

func NewAuthBroker() *AuthBroker {
	return &AuthBroker{
		state: AuthStateStarting,
		codes: make(chan *codeSubmission, 1),
	}
}

func (b *AuthBroker) Snapshot() AuthSnapshot {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return AuthSnapshot{
		State:          b.state,
		WaitingForCode: b.state == AuthStateWaitingCode,
		Authorized:     b.state == AuthStateAuthorized,
		UserID:         b.userID,
		LastError:      b.lastErr,
	}
}

func (b *AuthBroker) SetSendingCode() {
	b.set(AuthStateSendingCode, 0, "")
}

func (b *AuthBroker) SetWaitingCode(lastErr error) {
	message := ""
	if lastErr != nil {
		message = lastErr.Error()
	}
	b.set(AuthStateWaitingCode, 0, message)
}

func (b *AuthBroker) SetAuthorized(userID int64) {
	b.set(AuthStateAuthorized, userID, "")
}

func (b *AuthBroker) SetError(err error) {
	message := ""
	if err != nil {
		message = err.Error()
	}
	b.set(AuthStateError, 0, message)
}

func (b *AuthBroker) set(state AuthState, userID int64, lastErr string) {
	b.mu.Lock()
	b.state = state
	b.userID = userID
	b.lastErr = lastErr
	b.mu.Unlock()
}

func (b *AuthBroker) SubmitCode(ctx context.Context, code string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return errors.New("verification code must not be empty")
	}

	b.mu.Lock()
	if b.state != AuthStateWaitingCode {
		b.mu.Unlock()
		return ErrNotWaitingForCode
	}
	b.state = AuthStateAuthenticating
	b.lastErr = ""
	b.mu.Unlock()

	submission := &codeSubmission{code: code, result: make(chan error, 1)}
	select {
	case b.codes <- submission:
	case <-ctx.Done():
		b.SetWaitingCode(ctx.Err())
		return ctx.Err()
	}

	select {
	case err := <-submission.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *AuthBroker) nextCode(ctx context.Context) (*codeSubmission, error) {
	select {
	case request := <-b.codes:
		return request, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *AuthBroker) complete(request *codeSubmission, err error, userID int64) {
	if err != nil {
		b.SetWaitingCode(err)
	} else {
		b.SetAuthorized(userID)
	}
	request.result <- err
}

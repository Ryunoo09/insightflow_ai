package mailer

import (
	"context"
	"sync"
)

// Message is a captured OTP delivery.
type Message struct {
	To   string
	Code string
}

// Fake records messages in memory for tests instead of sending them.
type Fake struct {
	mu       sync.Mutex
	messages []Message
	err      error
}

// FailWith makes every subsequent send return err.
func (f *Fake) FailWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// SendOTP captures the message.
func (f *Fake) SendOTP(_ context.Context, to, code string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.messages = append(f.messages, Message{To: to, Code: code})
	return nil
}

// Messages returns a copy of everything captured so far.
func (f *Fake) Messages() []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Message, len(f.messages))
	copy(out, f.messages)
	return out
}

// Last returns the most recently captured message.
func (f *Fake) Last() (Message, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.messages) == 0 {
		return Message{}, false
	}
	return f.messages[len(f.messages)-1], true
}

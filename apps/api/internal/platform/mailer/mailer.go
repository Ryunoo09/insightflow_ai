// Package mailer sends transactional email. Only the OTP message is needed at
// MVP; other messages are added when their feature lands.
package mailer

import (
	"context"
	"fmt"

	"github.com/wneessen/go-mail"
)

// Mailer delivers an OTP code to a recipient.
type Mailer interface {
	SendOTP(ctx context.Context, to, code string) error
}

// SMTPConfig configures the SMTP sender.
type SMTPConfig struct {
	Host     string
	Port     int
	From     string
	Username string
	Password string
	// UseTLS enables STARTTLS/implicit TLS for authenticated relays. It is off
	// for the local Mailpit catcher, which speaks plain SMTP.
	UseTLS bool
}

// SMTPSender delivers mail through an SMTP server.
type SMTPSender struct {
	cfg SMTPConfig
}

// NewSMTP builds an SMTP sender.
func NewSMTP(cfg SMTPConfig) *SMTPSender { return &SMTPSender{cfg: cfg} }

// SendOTP emails a 6-digit verification code. The code is never logged.
func (s *SMTPSender) SendOTP(ctx context.Context, to, code string) error {
	msg := mail.NewMsg()
	if err := msg.From(s.cfg.From); err != nil {
		return fmt.Errorf("mailer: from: %w", err)
	}
	if err := msg.To(to); err != nil {
		return fmt.Errorf("mailer: to: %w", err)
	}
	msg.Subject("Kode verifikasi InsightFlow AI")
	msg.SetBodyString(mail.TypeTextPlain, otpBody(code))

	opts := []mail.Option{mail.WithPort(s.cfg.Port)}
	if s.cfg.Username != "" {
		opts = append(opts, mail.WithSMTPAuth(mail.SMTPAuthPlain),
			mail.WithUsername(s.cfg.Username), mail.WithPassword(s.cfg.Password))
	}
	if s.cfg.UseTLS {
		opts = append(opts, mail.WithTLSPolicy(mail.TLSMandatory))
	} else {
		opts = append(opts, mail.WithTLSPolicy(mail.NoTLS))
	}

	client, err := mail.NewClient(s.cfg.Host, opts...)
	if err != nil {
		return fmt.Errorf("mailer: client: %w", err)
	}
	if err := client.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("mailer: send: %w", err)
	}
	return nil
}

func otpBody(code string) string {
	return fmt.Sprintf(
		"Kode verifikasi InsightFlow AI Anda: %s\n\n"+
			"Kode berlaku 5 menit. Jangan bagikan kode ini kepada siapa pun.\n"+
			"Jika Anda tidak meminta kode ini, abaikan email ini.\n",
		code,
	)
}

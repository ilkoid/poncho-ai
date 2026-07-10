// Package email sends mail over SMTP with implicit TLS (port 465), STARTTLS
// (port 587), or plaintext. It is a library: configuration is loaded from YAML
// via pkg/config.LoadYAML, every mandatory field is validated at construction
// time (no defaults — a missing field fails fast), and the recipient list is
// taken from config with per-message override.
//
// Typical use:
//
//	var cfg email.Config
//	if err := config.LoadYAML("config.yaml", &cfg); err != nil { return err }
//	sender, err := email.NewSender(cfg)
//	if err != nil { return err }
//	return sender.Send(ctx, email.Message{
//	    Subject:  "Отчёт",
//	    TextBody: "Во вложении — отчёт за сегодня.",
//	    Attachments: []email.Attachment{{
//	        Filename: "report.csv",
//	        Content:  csvBytes,
//	    }},
//	})
package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Sender holds validated SMTP configuration and sends messages.
type Sender struct {
	cfg Config
}

// NewSender validates the config and returns a ready Sender. It does NOT open
// a connection — each Send opens and closes its own connection, which keeps
// the Sender safe to share across goroutines without locking.
func NewSender(cfg Config) (*Sender, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Sender{cfg: cfg}, nil
}

// Send builds and transmits a single message.
//
// Recipient resolution: empty To/Cc/Bcc on the Message fall back to
// Config.Recipients; non-empty fields override the config entirely.
func (s *Sender) Send(ctx context.Context, msg Message) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("email: send cancelled before start: %w", err)
	}

	to := overrideOr(msg.To, s.cfg.Recipients.To)
	cc := overrideOr(msg.Cc, s.cfg.Recipients.Cc)
	bcc := overrideOr(msg.Bcc, s.cfg.Recipients.Bcc)

	if len(to) == 0 {
		return fmt.Errorf("email: no To recipients (message overrode config with empty list): %w", ErrNoRecipient)
	}
	if msg.TextBody == "" {
		return fmt.Errorf("email: text body is required")
	}

	raw, err := buildMessage(msg, s.cfg.SMTP.From, to, cc, bcc, s.cfg.SMTP.KeepBCC)
	if err != nil {
		return err
	}

	if err := s.deliver(ctx, raw, to, cc, bcc); err != nil {
		return fmt.Errorf("email: send: %w", err)
	}
	return nil
}

// overrideOr returns override when it is non-empty, otherwise fallback.
func overrideOr(override, fallback []string) []string {
	if len(override) > 0 {
		return override
	}
	return fallback
}

// deliver opens an SMTP connection in the configured TLS mode, authenticates,
// and transmits raw to every envelope recipient (To + Cc + Bcc).
func (s *Sender) deliver(ctx context.Context, raw []byte, to, cc, bcc []string) error {
	sm := s.cfg.SMTP
	addr := net.JoinHostPort(sm.Host, fmt.Sprintf("%d", sm.Port))

	client, cleanup, err := s.dial(ctx, addr)
	if err != nil {
		return err
	}
	// cleanup always closes the connection and swallows its error so the
	// real failure (if any) is what the caller sees.
	defer func() { _ = cleanup() }()

	if sm.Auth {
		if err := client.Auth(selectAuth(sm)); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}

	if err := client.Mail(sm.From); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	// Envelope recipients include Bcc: they receive the message, but Bcc
	// addresses are omitted from headers unless KeepBCC is set (handled in
	// buildMessage). Cc recipients likewise receive a copy.
	for _, rcpt := range allRecipients(to, cc, bcc) {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("RCPT TO %q: %w", rcpt, err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close DATA: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled during send: %w", err)
	}
	return nil
}

// dial establishes the SMTP client according to TLSMode and returns it along
// with a cleanup function that closes the underlying connection.
//
// cleanup signature: returns an error (so callers can wrap it), but in
// practice the deferred call in deliver ignores it to prioritize the real
// error.
func (s *Sender) dial(ctx context.Context, addr string) (*smtp.Client, func() error, error) {
	sm := s.cfg.SMTP

	switch sm.TLSMode {
	case tlsModeImplicit:
		// Port 465: implicit TLS — the TCP connection is immediately
		// upgraded to TLS before any SMTP traffic.
		tlsCfg := &tls.Config{
			ServerName:         sm.Host,
			InsecureSkipVerify: !sm.TLSCertCheck,
		}
		d := &net.Dialer{Timeout: sm.Timeout}
		rawConn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
		}
		// Wrap the deadline around the handshake.
		tlsConn := tls.Client(rawConn, tlsCfg)
		if dl, ok := ctx.Deadline(); ok {
			_ = tlsConn.SetDeadline(dl)
		} else {
			_ = tlsConn.SetDeadline(time.Now().Add(sm.Timeout))
		}
		if err := tlsConn.Handshake(); err != nil {
			_ = rawConn.Close()
			return nil, nil, fmt.Errorf("tls handshake %s: %w", addr, err)
		}
		client, err := smtp.NewClient(tlsConn, sm.Host)
		if err != nil {
			_ = tlsConn.Close()
			return nil, nil, fmt.Errorf("smtp client %s: %w", addr, err)
		}
		return client, client.Quit, nil

	case tlsModeStartTLS:
		// Port 587: plaintext connect, then STARTTLS upgrade.
		d := &net.Dialer{Timeout: sm.Timeout}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
		}
		if dl, ok := ctx.Deadline(); ok {
			_ = conn.SetDeadline(dl)
		}
		client, err := smtp.NewClient(conn, sm.Host)
		if err != nil {
			_ = conn.Close()
			return nil, nil, fmt.Errorf("smtp client %s: %w", addr, err)
		}
		if err := client.StartTLS(&tls.Config{
			ServerName:         sm.Host,
			InsecureSkipVerify: !sm.TLSCertCheck,
		}); err != nil {
			_ = client.Quit()
			return nil, nil, fmt.Errorf("starttls %s: %w", addr, err)
		}
		return client, client.Quit, nil

	case tlsModeNone:
		// Plaintext — only acceptable for a local test server.
		d := &net.Dialer{Timeout: sm.Timeout}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
		}
		if dl, ok := ctx.Deadline(); ok {
			_ = conn.SetDeadline(dl)
		}
		client, err := smtp.NewClient(conn, sm.Host)
		if err != nil {
			_ = conn.Close()
			return nil, nil, fmt.Errorf("smtp client %s: %w", addr, err)
		}
		return client, client.Quit, nil

	default:
		// Validate() should prevent this, but guard anyway.
		return nil, nil, fmt.Errorf("unsupported tls_mode %q", sm.TLSMode)
	}
}

// allRecipients is the deduplicated envelope recipient list (To + Cc + Bcc).
func allRecipients(groups ...[]string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 8)
	for _, g := range groups {
		for _, r := range g {
			r = strings.TrimSpace(r)
			if r == "" {
				continue
			}
			if _, dup := seen[r]; dup {
				continue
			}
			seen[r] = struct{}{}
			out = append(out, r)
		}
	}
	return out
}

// ErrNoRecipient is returned when a message would have no recipients at all.
var ErrNoRecipient = errors.New("email: no recipients")

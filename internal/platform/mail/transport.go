package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Transport delivers one rendered message.
type Transport interface {
	Send(ctx context.Context, e Envelope) error
}

// TLS modes for SMTPTransport.
const (
	TLSStartTLS = "starttls" // port 587: plain connection upgraded with STARTTLS (required)
	TLSImplicit = "tls"      // port 465: TLS from the first byte
	TLSNone     = "none"     // local test servers (Mailpit) only
)

// SMTPTransport sends through an SMTP server.
type SMTPTransport struct {
	Host     string
	Port     int
	Username string
	Password string
	TLS      string
	Timeout  time.Duration
}

func (t SMTPTransport) Send(ctx context.Context, e Envelope) error {
	msg, err := e.Bytes()
	if err != nil {
		return err
	}
	timeout := t.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	addr := net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
	dialer := &net.Dialer{Timeout: timeout}
	var conn net.Conn
	if t.TLS == TLSImplicit {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: t.Host, MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline) //nolint:errcheck

	c, err := smtp.NewClient(conn, t.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp greeting: %w", err)
	}
	defer c.Close()

	if t.TLS == TLSStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("smtp: server does not offer STARTTLS")
		}
		if err := c.StartTLS(&tls.Config{ServerName: t.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if t.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", t.Username, t.Password, t.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(e.From.Address); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	if err := c.Rcpt(e.To); err != nil {
		return fmt.Errorf("smtp RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp end of data: %w", err)
	}
	return c.Quit()
}

// FileTransport writes each message to Dir as an .eml file (development without SMTP).
type FileTransport struct{ Dir string }

func (t FileTransport) Send(_ context.Context, e Envelope) error {
	msg, err := e.Bytes()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(t.Dir, 0o750); err != nil {
		return err
	}
	name := fmt.Sprintf("%s-%s.eml", e.Date.UTC().Format("20060102T150405"), sanitize(e.MessageID))
	return os.WriteFile(filepath.Join(t.Dir, name), msg, 0o600)
}

func sanitize(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '.' {
			out = append(out, r)
		}
	}
	return string(out)
}

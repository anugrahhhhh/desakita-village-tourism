package mail

import (
	"bufio"
	"encoding/base64"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is a minimal SMTP server: enough of RFC 5321 to accept one message, record the
// conversation and optionally offer STARTTLS/AUTH.
type fakeSMTP struct {
	ln       net.Listener
	mu       sync.Mutex
	commands []string
	data     string
	auth     string
	starttls bool
}

func newFakeSMTP(t *testing.T, offerStartTLS bool) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln, starttls: offerStartTLS}
	t.Cleanup(func() { ln.Close() })
	go f.serve()
	return f
}

func (f *fakeSMTP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeSMTP) serve() {
	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
	r := bufio.NewReader(conn)
	w := func(s string) { conn.Write([]byte(s + "\r\n")) } //nolint:errcheck
	w("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		f.mu.Lock()
		f.commands = append(f.commands, line)
		f.mu.Unlock()
		cmd := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		switch cmd {
		case "EHLO", "HELO":
			if f.starttls {
				w("250-fake\r\n250-STARTTLS\r\n250 AUTH PLAIN")
			} else {
				w("250-fake\r\n250 AUTH PLAIN")
			}
		case "AUTH":
			f.mu.Lock()
			f.auth = line
			f.mu.Unlock()
			w("235 ok")
		case "MAIL", "RCPT", "RSET", "NOOP":
			w("250 ok")
		case "DATA":
			w("354 go ahead")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			w("250 queued")
		case "QUIT":
			w("221 bye")
			return
		default:
			w("502 not implemented")
		}
	}
}

func TestSMTPTransport(t *testing.T) {
	f := newFakeSMTP(t, false)
	tr := SMTPTransport{Host: "localhost", Port: f.port(), Username: "desakita", Password: "rahasia", TLS: TLSNone, Timeout: 5 * time.Second}
	e := Envelope{From: testFrom(), To: "sari@example.com", Subject: "Pemesanan dikonfirmasi", Body: "Halo Sari", MessageID: "<outbox-9@desakita.id>", Date: time.Now()}
	if err := tr.Send(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	joined := strings.Join(f.commands, "\n")
	for _, want := range []string{"MAIL FROM:<noreply@desakita.id>", "RCPT TO:<sari@example.com>", "QUIT"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing command %q in:\n%s", want, joined)
		}
	}
	creds, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(f.auth, "AUTH PLAIN "))
	if string(creds) != "\x00desakita\x00rahasia" {
		t.Errorf("AUTH PLAIN credentials = %q", creds)
	}
	if !strings.Contains(f.data, "Subject: Pemesanan dikonfirmasi") || !strings.Contains(f.data, "Message-ID: <outbox-9@desakita.id>") {
		t.Errorf("message data:\n%s", f.data)
	}
}

func TestSMTPRequiresStartTLSWhenConfigured(t *testing.T) {
	f := newFakeSMTP(t, false) // does not offer STARTTLS
	tr := SMTPTransport{Host: "127.0.0.1", Port: f.port(), TLS: TLSStartTLS, Timeout: 5 * time.Second}
	err := tr.Send(t.Context(), Envelope{From: testFrom(), To: "a@example.com", Subject: "s", Body: "b", MessageID: "<1@x>", Date: time.Now()})
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("err = %v, want refusal without STARTTLS", err)
	}
}

func TestSMTPDialError(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // nothing listens now
	err := SMTPTransport{Host: "127.0.0.1", Port: port, TLS: TLSNone, Timeout: time.Second}.Send(t.Context(),
		Envelope{From: testFrom(), To: "a@example.com", Subject: "s", Body: "b", MessageID: "<1@x>", Date: time.Now()})
	if err == nil || !strings.Contains(err.Error(), "dial") {
		t.Errorf("err = %v", err)
	}
}

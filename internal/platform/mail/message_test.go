package mail

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testFrom() *mail.Address { return &mail.Address{Name: "DesaKita", Address: "noreply@desakita.id"} }

func TestEnvelopeBytes(t *testing.T) {
	body := "Halo Sari,\n\nPemesanan Anda untuk “Menanam Padi” dikonfirmasi.\nTotal: Rp 300.000 — sampai jumpa!\n" + strings.Repeat("panjang ", 20)
	e := Envelope{From: testFrom(), To: "sari@example.com", Subject: "Pemesanan dikonfirmasi: Paket Ñandú", Body: body, MessageID: "<outbox-7@desakita.id>", Date: time.Date(2030, 3, 10, 10, 0, 0, 0, time.UTC)}
	raw, err := e.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	head, encoded, ok := bytes.Cut(raw, []byte("\r\n\r\n"))
	if !ok {
		t.Fatal("no header/body separator")
	}
	h := string(head)
	for _, want := range []string{"From: \"DesaKita\" <noreply@desakita.id>", "To: <sari@example.com>", "Message-ID: <outbox-7@desakita.id>",
		"Content-Type: text/plain; charset=UTF-8", "Content-Transfer-Encoding: quoted-printable", "MIME-Version: 1.0", "Auto-Submitted: auto-generated"} {
		if !strings.Contains(h, want) {
			t.Errorf("header missing %q in:\n%s", want, h)
		}
	}
	if strings.Contains(h, "Ñ") {
		t.Error("non-ASCII subject not encoded")
	}
	var subject string
	for _, line := range strings.Split(h, "\r\n") {
		if v, ok := strings.CutPrefix(line, "Subject: "); ok {
			subject = v
		}
	}
	if dec, err := new(mime.WordDecoder).DecodeHeader(subject); err != nil || dec != e.Subject {
		t.Errorf("subject round trip = %q, %v", dec, err)
	}
	for _, line := range strings.Split(string(encoded), "\r\n") {
		if len(line) > 76 {
			t.Errorf("body line longer than 76: %q", line)
		}
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.ReplaceAll(body, "\n", "\r\n"); string(decoded) != want {
		t.Errorf("body round trip mismatch:\n%q\n%q", decoded, want)
	}
}

func TestEnvelopeRejectsHeaderInjection(t *testing.T) {
	base := Envelope{From: testFrom(), To: "a@example.com", Subject: "ok", MessageID: "<x@y>", Body: "b"}
	cases := map[string]func(e *Envelope){
		"subject newline": func(e *Envelope) { e.Subject = "Halo\r\nBcc: victim@example.com" },
		"bad recipient":   func(e *Envelope) { e.To = "a@example.com\r\nBcc: v@example.com" },
		"message id":      func(e *Envelope) { e.MessageID = "<x@y>\nBcc: v@example.com" },
	}
	for name, mutate := range cases {
		e := base
		mutate(&e)
		if _, err := e.Bytes(); !errors.Is(err, ErrInvalidMessage) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestMessageValidate(t *testing.T) {
	ok := Message{Kind: "k", DedupeKey: "d", To: "a@example.com", Subject: "s", Body: "b"}
	if err := ok.validate(); err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]Message{
		"display name":  {Kind: "k", DedupeKey: "d", To: "A <a@example.com>", Subject: "s", Body: "b"},
		"not an email":  {Kind: "k", DedupeKey: "d", To: "nope", Subject: "s", Body: "b"},
		"subject break": {Kind: "k", DedupeKey: "d", To: "a@example.com", Subject: "a\nb", Body: "b"},
		"long subject":  {Kind: "k", DedupeKey: "d", To: "a@example.com", Subject: strings.Repeat("x", 201), Body: "b"},
		"no dedupe":     {Kind: "k", To: "a@example.com", Subject: "s", Body: "b"},
		"no body":       {Kind: "k", DedupeKey: "d", To: "a@example.com", Subject: "s"},
	} {
		if err := m.validate(); !errors.Is(err, ErrInvalidMessage) {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestMessageIDIsStable(t *testing.T) {
	if a, b := messageID(42, testFrom()), messageID(42, testFrom()); a != b || a != "<outbox-42@desakita.id>" {
		t.Errorf("message ids %q %q", a, b)
	}
}

func TestFileTransport(t *testing.T) {
	dir := t.TempDir()
	e := Envelope{From: testFrom(), To: "a@example.com", Subject: "Uji", Body: "Isi", MessageID: "<outbox-1@desakita.id>", Date: time.Now()}
	if err := (FileTransport{Dir: dir}).Send(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.eml"))
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	b, _ := os.ReadFile(files[0])
	if !bytes.Contains(b, []byte("Subject: Uji")) {
		t.Errorf("eml content: %s", b)
	}
}

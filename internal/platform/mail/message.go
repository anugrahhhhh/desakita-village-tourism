package mail

import (
	"bytes"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"
)

// Envelope is a message ready for a Transport.
type Envelope struct {
	From      *mail.Address
	To        string
	Subject   string
	Body      string
	MessageID string
	Date      time.Time
}

// Bytes renders e as an RFC 5322 message: UTF-8 plain text, quoted-printable body, RFC 2047
// encoded subject, CRLF line endings. Header values are validated against CR/LF injection.
func (e Envelope) Bytes() ([]byte, error) {
	to, err := mail.ParseAddress(e.To)
	if err != nil {
		return nil, fmt.Errorf("%w: bad recipient", ErrInvalidMessage)
	}
	for _, v := range []string{e.Subject, e.MessageID} {
		if strings.ContainsAny(v, "\r\n") {
			return nil, fmt.Errorf("%w: header contains a line break", ErrInvalidMessage)
		}
	}
	var b bytes.Buffer
	header := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	header("From", e.From.String())
	header("To", to.String())
	header("Subject", mime.QEncoding.Encode("utf-8", e.Subject))
	header("Date", e.Date.Format(time.RFC1123Z))
	header("Message-ID", e.MessageID)
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=UTF-8")
	header("Content-Transfer-Encoding", "quoted-printable")
	header("Auto-Submitted", "auto-generated")
	b.WriteString("\r\n")

	qp := quotedprintable.NewWriter(&b)
	body := strings.ReplaceAll(strings.ReplaceAll(e.Body, "\r\n", "\n"), "\n", "\r\n")
	if _, err := qp.Write([]byte(body)); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// messageID is stable per outbox row, so a message retried after a crash carries the same
// ID and receiving servers can drop the duplicate.
func messageID(outboxID int64, from *mail.Address) string {
	domain := "localhost"
	if i := strings.LastIndexByte(from.Address, '@'); i >= 0 && i < len(from.Address)-1 {
		domain = from.Address[i+1:]
	}
	return fmt.Sprintf("<outbox-%d@%s>", outboxID, domain)
}

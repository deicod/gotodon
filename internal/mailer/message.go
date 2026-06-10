package mailer

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"
)

// BuildMessage assembles an RFC 5322 message with a quoted-printable UTF-8
// plain-text body. The from and to addresses must be bare or named addresses
// parseable by net/mail.
func BuildMessage(from, to, subject, body string, now time.Time) ([]byte, error) {
	fromAddr, err := mail.ParseAddress(from)
	if err != nil {
		return nil, fmt.Errorf("invalid from address: %w", err)
	}
	toAddr, err := mail.ParseAddress(to)
	if err != nil {
		return nil, fmt.Errorf("invalid to address: %w", err)
	}

	messageID, err := generateMessageID(fromAddr.Address)
	if err != nil {
		return nil, err
	}

	var sb strings.Builder
	writeHeader(&sb, "From", fromAddr.String())
	writeHeader(&sb, "To", toAddr.String())
	writeHeader(&sb, "Subject", mime.QEncoding.Encode("utf-8", subject))
	writeHeader(&sb, "Date", now.UTC().Format(time.RFC1123Z))
	writeHeader(&sb, "Message-ID", messageID)
	writeHeader(&sb, "MIME-Version", "1.0")
	writeHeader(&sb, "Content-Type", `text/plain; charset="UTF-8"`)
	writeHeader(&sb, "Content-Transfer-Encoding", "quoted-printable")
	writeHeader(&sb, "Auto-Submitted", "auto-generated")
	sb.WriteString("\r\n")

	qp := quotedprintable.NewWriter(&crlfWriter{sb: &sb})
	if _, err := qp.Write([]byte(body)); err != nil {
		return nil, fmt.Errorf("encoding body: %w", err)
	}
	if err := qp.Close(); err != nil {
		return nil, fmt.Errorf("encoding body: %w", err)
	}

	return []byte(sb.String()), nil
}

func writeHeader(sb *strings.Builder, name, value string) {
	sb.WriteString(name)
	sb.WriteString(": ")
	sb.WriteString(value)
	sb.WriteString("\r\n")
}

func generateMessageID(fromAddress string) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generating message id: %w", err)
	}
	domain := "localhost"
	if at := strings.LastIndex(fromAddress, "@"); at >= 0 && at < len(fromAddress)-1 {
		domain = fromAddress[at+1:]
	}
	return fmt.Sprintf("<%s@%s>", hex.EncodeToString(random), domain), nil
}

// crlfWriter normalizes the LF line endings emitted by quotedprintable.Writer
// to the CRLF endings SMTP requires.
type crlfWriter struct {
	sb       *strings.Builder
	lastByte byte
}

func (w *crlfWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b == '\n' && w.lastByte != '\r' {
			w.sb.WriteByte('\r')
		}
		w.sb.WriteByte(b)
		w.lastByte = b
	}
	return len(p), nil
}

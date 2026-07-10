package email

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"path/filepath"
	"strings"
	"time"
)

// crlf is the canonical line terminator for SMTP/RFC 5321.
const crlf = "\r\n"

// encodeHeader encodes a header value per RFC 2047 (B-encoding) when it
// contains non-ASCII bytes (e.g. Cyrillic subjects). Pure-ASCII values pass
// through unchanged.
func encodeHeader(s string) string {
	if isASCII(s) {
		return s
	}
	return mime.BEncoding.Encode("utf-8", s)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			return false
		}
	}
	return true
}

// formatAddressList turns ["a@x", "b@x"] into "a@x, b@x".
func formatAddressList(addrs []string) string {
	return strings.Join(addrs, ", ")
}

// generateBoundary returns a random multipart boundary. Uses crypto/rand so
// the boundary cannot accidentally appear inside attachment content.
func generateBoundary() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read failing is exceptional; fall back to a timestamp-based
		// boundary so the message can still be built.
		return fmt.Sprintf("====poncho-%x====", time.Now().UnixNano())
	}
	return "====poncho-" + base64.StdEncoding.EncodeToString(b[:]) + "===="
}

// resolveContentType returns the attachment MIME type, auto-detecting by
// extension when Attachment.ContentType is empty.
func resolveContentType(filename, explicit string) string {
	if explicit != "" {
		return explicit
	}
	ext := filepath.Ext(filename)
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// buildBody constructs the text part(s) of the message:
//   - plain text only  -> simple text/plain part
//   - text + HTML      -> multipart/alternative wrapping both
//
// It returns the MIME type of the produced block and its serialized bytes.
func buildBody(msg Message) (contentType string, body []byte, err error) {
	switch {
	case msg.HTMLBody == "":
		// Plain text only.
		buf := &bytes.Buffer{}
		buf.WriteString("Content-Type: text/plain; charset=utf-8")
		buf.WriteString(crlf)
		buf.WriteString("Content-Transfer-Encoding: 8bit")
		buf.WriteString(crlf)
		buf.WriteString(crlf)
		buf.WriteString(msg.TextBody)
		return "text/plain; charset=utf-8", buf.Bytes(), nil

	default:
		// multipart/alternative: plain + HTML.
		buf := &bytes.Buffer{}
		boundary := generateBoundary()
		w := multipart.NewWriter(buf)
		if err := w.SetBoundary(boundary); err != nil {
			return "", nil, fmt.Errorf("email: set alternative boundary: %w", err)
		}

		textHeader := textproto.MIMEHeader{}
		textHeader.Set("Content-Type", "text/plain; charset=utf-8")
		textHeader.Set("Content-Transfer-Encoding", "8bit")
		pw, err := w.CreatePart(textHeader)
		if err != nil {
			return "", nil, fmt.Errorf("email: write text part: %w", err)
		}
		if _, err := io.WriteString(pw, msg.TextBody); err != nil {
			return "", nil, fmt.Errorf("email: write text body: %w", err)
		}

		htmlHeader := textproto.MIMEHeader{}
		htmlHeader.Set("Content-Type", "text/html; charset=utf-8")
		htmlHeader.Set("Content-Transfer-Encoding", "8bit")
		hw, err := w.CreatePart(htmlHeader)
		if err != nil {
			return "", nil, fmt.Errorf("email: write html part: %w", err)
		}
		if _, err := io.WriteString(hw, msg.HTMLBody); err != nil {
			return "", nil, fmt.Errorf("email: write html body: %w", err)
		}

		if err := w.Close(); err != nil {
			return "", nil, fmt.Errorf("email: close alternative part: %w", err)
		}
		return "multipart/alternative; boundary=" + boundary, buf.Bytes(), nil
	}
}

// buildAttachment serializes a single attachment as a multipart part using
// base64 transfer encoding (binary-safe for any content type).
func buildAttachment(w *multipart.Writer, att Attachment) error {
	if att.Filename == "" {
		return fmt.Errorf("email: attachment filename is required")
	}
	if att.Content == nil {
		return fmt.Errorf("email: attachment %q has no content", att.Filename)
	}
	ct := resolveContentType(att.Filename, att.ContentType)

	header := textproto.MIMEHeader{}
	// Split charset off the content type when adding a name parameter:
	// "text/csv; charset=utf-8" -> name goes after the media type.
	header.Set("Content-Type", ct)
	header.Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"`, att.Filename))
	header.Set("Content-Transfer-Encoding", "base64")

	pw, err := w.CreatePart(header)
	if err != nil {
		return fmt.Errorf("email: create attachment part %q: %w", att.Filename, err)
	}
	b64 := base64.NewEncoder(base64.StdEncoding, pw)
	if _, err := b64.Write(att.Content); err != nil {
		_ = b64.Close()
		return fmt.Errorf("email: encode attachment %q: %w", att.Filename, err)
	}
	if err := b64.Close(); err != nil {
		return fmt.Errorf("email: finalize attachment %q: %w", att.Filename, err)
	}
	return nil
}

// buildMessage assembles the full RFC 5322 message (headers + body) ready to
// be written to the SMTP DATA stream.
//
// Header policy:
//   - From, To always present.
//   - Cc present only when there is at least one Cc recipient.
//   - Bcc present only when keepBCC is true (keepbcc off = Bcc stays hidden).
//
// Structure:
//
//	no attachments, text only   -> text/plain
//	no attachments, text+html   -> multipart/alternative
//	attachments present         -> multipart/mixed { body-block, attachments... }
func buildMessage(msg Message, from string, to, cc, bcc []string, keepBCC bool) ([]byte, error) {
	if strings.TrimSpace(msg.TextBody) == "" {
		return nil, fmt.Errorf("email: text body is required")
	}

	var buf bytes.Buffer

	// --- Headers ---
	writeHeader := func(key, val string) {
		buf.WriteString(key)
		buf.WriteString(": ")
		buf.WriteString(val)
		buf.WriteString(crlf)
	}
	writeHeader("From", from)
	writeHeader("To", formatAddressList(to))
	if len(cc) > 0 {
		writeHeader("Cc", formatAddressList(cc))
	}
	if keepBCC && len(bcc) > 0 {
		writeHeader("Bcc", formatAddressList(bcc))
	}
	writeHeader("Subject", encodeHeader(msg.Subject))
	writeHeader("MIME-Version", "1.0")
	writeHeader("Date", time.Now().Format(time.RFC1123Z))
	writeHeader("Message-ID", generateMessageID(from))

	// --- Body assembly ---
	bodyCT, bodyBytes, err := buildBody(msg)
	if err != nil {
		return nil, err
	}

	if len(msg.Attachments) == 0 {
		// Simple message: body is the whole payload.
		buf.WriteString("Content-Type: ")
		buf.WriteString(bodyCT)
		buf.WriteString(crlf)
		buf.WriteString(crlf) // blank line separates headers from body
		buf.Write(bodyBytes)
		return buf.Bytes(), nil
	}

	// multipart/mixed: first part = body block, then attachments.
	boundary := generateBoundary()
	buf.WriteString("Content-Type: multipart/mixed; boundary=")
	buf.WriteString(boundary)
	buf.WriteString(crlf)
	buf.WriteString(crlf)

	mw := multipart.NewWriter(&buf)
	if err := mw.SetBoundary(boundary); err != nil {
		return nil, fmt.Errorf("email: set mixed boundary: %w", err)
	}

	// Body block: write its own Content-Type then its serialized bytes.
	bodyHeader := textproto.MIMEHeader{}
	bodyHeader.Set("Content-Type", bodyCT)
	partWriter, err := mw.CreatePart(bodyHeader)
	if err != nil {
		return nil, fmt.Errorf("email: create body part: %w", err)
	}
	if _, err := partWriter.Write(bodyBytes); err != nil {
		return nil, fmt.Errorf("email: write body part: %w", err)
	}

	for _, att := range msg.Attachments {
		if err := buildAttachment(mw, att); err != nil {
			return nil, err
		}
	}

	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("email: close mixed part: %w", err)
	}
	return buf.Bytes(), nil
}

// generateMessageID builds an RFC 5322 Message-ID using the domain of the
// From address and a random token.
func generateMessageID(from string) string {
	domain := "localhost"
	if at := strings.LastIndex(from, "@"); at >= 0 && at < len(from)-1 {
		domain = from[at+1:]
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("<poncho.%d@%s>", time.Now().UnixNano(), domain)
	}
	return fmt.Sprintf("<poncho.%x@%s>", b[:], domain)
}

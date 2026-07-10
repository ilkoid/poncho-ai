package email

// Message describes a single email to send.
//
// Recipient fields (To, Cc, Bcc): if a field is empty, the corresponding
// list from Config.Recipients is used. If non-empty, it fully overrides the
// config (it does NOT merge). This keeps per-message targeting predictable.
type Message struct {
	To          []string     // override config recipients.to when non-empty
	Cc          []string     // override config recipients.cc when non-empty
	Bcc         []string     // override config recipients.bcc when non-empty
	Subject     string       // message subject
	TextBody    string       // plain-text body (required)
	HTMLBody    string       // optional HTML body; when set, body becomes multipart/alternative
	Attachments []Attachment // optional attachments; when present, whole message becomes multipart/mixed
}

// Attachment is a binary file attached to a message.
type Attachment struct {
	Filename    string // shown to recipient, e.g. "report.csv" (required)
	Content     []byte // raw attachment bytes (required)
	ContentType string // MIME type, e.g. "text/csv". Empty = auto-detect by extension
}

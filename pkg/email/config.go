// Configuration types for the email package.
//
// Unlike most pkg/ types in this repo, email config has NO defaults: every
// mandatory field must be present in the YAML, otherwise NewSender fails fast
// via Validate(). This is deliberate — misconfigured mail silently going to
// the wrong server or with a missing password is worse than a loud startup
// error. Secrets (password) are kept as ${ENV} references expanded by
// pkg/config.LoadYAML, matching the project convention (${PG_PWD} etc.).
package email

import (
	"fmt"
	"strings"
	"time"
)

// Valid TLS modes. tls_mode is a YAML string so the mode can be changed
// without touching code.
const (
	tlsModeImplicit = "implicit" // port 465: TLS handshake immediately on connect (SMTPS)
	tlsModeStartTLS = "starttls" // port 587: plain connect, then STARTTLS upgrade
	tlsModeNone     = "none"     // no encryption (plaintext) — only for local testing
)

// Config is the top-level email config. Load it with config.LoadYAML.
type Config struct {
	SMTP       SMTPConfig `yaml:"smtp"`
	Recipients Recipients `yaml:"recipients"`
}

// SMTPConfig holds server connection and authentication settings.
//
// Field-by-field mapping from the sysadmin-provided msmtp config:
//
//	host            -> Host
//	port            -> Port
//	from            -> From
//	user            -> Username
//	password        -> Password (store as ${SMTP_PASSWORD} in YAML)
//	auth on         -> Auth: true
//	tls on          -> TLSMode: "implicit" (for port 465)
//	tls_starttls on -> redundant on 465; ignored. Use TLSMode: "starttls" on 587.
//	tls_certcheck off -> TLSCertCheck: false (skip cert verification)
//	keepbcc off     -> KeepBCC: false (Bcc recipients stay hidden in headers)
type SMTPConfig struct {
	Host          string        `yaml:"host"`           // SMTP server address (required)
	Port          int           `yaml:"port"`           // SMTP server port, 1..65535 (required)
	Timeout       time.Duration `yaml:"timeout"`        // connect/send timeout (required)
	From          string        `yaml:"from"`           // envelope + header From address (required)
	Username      string        `yaml:"username"`       // SMTP auth login (required)
	Password      string        `yaml:"password"`       // SMTP auth password, ${SMTP_PASSWORD} (required)
	Auth          bool          `yaml:"auth"`           // perform SMTP AUTH when true
	AuthMechanism string        `yaml:"auth_mechanism"` // plain (default) | login (MS Exchange и др., не принимающие AUTH PLAIN → 504)
	TLSMode       string        `yaml:"tls_mode"`       // implicit | starttls | none (required)
	TLSCertCheck  bool          `yaml:"tls_certcheck"`  // verify server TLS cert when true
	KeepBCC       bool          `yaml:"keepbcc"`        // include Bcc in message headers when true
}

// Recipients is the default recipient list applied to every message when the
// Message itself does not override the corresponding field.
type Recipients struct {
	To  []string `yaml:"to"`
	Cc  []string `yaml:"cc"`
	Bcc []string `yaml:"bcc"`
}

// Validate enforces that every mandatory field is set. It returns a wrapped
// error with a human-readable description of the first missing/invalid field.
func (c Config) Validate() error {
	s := c.SMTP
	if s.Host == "" {
		return fmt.Errorf("email config: smtp.host is required")
	}
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("email config: smtp.port must be in 1..65535, got %d", s.Port)
	}
	if s.Timeout <= 0 {
		return fmt.Errorf("email config: smtp.timeout is required and must be positive")
	}
	if s.From == "" {
		return fmt.Errorf("email config: smtp.from is required")
	}
	if s.Username == "" {
		return fmt.Errorf("email config: smtp.username is required")
	}
	if s.Password == "" {
		return fmt.Errorf("email config: smtp.password is required (use ${SMTP_PASSWORD})")
	}
	switch s.TLSMode {
	case tlsModeImplicit, tlsModeStartTLS, tlsModeNone:
	default:
		return fmt.Errorf("email config: smtp.tls_mode must be one of implicit|starttls|none, got %q", s.TLSMode)
	}
	switch strings.ToLower(s.AuthMechanism) {
	case "", "plain", "login": // пусто = plain (по умолчанию)
	default:
		return fmt.Errorf("email config: smtp.auth_mechanism must be one of plain|login, got %q", s.AuthMechanism)
	}
	if len(c.Recipients.To) == 0 {
		return fmt.Errorf("email config: recipients.to must contain at least one address")
	}
	return nil
}

package email

import (
	"strings"
	"testing"
	"time"
)

// validConfig returns a fully-populated Config that passes Validate.
// Tests mutate a copy to exercise specific failure paths.
func validConfig() Config {
	return Config{
		SMTP: SMTPConfig{
			Host:         "10.120.11.31",
			Port:         465,
			Timeout:      30 * time.Second,
			From:         "ai-tools@playtoday.ru",
			Username:     "it_service@playtoday.ru",
			Password:     "secret",
			Auth:         true,
			TLSMode:      "implicit",
			TLSCertCheck: false,
			KeepBCC:      false,
		},
		Recipients: Recipients{
			To: []string{"ops@playtoday.ru"},
		},
	}
}

func TestValidate_OK(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestValidate_MissingFields(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantSub string
	}{
		{"host", func(c *Config) { c.SMTP.Host = "" }, "smtp.host"},
		{"port zero", func(c *Config) { c.SMTP.Port = 0 }, "smtp.port"},
		{"port huge", func(c *Config) { c.SMTP.Port = 70000 }, "smtp.port"},
		{"timeout", func(c *Config) { c.SMTP.Timeout = 0 }, "smtp.timeout"},
		{"from", func(c *Config) { c.SMTP.From = "" }, "smtp.from"},
		{"username", func(c *Config) { c.SMTP.Username = "" }, "smtp.username"},
		{"password", func(c *Config) { c.SMTP.Password = "" }, "smtp.password"},
		{"tls_mode", func(c *Config) { c.SMTP.TLSMode = "weird" }, "smtp.tls_mode"},
		{"no recipients.to", func(c *Config) { c.Recipients.To = nil }, "recipients.to"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			tc.mutate(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("expected error mentioning %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestValidate_TLSModes(t *testing.T) {
	for _, mode := range []string{tlsModeImplicit, tlsModeStartTLS, tlsModeNone} {
		t.Run(mode, func(t *testing.T) {
			c := validConfig()
			c.SMTP.TLSMode = mode
			if err := c.Validate(); err != nil {
				t.Fatalf("mode %q should be valid, got %v", mode, err)
			}
		})
	}
}

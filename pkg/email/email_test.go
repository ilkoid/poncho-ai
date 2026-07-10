package email

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNewSender_RejectsInvalidConfig(t *testing.T) {
	bad := validConfig()
	bad.SMTP.Host = ""
	if _, err := NewSender(bad); err == nil {
		t.Fatal("expected error for invalid config")
	}
}

func TestNewSender_AcceptsValidConfig(t *testing.T) {
	s, err := NewSender(validConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s == nil {
		t.Fatal("nil sender")
	}
}

func TestAllRecipients_DedupAndTrim(t *testing.T) {
	got := allRecipients(
		[]string{"a@x", " b@x ", ""},
		[]string{"b@x", "c@x"},
		[]string{"a@x"},
	)
	want := []string{"a@x", "b@x", "c@x"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestOverrideOr(t *testing.T) {
	fallback := []string{"cfg@x"}
	if got := overrideOr(nil, fallback); len(got) != 1 || got[0] != "cfg@x" {
		t.Errorf("empty override should fall back, got %v", got)
	}
	if got := overrideOr([]string{"msg@x"}, fallback); len(got) != 1 || got[0] != "msg@x" {
		t.Errorf("non-empty override should win, got %v", got)
	}
}

// TestSend_RecipientResolution exercises the full pre-send path (recipient
// resolution + message build) without connecting to a real SMTP server. We
// point the sender at a closed port so deliver fails AFTER the message is
// assembled; we then assert the failure is a connection error (meaning the
// build path succeeded) and that recipients were resolved.
func TestSend_RecipientResolutionFromConfig(t *testing.T) {
	c := validConfig()
	// Point at a guaranteed-closed port so we observe a dial error rather
	// than a real send.
	c.SMTP.Host = "127.0.0.1"
	c.SMTP.Port = 1 // privileged + unused in tests
	c.SMTP.TLSCertCheck = false
	c.SMTP.Timeout = 1 // short
	c.Recipients = Recipients{
		To:  []string{"to@x"},
		Cc:  []string{"cc@x"},
		Bcc: []string{"bcc@x"},
	}

	s, err := NewSender(c)
	if err != nil {
		t.Fatalf("NewSender: %v", err)
	}

	// Message has no To/Cc/Bcc -> config defaults apply. Build succeeds, send
	// fails at dial.
	err = s.Send(context.Background(), Message{
		Subject:  "test",
		TextBody: "hello",
	})
	if err == nil {
		t.Fatal("expected dial error on closed port, got nil")
	}
	// The error must be a connection failure, NOT a build/validation error.
	low := strings.ToLower(err.Error())
	if !strings.Contains(low, "dial") && !strings.Contains(low, "connection") {
		t.Fatalf("expected dial/connection error, got %v", err)
	}
}

func TestSend_MessageOverridesConfigRecipients(t *testing.T) {
	c := validConfig()
	c.SMTP.Host = "127.0.0.1"
	c.SMTP.Port = 1
	c.SMTP.Timeout = 1
	s, err := NewSender(c)
	if err != nil {
		t.Fatalf("NewSender: %v", err)
	}

	// Override To with an empty list is NOT possible via nil (that triggers
	// the fallback); to assert override semantics we set a concrete list and
	// confirm the message is accepted for send (fails at dial only).
	err = s.Send(context.Background(), Message{
		To:       []string{"custom@x"},
		Subject:  "s",
		TextBody: "b",
	})
	if err == nil {
		t.Fatal("expected dial error on closed port")
	}
	// Ensure it was not a recipient-resolution error.
	if errors.Is(err, ErrNoRecipient) {
		t.Fatalf("override should satisfy recipient requirement: %v", err)
	}
}

func TestSend_EmptyTextBodyFailsBeforeDial(t *testing.T) {
	c := validConfig()
	s, err := NewSender(c)
	if err != nil {
		t.Fatalf("NewSender: %v", err)
	}
	err = s.Send(context.Background(), Message{Subject: "s", TextBody: ""})
	if err == nil {
		t.Fatal("expected error for empty text body")
	}
	if !strings.Contains(err.Error(), "text body") {
		t.Fatalf("unexpected error: %v", err)
	}
}

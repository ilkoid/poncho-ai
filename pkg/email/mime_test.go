package email

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestBuildMessage_PlainTextOnly(t *testing.T) {
	raw, err := buildMessage(
		Message{Subject: "Hello", TextBody: "plain body"},
		"from@x", []string{"to@x"}, nil, nil, false,
	)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, "Content-Type: text/plain; charset=utf-8") {
		t.Errorf("missing text/plain content type:\n%s", s)
	}
	if !strings.Contains(s, "Subject: Hello") {
		t.Errorf("missing Subject header:\n%s", s)
	}
	if !strings.Contains(s, "From: from@x") {
		t.Errorf("missing From header:\n%s", s)
	}
	if !strings.Contains(s, "To: to@x") {
		t.Errorf("missing To header:\n%s", s)
	}
	if !strings.Contains(s, "plain body") {
		t.Errorf("missing body:\n%s", s)
	}
	if strings.Contains(s, "multipart") {
		t.Errorf("plain text should not be multipart:\n%s", s)
	}
}

func TestBuildMessage_Alternative(t *testing.T) {
	raw, err := buildMessage(
		Message{Subject: "S", TextBody: "txt", HTMLBody: "<p>html</p>"},
		"from@x", []string{"to@x"}, nil, nil, false,
	)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, "multipart/alternative") {
		t.Errorf("expected multipart/alternative:\n%s", s)
	}
	if !strings.Contains(s, "text/plain; charset=utf-8") {
		t.Errorf("missing text part:\n%s", s)
	}
	if !strings.Contains(s, "text/html; charset=utf-8") {
		t.Errorf("missing html part:\n%s", s)
	}
}

func TestBuildMessage_WithAttachment(t *testing.T) {
	content := []byte("col1,col2\n1,2\n")
	raw, err := buildMessage(
		Message{
			Subject:  "Report",
			TextBody: "see attachment",
			Attachments: []Attachment{{
				Filename: "report.csv",
				Content:  content,
			}},
		},
		"from@x", []string{"to@x"}, nil, nil, false,
	)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, "multipart/mixed") {
		t.Errorf("expected multipart/mixed:\n%s", s)
	}
	if !strings.Contains(s, `filename="report.csv"`) {
		t.Errorf("missing attachment disposition:\n%s", s)
	}
	// base64-encoded content must be present.
	wantB64 := base64.StdEncoding.EncodeToString(content)
	if !strings.Contains(s, wantB64) {
		t.Errorf("missing base64 attachment payload (want %s):\n%s", wantB64, s)
	}
}

func TestBuildMessage_KeepBCCControlsHeader(t *testing.T) {
	bcc := []string{"secret@x"}

	off, _ := buildMessage(
		Message{Subject: "S", TextBody: "b"},
		"from@x", []string{"to@x"}, nil, bcc, false, // keepBCC off
	)
	if strings.Contains(string(off), "Bcc:") {
		t.Errorf("keepBCC=false should omit Bcc header:\n%s", off)
	}

	on, _ := buildMessage(
		Message{Subject: "S", TextBody: "b"},
		"from@x", []string{"to@x"}, nil, bcc, true, // keepBCC on
	)
	if !strings.Contains(string(on), "Bcc: secret@x") {
		t.Errorf("keepBCC=true should include Bcc header:\n%s", on)
	}
}

func TestBuildMessage_CCHeaderOnlyWhenPresent(t *testing.T) {
	withCC, _ := buildMessage(
		Message{Subject: "S", TextBody: "b"},
		"from@x", []string{"to@x"}, []string{"cc@x"}, nil, false,
	)
	if !strings.Contains(string(withCC), "Cc: cc@x") {
		t.Errorf("Cc header missing:\n%s", withCC)
	}

	noCC, _ := buildMessage(
		Message{Subject: "S", TextBody: "b"},
		"from@x", []string{"to@x"}, nil, nil, false,
	)
	if strings.Contains(string(noCC), "Cc:") {
		t.Errorf("Cc header should be omitted when empty:\n%s", noCC)
	}
}

func TestBuildMessage_EncodesNonASCIISubject(t *testing.T) {
	raw, err := buildMessage(
		Message{Subject: "Отчёт", TextBody: "x"},
		"from@x", []string{"to@x"}, nil, nil, false,
	)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	s := string(raw)
	// RFC 2047 B-encoding marker for UTF-8.
	if !strings.Contains(s, "Subject: =?utf-8?b?") {
		t.Errorf("non-ASCII subject not encoded:\n%s", s)
	}
}

func TestBuildMessage_RequiresTextBody(t *testing.T) {
	if _, err := buildMessage(
		Message{Subject: "S", TextBody: "  "},
		"from@x", []string{"to@x"}, nil, nil, false,
	); err == nil {
		t.Fatal("expected error for empty text body")
	}
}

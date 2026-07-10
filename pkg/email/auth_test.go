package email

import (
	"net/smtp"
	"testing"
)

func TestLoginAuth_StartMechanism(t *testing.T) {
	a := &loginAuth{username: "u", password: "p"}
	mech, resp, err := a.Start(&smtp.ServerInfo{Name: "host", TLS: true})
	if err != nil {
		t.Fatalf("Start: unexpected error: %v", err)
	}
	if mech != "LOGIN" {
		t.Fatalf("Start mechanism = %q, want %q", mech, "LOGIN")
	}
	if resp != nil {
		t.Fatalf("Start initial response = %v, want nil (no initial-response; server prompts first)", resp)
	}
}

func TestLoginAuth_NextChallenges(t *testing.T) {
	a := &loginAuth{username: "alice", password: "secret"}
	// net/smtp декодирует base64-челлендж сервера и передаёт в Next расшифрованные байты.
	// Матчим по содержимому — устойчиво к вариациям формулировок и регистра.
	cases := []struct {
		challenge string
		want      string
	}{
		{"Username:", "alice"},
		{"User name ", "alice"}, // вариация формулировки
		{"Password:", "secret"},
		{"password:", "secret"}, // регистронезависимо
	}
	for _, c := range cases {
		got, err := a.Next([]byte(c.challenge), true)
		if err != nil {
			t.Fatalf("Next(%q): unexpected error: %v", c.challenge, err)
		}
		if string(got) != c.want {
			t.Fatalf("Next(%q) = %q, want %q", c.challenge, got, c.want)
		}
	}
	// more=false — конец auth-обмена (сервер ответил 235), Next возвращает nil.
	if got, err := a.Next([]byte("235 OK"), false); err != nil || got != nil {
		t.Fatalf("Next(more=false) = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestLoginAuth_UnexpectedChallenge(t *testing.T) {
	a := &loginAuth{username: "u", password: "p"}
	if _, err := a.Next([]byte("totally unexpected prompt"), true); err == nil {
		t.Fatal("expected error for an unrecognized AUTH LOGIN challenge")
	}
}

func TestSelectAuth_MechanismChoice(t *testing.T) {
	base := SMTPConfig{Host: "h", Username: "u", Password: "p"}

	// "login" (любой регистр) → *loginAuth.
	for _, m := range []string{"login", "LOGIN", "Login"} {
		a := selectAuth(SMTPConfig{Host: base.Host, Username: base.Username, Password: base.Password, AuthMechanism: m})
		if _, ok := a.(*loginAuth); !ok {
			t.Fatalf("auth_mechanism %q: expected *loginAuth, got %T", m, a)
		}
	}

	// "" / "plain" → НЕ *loginAuth (это PlainAuth из net/smtp).
	for _, m := range []string{"", "plain"} {
		a := selectAuth(SMTPConfig{Host: base.Host, Username: base.Username, Password: base.Password, AuthMechanism: m})
		if a == nil {
			t.Fatalf("auth_mechanism %q: nil auth", m)
		}
		if _, ok := a.(*loginAuth); ok {
			t.Fatalf("auth_mechanism %q: should NOT be *loginAuth (plain path)", m)
		}
	}
}

package email

import (
	"fmt"
	"net/smtp"
	"strings"
)

// loginAuth реализует smtp.Auth для механизма AUTH LOGIN.
//
// Нужен для серверов, которые НЕ принимают AUTH PLAIN, — в первую очередь Microsoft Exchange:
// после STARTTLS он рекламирует только AUTH LOGIN (и иногда NTLM), а на AUTH PLAIN отвечает
// «504 5.7.4 Unrecognized authentication type». Go-stdlib даёт лишь PlainAuth, поэтому LOGIN
// реализуем сами. Контракт net/smtp (Client.Auth): на 334-челлендже Go декодирует base64 и
// передаёт в Next уже расшифрованные байты — поэтому матчим по содержимому challenge.
type loginAuth struct {
	username, password string
}

// Start объявляет механизм "LOGIN" без initial-response: сервер сам пришлёт challenge
// «Username:», на который Next ответит логином.
func (a *loginAuth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", nil, nil
}

// Next отвечает на два декодированных challenge сервера. AUTH LOGIN всегда двухступенчатый
// (сначала логин, затем пароль); матчим по содержимому lenient (case-insensitive), чтобы быть
// устойчивыми к вариациям формулировок («Username:»/«User name»/«Password:» и т.п.).
func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	ch := strings.ToLower(string(fromServer))
	switch {
	case strings.Contains(ch, "user"):
		return []byte(a.username), nil
	case strings.Contains(ch, "pass"):
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("email: unexpected AUTH LOGIN challenge: %q", fromServer)
}

// selectAuth выбирает smtp.Auth по SMTPConfig.AuthMechanism.
//
//	"login"        → AUTH LOGIN (MS Exchange и др., не принимающие PLAIN)
//	"" / "plain"   → AUTH PLAIN (по умолчанию; текущее поведение для большинства серверов)
func selectAuth(sm SMTPConfig) smtp.Auth {
	if strings.EqualFold(sm.AuthMechanism, "login") {
		return &loginAuth{username: sm.Username, password: sm.Password}
	}
	return smtp.PlainAuth("", sm.Username, sm.Password, sm.Host)
}

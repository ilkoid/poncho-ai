// mail-notify — отправка статусного письма через pkg/email (CLI-обёртка для скриптов).
//
// Вызывается ночным конвейером (download-all-v2.sh, mail_send) и вручную.
// Тело письма — из stdin: printf '%s' "$text" | mail-notify --subject "..." [--to a,b]
// SMTP-подключение и получатели по умолчанию — из config.yaml рядом с main.go
// (проверенные настройки внутреннего MS Exchange relay; пароль — ${SMTP_PASSWORD}
// из env, разворачивается config.LoadYAML + os.ExpandEnv — как у PG_PWD).
// Флаг --to переопределяет получателей конфига (не мерджится — см. email.Message).
// Флаг --selftest отправляет тестовое письмо (stdin/subject не нужны).
//
// Почему не curl smtp://: по IP relay curl даёт exit 60 (сертификат CN не
// совпадает с IP), по хостнейму — exit 94 (AUTH-механизм Exchange). pkg/email
// с tls_certcheck: false работает с этим relay годами (collection-readiness).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/email"
)

func main() {
	configPath := flag.String("config", "config.yaml", "путь к YAML с секциями smtp/recipients (типы pkg/email)")
	subject := flag.String("subject", "", "тема письма (обязательна без --selftest)")
	to := flag.String("to", "", "переопределить получателей: адреса через запятую (иначе recipients.to из конфига)")
	selftest := flag.Bool("selftest", false, "отправить тестовое письмо (stdin и --subject не нужны)")
	flag.Parse()

	var text string
	if *selftest {
		host, _ := os.Hostname()
		*subject = fmt.Sprintf("mail-notify selftest (%s)", host)
		text = fmt.Sprintf("Тестовый прогон утилиты mail-notify.\nhost: %s\ntime: %s\n", host, time.Now().Format("2006-01-02 15:04:05"))
	} else {
		if *subject == "" {
			fmt.Fprintln(os.Stderr, "FAIL: --subject обязателен (или используй --selftest)")
			flag.Usage()
			os.Exit(2)
		}
		body, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: чтение тела из stdin: %v\n", err)
			os.Exit(2)
		}
		text = string(body)
		if strings.TrimSpace(text) == "" {
			fmt.Fprintln(os.Stderr, "FAIL: тело письма пустое (stdin)")
			os.Exit(2)
		}
	}

	var cfg email.Config
	if err := config.LoadYAML(*configPath, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: конфиг %s: %v\n", *configPath, err)
		os.Exit(1)
	}

	msg := email.Message{
		Subject:  *subject,
		TextBody: text,
	}
	if strings.TrimSpace(*to) != "" {
		for _, addr := range strings.Split(*to, ",") {
			if addr = strings.TrimSpace(addr); addr != "" {
				msg.To = append(msg.To, addr)
			}
		}
	}

	sender, err := email.NewSender(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: конфигурация SMTP (%s): %v\n", *configPath, err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := sender.Send(ctx, msg); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: отправка письма: %v\n", err)
		os.Exit(1)
	}

	recip := cfg.Recipients.To
	if len(msg.To) > 0 {
		recip = msg.To
	}
	fmt.Printf("OK: письмо отправлено (%s → %s)\n", cfg.SMTP.From, strings.Join(recip, ", "))
}

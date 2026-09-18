// mail-notify — отправка статусного письма через pkg/email (CLI-обёртка для скриптов).
//
// Вызывается ночным конвейером (download-all-v2.sh, mail_send) и вручную.
// SMTP-подключение и получатели по умолчанию — из config.yaml рядом с main.go
// (проверенные настройки внутреннего MS Exchange relay; пароль — ${SMTP_PASSWORD}
// из env, разворачивается config.LoadYAML + os.ExpandEnv — как у PG_PWD).
// Флаг --to переопределяет получателей конфига (не мерджится — см. email.Message).
//
// Почему не curl smtp://: relay отдаёт сертификат, который curl на части машин
// не принимает (exit 60), и требует AUTH LOGIN после STARTTLS — pkg/email
// обрабатывает оба случая и работает с этим relay ежедневно (collection-readiness).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/email"
)

func main() {
	configPath := flag.String("config", "config.yaml", "путь к YAML с секциями smtp/recipients (типы pkg/email)")
	subject := flag.String("subject", "", "тема письма (обязателен)")
	text := flag.String("text", "", "текст письма, plain-text (обязателен)")
	to := flag.String("to", "", "переопределить получателей: адреса через запятую (иначе recipients.to из конфига)")
	flag.Parse()

	if *subject == "" || *text == "" {
		fmt.Fprintln(os.Stderr, "FAIL: --subject и --text обязательны")
		flag.Usage()
		os.Exit(2)
	}

	var cfg email.Config
	if err := config.LoadYAML(*configPath, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: конфиг %s: %v\n", *configPath, err)
		os.Exit(1)
	}

	msg := email.Message{
		Subject:  *subject,
		TextBody: *text,
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

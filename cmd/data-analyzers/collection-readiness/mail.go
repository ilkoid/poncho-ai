// mail.go — отправка готового xlsx-отчёта по почте через pkg/email.
//
// Срабатывает после сохранения xlsx, когда Email.Enabled=true ИЛИ передан флаг --mail
// (решение принимает main, здесь — только сама отправка). Подключение SMTP и список
// получателей берутся из EmailConfig (config.yaml → секция email); Content-Type
// вложения (.xlsx) автоопределяется pkg/email через mime.TypeByExtension.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ilkoid/poncho-ai/pkg/email"
)

// sendReport читает xlsx (path) и отправляет его вложением на адреса из EmailConfig.
//
// Тема/текст: если в конфиге пусто — подставляется осмысленное значение по умолчанию
// (тема включает название коллекции/сезона). Ошибка конфигурации SMTP (Validate) и
// ошибка отправки различаются в сообщении, чтобы было видно, где именно упало.
func sendReport(ctx context.Context, ec EmailConfig, path string, collections, seasons []string) error {
	xlsxBytes, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("чтение xlsx для вложения (%s): %w", path, err)
	}

	// Собираем email.Config из локальной обёртки и валидируем через NewSender.
	sender, err := email.NewSender(email.Config{
		SMTP:       ec.SMTP,
		Recipients: ec.Recipients,
	})
	if err != nil {
		return fmt.Errorf("конфигурация SMTP (проверьте секцию email в config.yaml): %w", err)
	}

	subject := ec.Subject
	if subject == "" {
		subject = "Отчёт готовности карточек — " + filterLabel(collections, seasons)
	}
	body := ec.Body
	if body == "" {
		body = "Во вложении — отчёт готовности карточек."
	}

	msg := email.Message{
		Subject:  subject,
		TextBody: body,
		Attachments: []email.Attachment{{
			Filename: filepath.Base(path),
			Content:  xlsxBytes,
		}},
	}
	if err := sender.Send(ctx, msg); err != nil {
		return fmt.Errorf("отправка письма: %w", err)
	}
	return nil
}

// filterLabel — короткая подпись фильтра для темы по умолчанию: первая коллекция,
// иначе первый сезон, иначе «все».
func filterLabel(collections, seasons []string) string {
	if len(collections) > 0 {
		return collections[0]
	}
	if len(seasons) > 0 {
		return seasons[0]
	}
	return "все"
}

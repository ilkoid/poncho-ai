// mail.go — отправка готового xlsx-отчёта по почте через pkg/email.
//
// Копия паттерна mail.go из collection-readiness. Срабатывает после сохранения
// xlsx, когда Email.Enabled=true ИЛИ передан флаг --mail (решает main).
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/email"
)

// sendReport читает xlsx (path) и отправляет его вложением на адреса из EmailConfig.
func sendReport(ctx context.Context, ec EmailConfig, path string, watchTotal, escalated int) error {
	xlsxBytes, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("чтение xlsx для вложения (%s): %w", path, err)
	}

	sender, err := email.NewSender(email.Config{
		SMTP:       ec.SMTP,
		Recipients: ec.Recipients,
	})
	if err != nil {
		return fmt.Errorf("конфигурация SMTP (проверьте секцию email в config.yaml): %w", err)
	}

	subject := ec.Subject
	if subject == "" {
		subject = "Ранняя диагностика — карточки без заказов"
	}
	body := ec.Body
	if body == "" {
		body = fmt.Sprintf(
			"Во вложении — карточки без заказов по критериям гендира (%d поз., эскалация 7+ дней: %d). Срез от %s.",
			watchTotal, escalated, time.Now().In(mskTZ).Format("02.01.2006"))
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

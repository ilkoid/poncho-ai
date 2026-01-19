// Package questions предоставляет типы для интерактивных вопросов пользователю.
//
// Используется инструментом ask_user_question для организации
// взаимодействия LLM с пользователем через выбор вариантов ответов.
package questions

import (
	"fmt"
	"time"
)

// QuestionOption представляет один вариант ответа на вопрос.
type QuestionOption struct {
	Label       string `json:"label"`        // Короткий текст (например "12612157")
	Description string `json:"description"`  // Пояснение (например "Платье женское")
}

// QuestionAnswer представляет ответ пользователя на вопрос.
type QuestionAnswer struct {
	Index       int       // Индекс выбранного варианта (0-based)
	Label       string    // Текст выбранного варианта
	Description string    // Описание выбранного варианта
	Timestamp   time.Time // Время ответа
}

// PendingQuestion представляет ожидающий ответ вопрос.
type PendingQuestion struct {
	ID        string
	Question  string
	Options   []QuestionOption
	CreatedAt time.Time
}

// Validate проверяет валидность вопроса.
func (pq *PendingQuestion) Validate(maxOptions int) error {
	if pq.Question == "" {
		return fmt.Errorf("question cannot be empty")
	}
	if len(pq.Options) == 0 {
		return fmt.Errorf("at least one option required")
	}
	if len(pq.Options) > maxOptions {
		return fmt.Errorf("too many options: %d (max %d)", len(pq.Options), maxOptions)
	}
	for i, opt := range pq.Options {
		if opt.Label == "" {
			return fmt.Errorf("option %d: label cannot be empty", i)
		}
	}
	return nil
}

// IsValidIndex проверяет что индекс в допустимом диапазоне.
func (pq *PendingQuestion) IsValidIndex(index int) bool {
	return index >= 0 && index < len(pq.Options)
}

// GetOption возвращает опцию по индексу.
func (pq *PendingQuestion) GetOption(index int) (QuestionOption, bool) {
	if !pq.IsValidIndex(index) {
		return QuestionOption{}, false
	}
	return pq.Options[index], true
}

// QuestionResult представляет результат问答 (ответ или ошибка).
type QuestionResult struct {
	Status      string // "answered", "cancelled", "timeout"
	Index       int    // Индекс выбранного варианта (для status="answered")
	Label       string // Выбранный label
	Description string // Выбранное description
	Error       string // Ошибка (для status="cancelled", "timeout")
	Timestamp   time.Time
}

// NewAnsweredResult создает результат с ответом пользователя.
func NewAnsweredResult(answer QuestionAnswer) QuestionResult {
	opt := answer.Label
	if answer.Description != "" {
		opt = opt + " — " + answer.Description
	}
	return QuestionResult{
		Status:      "answered",
		Index:       answer.Index,
		Label:       answer.Label,
		Description: answer.Description,
		Timestamp:   answer.Timestamp,
	}
}

// NewCancelledResult создает результат отмены.
func NewCancelledResult(err string) QuestionResult {
	return QuestionResult{
		Status:    "cancelled",
		Error:     err,
		Timestamp: time.Now(),
	}
}

// NewTimeoutResult создает результат таймаута.
func NewTimeoutResult(timeout time.Duration) QuestionResult {
	return QuestionResult{
		Status:    "timeout",
		Error:     fmt.Sprintf("timeout_after_%s", timeout),
		Timestamp: time.Now(),
	}
}

// ToJSONString преобразует результат в JSON-строку для возврата в LLM.
func (qr *QuestionResult) ToJSONString() string {
	if qr.Status == "answered" {
		desc := qr.Description
		if desc == "" {
			desc = qr.Label
		}
		return fmt.Sprintf(
			`{"status":"answered","selected_index":%d,"selected_label":"%s","selected_description":"%s"}`,
			qr.Index, qr.Label, desc,
		)
	}
	return fmt.Sprintf(
		`{"status":"%s","error":"%s"}`,
		qr.Status, qr.Error,
	)
}

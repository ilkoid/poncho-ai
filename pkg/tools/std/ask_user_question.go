// Package std предоставляет стандартные инструменты для AI агента.
//
// AskUserQuestionTool — инструмент для задавания вопросов пользователю
// с вариантами ответов (1-5).
//
// АРХИТЕКТУРА (Polling Pattern):
// Tool не отправляет события! TUI опрашивает QuestionManager.HasPendingQuestions()
// после каждого события. Это соответствует Event Flow (Executor отправляет события).
package std

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/questions"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// AskUserQuestionTool — инструмент для задавания вопросов пользователю.
//
// Позволяет LLM задавать вопросы с вариантами ответов (1-5).
// TUI переключается в режим вопрос-ответ через polling QuestionManager.
//
// Архитектура (Rule 6 compliant):
// 1. LLM вызывает tool с вопросом и вариантами
// 2. Tool создает вопрос в QuestionManager (shared state)
// 3. Tool блокируется на WaitForAnswer()
// 4. TUI опрашивает QuestionManager.HasPendingQuestions() после каждого события
// 5. При наличии вопросов TUI переключается в question mode
// 6. Пользователь выбирает вариант (нажимает 1-5)
// 7. TUI отправляет ответ через QuestionManager.SubmitAnswer()
// 8. Tool.WaitForAnswer() разблокируется
// 9. Tool возвращает результат в LLM
// 10. Executor отправляет EventToolResult (как для обычных tools)
type AskUserQuestionTool struct {
	questionManager *questions.QuestionManager // Shared state (thread-safe)
	maxOptions      int
	timeout         time.Duration
	description     string
}

// NewAskUserQuestionTool создает инструмент для задавания вопросов.
//
// Параметры:
//   - questionManager: QuestionManager для координации вопросов (shared state)
//   - cfg: конфигурация tool из YAML
//
// Возвращает инструмент, готовый к регистрации в реестре.
//
// Примечание: Tool НЕ требует emitter! TUI опрашивает QuestionManager напрямую.
func NewAskUserQuestionTool(
	questionManager *questions.QuestionManager,
	cfg config.ToolConfig,
) *AskUserQuestionTool {
	// Ограничения согласно требованиям
	maxOptions := 5              // Максимум 5 вариантов (цифры 1-5)
	timeout := 5 * time.Minute   // Таймаут ожидания ответа

	return &AskUserQuestionTool{
		questionManager: questionManager,
		maxOptions:      maxOptions,
		timeout:         timeout,
		description:     cfg.Description,
	}
}

// SetQuestionManager устанавливает QuestionManager для tool.
//
// Вызывается при инициализации TUI для интеграции с вопросной системой.
// QuestionManager является shared state между Tool и TUI.
func (t *AskUserQuestionTool) SetQuestionManager(qm *questions.QuestionManager) {
	t.questionManager = qm
}

// GetQuestionManager возвращает QuestionManager для использования в TUI.
//
// TUI вызывает этот метод для получения менеджера вопросов,
// затем использует HasPendingQuestions(), GetQuestion(), SubmitAnswer().
func (t *AskUserQuestionTool) GetQuestionManager() *questions.QuestionManager {
	return t.questionManager
}

// Definition возвращает определение инструмента для function calling.
//
// Соответствует Tool interface (Rule 1).
func (t *AskUserQuestionTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "ask_user_question",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"question": map[string]interface{}{
					"type":        "string",
					"description": "Текст вопроса пользователю",
				},
				"options": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"label": map[string]interface{}{
								"type":        "string",
								"description": "Короткий текст варианта (например '12612157')",
							},
							"description": map[string]interface{}{
								"type":        "string",
								"description": "Пояснение варианта (опционально)",
							},
						},
						"required": []string{"label"},
					},
					"minItems": 1,
					"maxItems": t.maxOptions,
				},
			},
			"required": []string{"question", "options"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
//
// Соответствует Tool interface (Rule 1).
//
// Процесс (без emitter!):
// 1. Парсит аргументы (question, options)
// 2. Валидирует (1-5 вариантов, label не пустой)
// 3. Создает вопрос в QuestionManager (shared state)
// 4. Блокируется на WaitForAnswer()
// 5. Возвращает результат в JSON формате
// 6. Executor отправляет EventToolResult (стандартный flow)
func (t *AskUserQuestionTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// Парсим аргументы
	var args struct {
		Question string                     `json:"question"`
		Options  []questions.QuestionOption `json:"options"`
	}

	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("ошибка парсинга аргументов: %w", err)
	}

	// Валидация
	if args.Question == "" {
		return "", fmt.Errorf("question не может быть пустым")
	}
	if len(args.Options) == 0 {
		return "", fmt.Errorf("нужен хотя бы один вариант")
	}
	if len(args.Options) > t.maxOptions {
		return "", fmt.Errorf("слишком много вариантов: %d (максимум %d)", len(args.Options), t.maxOptions)
	}
	for i, opt := range args.Options {
		if opt.Label == "" {
			return "", fmt.Errorf("вариант %d: label не может быть пустым", i)
		}
	}

	// Проверяем что questionManager установлен
	if t.questionManager == nil {
		return "", fmt.Errorf("CRITICAL: questionManager is nil! Tool not configured properly")
	}

	// Создаем вопрос в QuestionManager (shared state)
	// TUI опросит HasPendingQuestions() и получит этот вопрос
	questionID, err := t.questionManager.CreateQuestion(args.Question, args.Options)
	if err != nil {
		return "", fmt.Errorf("ошибка создания вопроса: %w", err)
	}

	// Ждем ответа (блокировка до SubmitAnswer() или timeout)
	result, err := t.questionManager.WaitForAnswer(ctx, questionID)
	if err != nil {
		return "", fmt.Errorf("ошибка ожидания ответа: %w", err)
	}

	return result.ToJSONString(), nil
}

// QuestionAnswer — вспомогательная структура для создания ответа.
//
// Экспортируется для использования в TUI при отправке ответа.
type QuestionAnswer = questions.QuestionAnswer

// QuestionOption — вспомогательная структура для варианта ответа.
//
// Экспортируется для использования в Definition().
type QuestionOption = questions.QuestionOption

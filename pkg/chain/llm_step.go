// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// LLMInvocationStep — Step для вызова LLM.
//
// Используется в ReAct цикле для получения ответа от LLM.
// Вызывает LLM с текущим контекстом (история сообщений + системный промпт).
//
// Rule 4: Работает через llm.Provider интерфейс.
// Rule 5: Thread-safe через ChainContext.
// Rule 7: Возвращает ошибку вместо panic.
type LLMInvocationStep struct {
	// llm — провайдер языковой модели (Rule 4)
	llm llm.Provider

	// registry — реестр инструментов для получения определений (Rule 3)
	registry *tools.Registry

	// reasoningConfig — параметры модели для reasoning фазы
	reasoningConfig llm.GenerateOptions

	// chatConfig — параметры модели для финального ответа
	chatConfig llm.GenerateOptions

	// systemPrompt — базовый системный промпт
	systemPrompt string

	// debugRecorder — опциональный debug recorder
	debugRecorder *ChainDebugRecorder

	// startTime — время начала выполнения step (для duration tracking)
	startTime time.Time
}

// NewLLMInvocationStep создаёт новый LLMInvocationStep.
//
// Rule 10: Godoc на public API.
func NewLLMInvocationStep(
	llmProvider llm.Provider,
	registry *tools.Registry,
	reasoningConfig, chatConfig llm.GenerateOptions,
	systemPrompt string,
	debugRecorder *ChainDebugRecorder,
) *LLMInvocationStep {
	return &LLMInvocationStep{
		llm:             llmProvider,
		registry:        registry,
		reasoningConfig: reasoningConfig,
		chatConfig:      chatConfig,
		systemPrompt:    systemPrompt,
		debugRecorder:   debugRecorder,
	}
}

// Name возвращает имя Step (для логирования).
func (s *LLMInvocationStep) Name() string {
	return "llm_invocation"
}

// Execute выполняет LLM вызов.
//
// Возвращает:
//   - ActionContinue — если LLM вернул ответ (с tool calls или без)
//   - ActionError — если произошла ошибка
//
// Rule 7: Возвращает ошибку вместо panic.
func (s *LLMInvocationStep) Execute(ctx context.Context, chainCtx *ChainContext) (NextAction, error) {
	s.startTime = time.Now()

	// 1. Определяем параметры LLM (Rule 4 - runtime overrides)
	opts := s.determineLLMOptions(chainCtx)

	// 2. Формируем сообщения для LLM
	messages := chainCtx.BuildContextMessages(s.systemPrompt)
	messagesCount := len(messages)

	// 3. Получаем определения инструментов
	toolDefs := s.registry.GetDefinitions()

	// 4. Записываем LLM request в debug
	if s.debugRecorder != nil && s.debugRecorder.Enabled() {
		systemPromptUsed := "default"
		if chainCtx.GetActivePostPrompt() != "" {
			systemPromptUsed = "post_prompt"
		}
		s.debugRecorder.RecordLLMRequest(
			opts.Model,
			opts.Temperature,
			opts.MaxTokens,
			systemPromptUsed,
			messagesCount,
		)
	}

	// 5. Подготавливаем opts для Generate (конвертируем в слайс any)
	generateOpts := []any{toolDefs}
	if opts.Model != "" {
		generateOpts = append(generateOpts, llm.WithModel(opts.Model))
	}
	if opts.Temperature != 0 {
		generateOpts = append(generateOpts, llm.WithTemperature(opts.Temperature))
	}
	if opts.MaxTokens != 0 {
		generateOpts = append(generateOpts, llm.WithMaxTokens(opts.MaxTokens))
	}
	if opts.Format != "" {
		generateOpts = append(generateOpts, llm.WithFormat(opts.Format))
	}

	// 6. Вызываем LLM (Rule 4)
	llmStart := time.Now()
	response, err := s.llm.Generate(ctx, messages, generateOpts...)
	llmDuration := time.Since(llmStart).Milliseconds()

	if err != nil {
		// Rule 7: возвращаем ошибку вместо panic
		return ActionError, fmt.Errorf("LLM generation failed: %w", err)
	}

	// 6. Записываем LLM response в debug
	if s.debugRecorder != nil && s.debugRecorder.Enabled() {
		s.debugRecorder.RecordLLMResponse(
			response.Content,
			response.ToolCalls,
			llmDuration,
		)
	}

	// 7. Добавляем assistant message в историю (thread-safe)
	// REFACTORED 2026-01-04: AppendMessage теперь возвращает ошибку
	if err := chainCtx.AppendMessage(llm.Message{
		Role:      llm.RoleAssistant,
		Content:   response.Content,
		ToolCalls: response.ToolCalls,
	}); err != nil {
		return ActionError, fmt.Errorf("failed to append assistant message: %w", err)
	}

	// 8. Сохраняем фактические параметры модели в контексте
	chainCtx.SetActualModel(opts.Model)
	chainCtx.SetActualTemperature(opts.Temperature)
	chainCtx.SetActualMaxTokens(opts.MaxTokens)

	// 9. Продолжаем выполнение (ReAct цикл определит что делать дальше)
	return ActionContinue, nil
}

// determineLLMOptions определяет параметры LLM для текущей итерации.
//
// Приоритет параметров:
// 1. activePromptConfig (из post-prompt) — высший приоритет
// 2. reasoningConfig — для итераций с tool calls
// 3. chatConfig — для финального ответа
//
// Rule 4: Runtime parameter overrides через options pattern.
func (s *LLMInvocationStep) determineLLMOptions(chainCtx *ChainContext) llm.GenerateOptions {
	// Проверяем есть ли активный post-prompt с конфигурацией
	if promptConfig := chainCtx.GetActivePromptConfig(); promptConfig != nil {
		// Post-prompt имеет высший приоритет
		return llm.GenerateOptions{
			Model:       promptConfig.Model,
			Temperature: promptConfig.Temperature,
			MaxTokens:   promptConfig.MaxTokens,
			Format:      promptConfig.Format,
		}
	}

	// Иначе используем reasoningConfig (будет переопределён в ReAct при необходимости)
	return s.reasoningConfig
}

// GetDuration возвращает длительность выполнения step.
func (s *LLMInvocationStep) GetDuration() time.Duration {
	return time.Since(s.startTime)
}

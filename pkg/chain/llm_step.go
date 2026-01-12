// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/models"
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
	// modelRegistry — реестр LLM провайдеров (Rule 3)
	modelRegistry *models.Registry

	// defaultModel — имя модели по умолчанию для fallback
	defaultModel string

	// registry — реестр инструментов для получения определений (Rule 3)
	registry *tools.Registry

	// systemPrompt — базовый системный промпт
	systemPrompt string

	// debugRecorder — опциональный debug recorder
	debugRecorder *ChainDebugRecorder

	// emitter — для отправки streaming событий (EventThinkingChunk)
	emitter events.Emitter

	// startTime — время начала выполнения step (для duration tracking)
	startTime time.Time
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

	// 1. Определяем какую модель использовать
	modelName := s.determineModelName(chainCtx)

	// 2. Получаем провайдер и конфигурацию из реестра
	provider, modelDef, actualModel, err := s.modelRegistry.GetWithFallback(modelName, s.defaultModel)
	if err != nil {
		return ActionError, fmt.Errorf("failed to get model provider: %w", err)
	}

	// 3. Определяем параметры LLM (defaults + post-prompt overrides)
	opts := s.determineLLMOptions(chainCtx, modelDef)

	// 4. Формируем сообщения для LLM (с учетом типа модели)
	messages := chainCtx.BuildContextMessagesForModel(s.systemPrompt)
	messagesCount := len(messages)

	// 5. Получаем определения инструментов
	toolDefs := s.registry.GetDefinitions()

	// 6. Записываем LLM request в debug
	if s.debugRecorder != nil && s.debugRecorder.Enabled() {
		systemPromptUsed := "default"
		if chainCtx.GetActivePostPrompt() != "" {
			systemPromptUsed = "post_prompt"
		}
		s.debugRecorder.RecordLLMRequest(
			actualModel,
			opts.Temperature,
			opts.MaxTokens,
			systemPromptUsed,
			messagesCount,
		)
	}

	// 7. Подготавливаем opts для Generate (конвертируем в слайс any)
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

	// 8. Вызываем LLM (Rule 4)
	// Проверяем: поддерживает ли провайдер streaming?
	llmStart := time.Now()
	var response llm.Message

	if streamingProvider, ok := provider.(llm.StreamingProvider); ok && s.emitter != nil {
		// Используем streaming режим
		response, err = s.invokeStreamingLLM(ctx, streamingProvider, messages, generateOpts)
	} else {
		// Обычный синхронный режим
		response, err = provider.Generate(ctx, messages, generateOpts...)
	}
	llmDuration := time.Since(llmStart).Milliseconds()

	if err != nil {
		// Rule 7: возвращаем ошибку вместо panic
		return ActionError, fmt.Errorf("LLM generation failed: %w", err)
	}

	// 9. Записываем LLM response в debug
	if s.debugRecorder != nil && s.debugRecorder.Enabled() {
		s.debugRecorder.RecordLLMResponse(
			response.Content,
			response.ToolCalls,
			llmDuration,
		)
	}

	// 10. Добавляем assistant message в историю (thread-safe)
	if err := chainCtx.AppendMessage(llm.Message{
		Role:      llm.RoleAssistant,
		Content:   response.Content,
		ToolCalls: response.ToolCalls,
	}); err != nil {
		return ActionError, fmt.Errorf("failed to append assistant message: %w", err)
	}

	// 11. Сохраняем фактические параметры модели в контексте
	chainCtx.SetActualModel(actualModel)
	chainCtx.SetActualTemperature(opts.Temperature)
	chainCtx.SetActualMaxTokens(opts.MaxTokens)

	// 12. Продолжаем выполнение (ReAct цикл определит что делать дальше)
	return ActionContinue, nil
}

// determineModelName определяет какую модель использовать для текущей итерации.
//
// Приоритет:
// 1. Post-prompt config.Model (если указан)
// 2. defaultModel (fallback)
func (s *LLMInvocationStep) determineModelName(chainCtx *ChainContext) string {
	if promptConfig := chainCtx.GetActivePromptConfig(); promptConfig != nil {
		if promptConfig.Model != "" {
			return promptConfig.Model
		}
	}
	return s.defaultModel
}

// determineLLMOptions определяет параметры LLM для текущей итерации.
//
// Комбинирует дефолтные значения из modelDef с overrides из post-prompt.
//
// Приоритет параметров:
// 1. Post-prompt config (из activePromptConfig) — высший приоритет
// 2. Model defaults (из config.Models.Definitions[name])
//
// Rule 4: Runtime parameter overrides через options pattern.
func (s *LLMInvocationStep) determineLLMOptions(chainCtx *ChainContext, modelDef config.ModelDef) llm.GenerateOptions {
	// Начинаем с дефолтных значений из конфигурации модели
	opts := llm.GenerateOptions{
		Model:       modelDef.ModelName,
		Temperature: modelDef.Temperature,
		MaxTokens:   modelDef.MaxTokens,
	}

	// Копируем ParallelToolCalls из конфигурации модели
	if modelDef.ParallelToolCalls != nil {
		opts.ParallelToolCalls = modelDef.ParallelToolCalls
	}

	// Overrides из post-prompt если есть
	if promptConfig := chainCtx.GetActivePromptConfig(); promptConfig != nil {
		if promptConfig.Model != "" {
			opts.Model = promptConfig.Model
		}
		if promptConfig.Temperature != 0 {
			opts.Temperature = promptConfig.Temperature
		}
		if promptConfig.MaxTokens != 0 {
			opts.MaxTokens = promptConfig.MaxTokens
		}
		if promptConfig.Format != "" {
			opts.Format = promptConfig.Format
		}
	}

	return opts
}

// GetDuration возвращает длительность выполнения step.
func (s *LLMInvocationStep) GetDuration() time.Duration {
	return time.Since(s.startTime)
}

// invokeStreamingLLM вызывает LLM с поддержкой стриминга.
//
// Отправляет EventThinkingChunk для каждой порции reasoning_content.
func (s *LLMInvocationStep) invokeStreamingLLM(
	ctx context.Context,
	provider llm.StreamingProvider,
	messages []llm.Message,
	opts []any,
) (llm.Message, error) {
	// Callback для обработки чанков
	callback := func(chunk llm.StreamChunk) {
		// Rule 11: проверяем context
		select {
		case <-ctx.Done():
			return
		default:
		}

		switch chunk.Type {
		case llm.ChunkThinking:
			// Отправляем EventThinkingChunk с reasoning_content
			s.emitThinkingChunk(ctx, chunk)
		case llm.ChunkError:
			// Отправляем EventError
			if s.emitter != nil && chunk.Error != nil {
				s.emitter.Emit(ctx, events.Event{
					Type:      events.EventError,
					Data:      chunk.Error,
					Timestamp: time.Now(),
				})
			}
		case llm.ChunkDone:
			// Стриминг завершен
		}
	}

	// Вызываем GenerateStream
	return provider.GenerateStream(ctx, messages, callback, opts...)
}

// emitThinkingChunk отправляет событие с порцией reasoning_content.
func (s *LLMInvocationStep) emitThinkingChunk(ctx context.Context, chunk llm.StreamChunk) {
	if s.emitter == nil {
		return
	}
	s.emitter.Emit(ctx, events.Event{
		Type: events.EventThinkingChunk,
		Data: events.ThinkingChunkData{
			Chunk:       chunk.Delta,
			Accumulated: chunk.ReasoningContent,
		},
		Timestamp: time.Now(),
	})
}

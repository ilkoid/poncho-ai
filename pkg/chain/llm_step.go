// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/debug"
	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/models"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/utils"
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

	// bundleResolver — резолвер bundle для токен-оптимизации (Phase 3)
	bundleResolver *BundleResolver

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
//   - StepResult{Action: ActionContinue, Signal: SignalNone} — если LLM вернул ответ с tool calls
//   - StepResult{Action: ActionBreak, Signal: SignalFinalAnswer} — если финальный ответ (без tool calls)
//   - StepResult с ошибкой — если произошла ошибка
//
// PHASE 2 REFACTOR: Теперь возвращает StepResult с типизированным сигналом.
//
// Rule 7: Возвращает ошибку вместо panic.
func (s *LLMInvocationStep) Execute(ctx context.Context, chainCtx *ChainContext) StepResult {
	s.startTime = time.Now()

	// 1. Определяем какую модель использовать
	modelName := s.determineModelName(chainCtx)

	// 2. Получаем провайдер и конфигурацию из реестра
	provider, modelDef, actualModel, err := s.modelRegistry.GetWithFallback(modelName, s.defaultModel)
	if err != nil {
		return StepResult{}.WithError(fmt.Errorf("failed to get model provider: %w", err))
	}

	// 3. Определяем параметры LLM (defaults + post-prompt overrides)
	opts := s.determineLLMOptions(chainCtx, modelDef)

	// 4. Формируем сообщения для LLM (с учетом типа модели)
	messages := chainCtx.BuildContextMessagesForModel(s.systemPrompt)
	messagesCount := len(messages)

	// 5. Получаем определения инструментов (с учётом bundle mode)
	var toolDefs []tools.ToolDefinition
	if s.bundleResolver != nil {
		toolDefs = s.bundleResolver.GetToolDefinitions()
	} else {
		toolDefs = s.registry.GetDefinitions()
	}

	// 6. Записываем LLM request в debug
	if s.debugRecorder != nil && s.debugRecorder.Enabled() {
		systemPromptUsed := "default"
		if chainCtx.GetActivePostPrompt() != "" {
			systemPromptUsed = "post_prompt"
		}

		// Проверяем включено ли полное логирование
		if chainCtx.Input.FullLLMLogging {
			// Конвертируем tool definitions в debug format
			debugTools := make([]debug.ToolDef, len(toolDefs))
			for i, td := range toolDefs {
				debugTools[i] = debug.ToolDef{
					Name:        td.Name,
					Description: td.Description,
					Parameters:  td.Parameters,
				}
			}

			// Получаем thinking параметр и parallel_tool_calls из opts
			var thinking string
			var parallelToolCalls *bool
			// Note: эти параметры не хранятся в opts, но могут быть добавлены в будущем
			// Для текущей реализации передаём nil

			s.debugRecorder.RecordLLMRequestFull(
				actualModel,
				opts.Temperature,
				opts.MaxTokens,
				opts.Format,
				systemPromptUsed,
				messages,
				debugTools,
				nil, // rawRequest - можно добавить в будущем для полного HTTP запроса
				thinking,
				parallelToolCalls,
			)
		} else {
			// Стандартное логирование (без полных сообщений)
			s.debugRecorder.RecordLLMRequest(
				actualModel,
				opts.Temperature,
				opts.MaxTokens,
				systemPromptUsed,
				messagesCount,
			)
		}
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
		return StepResult{}.WithError(fmt.Errorf("LLM generation failed: %w", err))
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
		return StepResult{}.WithError(fmt.Errorf("failed to append assistant message: %w", err))
	}

	// 10a. PHASE 3: Bundle Resolution — проверяем и расширяем bundle calls
	if s.bundleResolver != nil && len(response.ToolCalls) > 0 {
		// Проверяем есть ли bundle calls среди tool calls
		for _, tc := range response.ToolCalls {
			if s.bundleResolver.IsBundleCall(tc.Name) {
				// Bundle call detected — расширяем bundle
				bundleMsg, err := s.bundleResolver.ExpandBundle(tc.Name)
				if err != nil {
					return StepResult{}.WithError(fmt.Errorf("failed to expand bundle '%s': %w", tc.Name, err))
				}

				// Добавляем bundle expansion message как system message
				if err := chainCtx.AppendMessage(bundleMsg); err != nil {
					return StepResult{}.WithError(fmt.Errorf("failed to append bundle expansion message: %w", err))
				}

				// Bundle expansion message добавлен в историю (toolCount только для информации)
				_ = s.countExpandedTools(bundleMsg.Content) // toolCount не используется, но подсчитывается для будущего использования

				// Re-run LLM call с расширенным контекстом
				return s.reRunWithExpandedContext(ctx, chainCtx, provider, modelDef, actualModel, opts)
			}
		}
	}

	// 11. Сохраняем фактические параметры модели в контексте
	chainCtx.SetActualModel(actualModel)
	chainCtx.SetActualTemperature(opts.Temperature)
	chainCtx.SetActualMaxTokens(opts.MaxTokens)

	// 12. Определяем сигнал на основе ответа
	// PHASE 2 REFACTOR: Используем типизированные сигналы вместо string-маркеров
	if len(response.ToolCalls) == 0 {
		// Финальный ответ - нет tool calls
		// Проверяем на маркер пользовательского ввода (для обратной совместимости)
		// TODO: В будущем это должно определяться через structured output
		if response.Content == UserChoiceRequest {
			return StepResult{
				Action: ActionBreak,
				Signal: SignalNeedUserInput,
			}
		}
		return StepResult{
			Action: ActionBreak,
			Signal: SignalFinalAnswer,
		}
	}

	// Есть tool calls - продолжаем цикл
	return StepResult{
		Action: ActionContinue,
		Signal: SignalNone,
	}
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
					Data:      events.ErrorData{Err: chunk.Error},
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

// countExpandedTools подсчитывает количество инструментов в bundle expansion message.
//
// Парсит содержимое system message и возвращает количество ## заголовков.
func (s *LLMInvocationStep) countExpandedTools(content string) int {
	count := 0
	for i := 0; i < len(content); i++ {
		if i+2 < len(content) && content[i] == '#' && content[i+1] == '#' {
			// Пропускаем пробелы после ##
			j := i + 2
			for j < len(content) && content[j] == ' ' {
				j++
			}
			// Если после ## есть текст (не конец строки), считаем как tool
			if j < len(content) && content[j] != '\n' {
				count++
			}
		}
	}
	return count
}

// reRunWithExpandedContext перезапускает LLM вызов с расширенным контекстом.
//
// Вызывается после bundle expansion. Получает обновлённый список сообщений
// из chainContext и делает ещё один LLM вызов.
func (s *LLMInvocationStep) reRunWithExpandedContext(
	ctx context.Context,
	chainCtx *ChainContext,
	provider llm.Provider,
	modelDef config.ModelDef,
	actualModel string,
	opts llm.GenerateOptions,
) StepResult {
	// 1. Получаем обновлённые сообщения (с bundle expansion)
	messages := chainCtx.BuildContextMessagesForModel(s.systemPrompt)

	// 2. Получаем tool definitions (теперь с расширенными tools из bundle)
	var toolDefs []tools.ToolDefinition
	if s.bundleResolver != nil {
		// После expansion bundle resolver должен возвращать все tools (включая расширенные)
		// Для simplicity пока берём все tools из registry
		toolDefs = s.registry.GetDefinitions()
	} else {
		toolDefs = s.registry.GetDefinitions()
	}

	// 3. Подготавливаем opts
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

	// 4. Вызываем LLM с расширенным контекстом
	llmStart := time.Now()
	var response llm.Message
	var err error

	// DEBUG: логируем bundle expansion re-run
	utils.Debug("Bundle expansion re-run",
		"messages_count", len(messages),
		"tools_count", len(toolDefs),
		"model", actualModel,
		"temperature", opts.Temperature,
		"max_tokens", opts.MaxTokens)

	// DEBUG: покажем последнее сообщение с bundle expansion
	if len(messages) > 0 {
		lastMsg := messages[len(messages)-1]
		utils.Debug("Bundle expansion last message",
			"role", lastMsg.Role,
			"content_preview", lastMsg.Content[:min(200, len(lastMsg.Content))])
	}

	if streamingProvider, ok := provider.(llm.StreamingProvider); ok && s.emitter != nil {
		response, err = s.invokeStreamingLLM(ctx, streamingProvider, messages, generateOpts)
	} else {
		response, err = provider.Generate(ctx, messages, generateOpts...)
	}
	llmDuration := time.Since(llmStart).Milliseconds()

	if err != nil {
		return StepResult{}.WithError(fmt.Errorf("LLM re-generation failed (after bundle expansion): %w", err))
	}

	// DEBUG: логируем результат bundle expansion re-run
	utils.Debug("Bundle expansion re-run result",
		"tool_calls_count", len(response.ToolCalls),
		"content_length", len(response.Content),
		"duration_ms", llmDuration,
		"has_tools", len(toolDefs) > 0)

	if len(response.ToolCalls) > 0 {
		for _, tc := range response.ToolCalls {
			utils.Debug("Bundle expansion tool call",
				"tool_name", tc.Name,
				"args_preview", tc.Args[:min(100, len(tc.Args))])
		}
	}

	// 5. Записываем LLM response в debug
	if s.debugRecorder != nil && s.debugRecorder.Enabled() {
		s.debugRecorder.RecordLLMResponse(
			response.Content,
			response.ToolCalls,
			llmDuration,
		)
	}

	// 6. Добавляем assistant message в историю
	if err := chainCtx.AppendMessage(llm.Message{
		Role:      llm.RoleAssistant,
		Content:   response.Content,
		ToolCalls: response.ToolCalls,
	}); err != nil {
		return StepResult{}.WithError(fmt.Errorf("failed to append re-generated assistant message: %w", err))
	}

	// 7. Сохраняем фактические параметры модели
	chainCtx.SetActualModel(actualModel)
	chainCtx.SetActualTemperature(opts.Temperature)
	chainCtx.SetActualMaxTokens(opts.MaxTokens)

	// 8. Определяем сигнал на основе ответа
	if len(response.ToolCalls) == 0 {
		if response.Content == UserChoiceRequest {
			return StepResult{
				Action: ActionBreak,
				Signal: SignalNeedUserInput,
			}
		}
		return StepResult{
			Action: ActionBreak,
			Signal: SignalFinalAnswer,
		}
	}

	// Есть tool calls - продолжаем цикл
	return StepResult{
		Action: ActionContinue,
		Signal: SignalNone,
	}
}

// min возвращает минимум из двух int

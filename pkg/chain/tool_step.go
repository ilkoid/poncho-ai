// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// ToolExecutionStep — Step для выполнения инструментов.
//
// Используется в ReAct цикле после LLM вызова с tool calls.
// Выполняет инструменты из последнего assistant message.
//
// Rule 1: Работает с Tool interface ("Raw In, String Out").
// Rule 3: Tools вызываются через Registry.
// Rule 5: Thread-safe через ChainContext.
// Rule 7: Возвращает ошибку вместо panic.
type ToolExecutionStep struct {
	// registry — реестр инструментов (Rule 3)
	registry *tools.Registry

	// promptLoader — loader для post-prompts
	promptLoader PromptLoader

	// debugRecorder — опциональный debug recorder
	debugRecorder *ChainDebugRecorder

	// startTime — время начала выполнения step
	startTime time.Time

	// toolResults — результаты выполненных инструментов (для post-processing)
	toolResults []ToolResult
}

// ToolResult — результат выполнения одного инструмента.
type ToolResult struct {
	Name     string
	Args     string
	Result   string
	Duration int64
	Success  bool
	Error    error
}

// PromptLoader — интерфейс для загрузки post-prompts.
//
// Разделён для возможности мокинга в тестах (Rule 9).
type PromptLoader interface {
	// LoadToolPostPrompt загружает post-prompt для инструмента.
	// Возвращает:
	//   - promptText: текст системного промпта
	//   - promptConfig: конфигурация с параметрами модели
	//   - error: ошибка загрузки
	LoadToolPostPrompt(toolName string) (string, *prompt.PromptConfig, error)
}

// NewToolExecutionStep создаёт новый ToolExecutionStep.
//
// Rule 10: Godoc на public API.
func NewToolExecutionStep(
	registry *tools.Registry,
	promptLoader PromptLoader,
	debugRecorder *ChainDebugRecorder,
) *ToolExecutionStep {
	return &ToolExecutionStep{
		registry:      registry,
		promptLoader:  promptLoader,
		debugRecorder: debugRecorder,
		toolResults:   make([]ToolResult, 0),
	}
}

// Name возвращает имя Step (для логирования).
func (s *ToolExecutionStep) Name() string {
	return "tool_execution"
}

// Execute выполняет все инструменты из последнего LLM ответа.
//
// Возвращает:
//   - ActionContinue — если инструменты выполнены успешно
//   - ActionError — если произошла критическая ошибка
//
// Rule 7: Возвращает ошибку вместо panic (но non-critical ошибки инструментов
//    возвращаются как строки в results).
func (s *ToolExecutionStep) Execute(ctx context.Context, chainCtx *ChainContext) (NextAction, error) {
	s.startTime = time.Now()
	s.toolResults = make([]ToolResult, 0)

	// 1. Получаем последнее сообщение (должен быть assistant с tool calls)
	lastMsg := chainCtx.GetLastMessage()
	if lastMsg == nil || lastMsg.Role != llm.RoleAssistant {
		return ActionError, fmt.Errorf("no assistant message found")
	}

	if len(lastMsg.ToolCalls) == 0 {
		// Нет tool calls - это нормально для финальной итерации
		return ActionContinue, nil
	}

	// 2. Выполняем каждый tool call
	for _, tc := range lastMsg.ToolCalls {
		result, err := s.executeToolCall(ctx, tc, chainCtx)
		s.toolResults = append(s.toolResults, result)

		if err != nil {
			// Критическая ошибка выполнения
			return ActionError, fmt.Errorf("tool execution failed: %w", err)
		}

		// 3. Добавляем tool result message в историю (thread-safe)
		// REFACTORED 2026-01-04: AppendMessage теперь возвращает ошибку
		if err := chainCtx.AppendMessage(llm.Message{
			Role:       llm.RoleTool,
			ToolCallID: tc.ID,
			Content:    result.Result,
		}); err != nil {
			return ActionError, fmt.Errorf("failed to append tool result message: %w", err)
		}
	}

	// 4. Если был выполнен только один инструмент, загружаем его post-prompt
	if len(lastMsg.ToolCalls) == 1 {
		toolName := lastMsg.ToolCalls[0].Name
		if err := s.loadAndActivatePostPrompt(toolName, chainCtx); err != nil {
			// Non-critical ошибка - логируем но продолжаем
			// В production можно добавить warning лог
			_ = err // TODO: добавить логирование
		}
	}

	return ActionContinue, nil
}

// executeToolCall выполняет один tool call.
//
// Rule 1: "Raw In, String Out" - получаем JSON строку, возвращаем строку.
// Rule 7: Возвращает ошибку вместо panic.
func (s *ToolExecutionStep) executeToolCall(ctx context.Context, tc llm.ToolCall, chainCtx *ChainContext) (ToolResult, error) {
	start := time.Now()
	result := ToolResult{
		Name: tc.Name,
		Args: tc.Args,
	}

	// 1. Санитизируем JSON аргументы
	cleanArgs := utils.CleanJsonBlock(tc.Args)

	// 2. Получаем tool из registry (Rule 3)
	tool, err := s.registry.Get(tc.Name)
	if err != nil {
		result.Success = false
		result.Error = err
		result.Result = fmt.Sprintf("Error: tool not found: %s", tc.Name)
		return result, err
	}

	// 3. Выполняем tool (Rule 1: "Raw In, String Out")
	execResult, execErr := tool.Execute(ctx, cleanArgs)
	duration := time.Since(start).Milliseconds()

	// 4. Формируем результат
	if execErr != nil {
		result.Success = false
		result.Error = execErr
		result.Result = fmt.Sprintf("Error: %v", execErr)
	} else {
		result.Success = true
		result.Result = execResult
	}
	result.Duration = duration

	// 5. Записываем в debug
	if s.debugRecorder != nil && s.debugRecorder.Enabled() {
		s.debugRecorder.RecordToolExecution(
			tc.Name,
			cleanArgs,
			result.Result,
			duration,
			result.Success,
		)
	}

	return result, nil
}

// loadAndActivatePostPrompt загружает и активирует post-prompt для инструмента.
//
// Post-prompt:
// 1. Загружается из YAML файла
// 2. Активируется в ChainContext (для следующей итерации)
// 3. Может переопределять параметры модели (model, temperature, max_tokens)
//
// Rule 2: Конфигурация через YAML.
func (s *ToolExecutionStep) loadAndActivatePostPrompt(toolName string, chainCtx *ChainContext) error {
	// 1. Загружаем post-prompt
	promptText, promptConfig, err := s.promptLoader.LoadToolPostPrompt(toolName)
	if err != nil {
		// Post-prompt опционален, не считаем это ошибкой
		return nil
	}

	// 2. Проверяем что post-prompt включён
	if promptConfig == nil {
		return nil
	}

	// 3. Активируем post-prompt в контексте (thread-safe)
	chainCtx.SetActivePostPrompt(promptText, promptConfig)

	return nil
}

// GetToolResults возвращает результаты выполнения инструментов.
func (s *ToolExecutionStep) GetToolResults() []ToolResult {
	return s.toolResults
}

// GetDuration возвращает длительность выполнения step.
func (s *ToolExecutionStep) GetDuration() time.Duration {
	return time.Since(s.startTime)
}

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

	// defaultToolTimeout — защитный timeout для выполнения инструментов
	// Если tool не завершится за это время, он будет отменён
	defaultToolTimeout time.Duration

	// toolTimeouts — переопределение timeout для конкретных инструментов
	toolTimeouts map[string]time.Duration
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

// Name возвращает имя Step (для логирования).
func (s *ToolExecutionStep) Name() string {
	return "tool_execution"
}

// Execute выполняет все инструменты из последнего LLM ответа.
//
// Возвращает:
//   - StepResult{Action: ActionContinue, Signal: SignalNone} — если инструменты выполнены успешно
//   - StepResult с ошибкой — если произошла критическая ошибка
//
// PHASE 2 REFACTOR: Теперь возвращает StepResult.
//
// Rule 7: Возвращает ошибку вместо panic (но non-critical ошибки инструментов
//    возвращаются как строки в results).
func (s *ToolExecutionStep) Execute(ctx context.Context, chainCtx *ChainContext) StepResult {
	s.startTime = time.Now()
	s.toolResults = make([]ToolResult, 0)

	// 1. Получаем последнее сообщение (должен быть assistant с tool calls)
	lastMsg := chainCtx.GetLastMessage()
	if lastMsg == nil || lastMsg.Role != llm.RoleAssistant {
		return StepResult{}.WithError(fmt.Errorf("no assistant message found"))
	}

	if len(lastMsg.ToolCalls) == 0 {
		// Нет tool calls - это нормально для финальной итерации
		return StepResult{
			Action: ActionContinue,
			Signal: SignalNone,
		}
	}

	// 2. Выполняем каждый tool call
	for _, tc := range lastMsg.ToolCalls {
		// 2a. ПРОВЕРКА: прерывание между tool calls для быстрой реакции
		// Это позволяет прервать выполнение до запуска следующего инструмента
		if chainCtx.Input.UserInputChan != nil {
			select {
			case userInput := <-chainCtx.Input.UserInputChan:
				// Пользователь прервал выполнение - возвращаем сигнал прерывания
				// Executor обработает это и добавит сообщение в историю
				return StepResult{
					Action:       ActionBreak,
					Signal:       SignalUserInterruption,
					Interruption: userInput,
				}
			default:
				// Нет прерывания - продолжаем выполнение инструмента
			}
		}

		result, err := s.executeToolCall(ctx, tc, chainCtx)
		s.toolResults = append(s.toolResults, result)

		if err != nil {
			// Критическая ошибка выполнения
			return StepResult{}.WithError(fmt.Errorf("tool execution failed: %w", err))
		}

		// 3. Добавляем tool result message в историю (thread-safe)
		// REFACTORED 2026-01-04: AppendMessage теперь возвращает ошибку
		if err := chainCtx.AppendMessage(llm.Message{
			Role:       llm.RoleTool,
			ToolCallID: tc.ID,
			Content:    result.Result,
		}); err != nil {
			return StepResult{}.WithError(fmt.Errorf("failed to append tool result message: %w", err))
		}
	}

	// 4. Загружаем post-prompt для выполненного инструмента
	//
	// NOTE: С включенным parallel_tool_calls=false LLM вызывает только один инструмент
	// за раз, поэтому post-prompt всегда активируется после выполнения.
	if len(lastMsg.ToolCalls) > 0 {
		toolName := lastMsg.ToolCalls[0].Name
		if err := s.loadAndActivatePostPrompt(toolName, chainCtx); err != nil {
			// Non-critical ошибка - логируем но продолжаем
			_ = err // TODO: добавить warning лог
		}
	}

	return StepResult{
		Action: ActionContinue,
		Signal: SignalNone,
	}
}

// executeToolCall выполняет один tool call.
//
// Rule 1: "Raw In, String Out" - получаем JSON строку, возвращаем строку.
// Rule 7: Возвращает ошибку вместо panic.
//
// Tool Timeout Protection:
//   - Использует defaultToolTimeout для предотвращения зависания
//   - Конкретный timeout можно переопределить через SetToolTimeout()
//   - При timeout возвращает ошибку без блокировки всего агента
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

	// 3. Определяем timeout для этого инструмента
	timeout := s.defaultToolTimeout
	if customTimeout, exists := s.toolTimeouts[tc.Name]; exists {
		timeout = customTimeout
	}

	// Special case: ask_user_question needs longer timeout (user interaction)
	// Override default timeout to 5 minutes for interactive tools
	if tc.Name == "ask_user_question" {
		timeout = 5 * time.Minute
	}

	// 4. Создаём контекст с timeout для защиты от зависания
	toolCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 5. Выполняем tool в отдельной goroutine для возможности отмены
	type execResult struct {
		output string
		err    error
	}
	resultChan := make(chan execResult, 1)

	go func() {
		execOutput, execErr := tool.Execute(toolCtx, cleanArgs)
		resultChan <- execResult{execOutput, execErr}
	}()

	// 6. Ждём результат или timeout
	select {
	case <-toolCtx.Done():
		// Timeout или отмена контекста
		duration := time.Since(start).Milliseconds()
		result.Success = false
		result.Duration = duration

		if toolCtx.Err() == context.DeadlineExceeded {
			result.Error = fmt.Errorf("tool execution timeout after %v", timeout)
			result.Result = fmt.Sprintf(
				"Tool %q exceeded timeout of %v. "+
					"Either the tool is stuck or the API response is slow.",
				tc.Name, timeout,
			)
		} else {
			result.Error = fmt.Errorf("tool execution cancelled: %w", toolCtx.Err())
			result.Result = "Tool execution was cancelled"
		}

		// Записываем в debug
		if s.debugRecorder != nil && s.debugRecorder.Enabled() {
			s.debugRecorder.RecordToolExecution(
				tc.Name,
				cleanArgs,
				result.Result,
				duration,
				result.Success,
			)
		}

		utils.Warn("Tool execution timeout",
			"tool", tc.Name,
			"timeout", timeout,
			"duration_ms", duration,
		)

		return result, result.Error

	case res := <-resultChan:
		// Tool завершился успешно (или с ошибкой, но в пределах timeout)
		duration := time.Since(start).Milliseconds()

		if res.err != nil {
			result.Success = false
			result.Error = res.err
			result.Result = fmt.Sprintf("Error: %v", res.err)
		} else {
			result.Success = true
			result.Result = res.output
		}
		result.Duration = duration

		// Записываем в debug
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

// SetDefaultToolTimeout устанавливает защитный timeout для всех инструментов.
//
// Если tool не завершится за это время, он будет отменён.
// Дефолтное значение: 30 секунд.
//
// Thread-safe: вызывать до начала Execute().
func (s *ToolExecutionStep) SetDefaultToolTimeout(timeout time.Duration) {
	s.defaultToolTimeout = timeout
}

// SetToolTimeout устанавливает индивидуальный timeout для конкретного инструмента.
//
// Переопределяет defaultToolTimeout для указанного инструмента.
// Полезно для медленных API (например, batch операции).
//
// Thread-safe: вызывать до начала Execute().
func (s *ToolExecutionStep) SetToolTimeout(toolName string, timeout time.Duration) {
	if s.toolTimeouts == nil {
		s.toolTimeouts = make(map[string]time.Duration)
	}
	s.toolTimeouts[toolName] = timeout
}

// GetDefaultToolTimeout возвращает текущий default timeout.
func (s *ToolExecutionStep) GetDefaultToolTimeout() time.Duration {
	return s.defaultToolTimeout
}

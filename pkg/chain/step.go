// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
)

// NextAction определяет поведение Chain после выполнения Step.
//
// Thread-safe concept: Step возвращает action, который обрабатывается Chain.
type NextAction int

const (
	// ActionContinue — продолжить выполнение следующего Step (или следующей итерации).
	ActionContinue NextAction = iota

	// ActionBreak — прервать выполнение Chain и вернуть результат.
	// Используется для завершения ReAct цикла.
	ActionBreak

	// ActionError — прервать выполнение с ошибкой.
	ActionError
)

// String возвращает строковое представление NextAction (для дебага).
func (a NextAction) String() string {
	switch a {
	case ActionContinue:
		return "Continue"
	case ActionBreak:
		return "Break"
	case ActionError:
		return "Error"
	default:
		return fmt.Sprintf("Unknown(%d)", a)
	}
}

// ExecutionSignal представляет типизированный управляющий сигнал от Step к Chain.
//
// PHASE 2 REFACTOR: Заменяет неявные string-маркеры (например, UserChoiceRequest)
// на типобезопасные сигналы. Это позволяет чётко определить намерение Step
// без анализа строкового содержимого ответа.
type ExecutionSignal int

const (
	// SignalNone — нет специального сигнала, обычное выполнение.
	SignalNone ExecutionSignal = iota

	// SignalFinalAnswer — финальный ответ получен, можно завершать выполнение.
	// Отправляется когда LLM вернул ответ без tool calls.
	SignalFinalAnswer

	// SignalNeedUserInput — требуется пользовательский ввод.
	// Отправляется когда LLM запросил дополнительную информацию от пользователя.
	// Заменяет string-маркер UserChoiceRequest.
	SignalNeedUserInput

	// SignalError — произошла ошибка, требующая особой обработки.
	SignalError
)

// String возвращает строковое представление ExecutionSignal (для дебага).
func (s ExecutionSignal) String() string {
	switch s {
	case SignalNone:
		return "None"
	case SignalFinalAnswer:
		return "FinalAnswer"
	case SignalNeedUserInput:
		return "NeedUserInput"
	case SignalError:
		return "Error"
	default:
		return fmt.Sprintf("Unknown(%d)", s)
	}
}

// StepResult представляет результат выполнения Step.
//
// PHASE 2 REFACTOR: Комбинирует NextAction (что делать дальше) и ExecutionSignal
// (типизированный управляющий сигнал). Это позволяет Step передавать более богатую
// информацию о своём состоянии без использования string-маркеров.
//
// Примеры использования:
//   - StepResult{Action: ActionContinue, Signal: SignalNone} — продолжить нормально
//   - StepResult{Action: ActionBreak, Signal: SignalFinalAnswer} — финальный ответ
//   - StepResult{Action: ActionBreak, Signal: SignalNeedUserInput} — нужен ввод пользователя
//   - StepResult{Action: ActionError, Signal: SignalError} — ошибка выполнения
type StepResult struct {
	// Action — что делать дальше (continue/break/error)
	Action NextAction

	// Signal — типизированный управляющий сигнал (опциональный)
	Signal ExecutionSignal

	// Error — ошибка выполнения (если Action == ActionError)
	Error error
}

// WithError создаёт StepResult с ошибкой.
func (r StepResult) WithError(err error) StepResult {
	return StepResult{
		Action: ActionError,
		Signal: SignalError,
		Error:  err,
	}
}

// String возвращает строковое представление StepResult (для дебага).
func (r StepResult) String() string {
	return fmt.Sprintf("StepResult{Action: %s, Signal: %s, Error: %v}",
		r.Action, r.Signal, r.Error)
}

// Step представляет атомарный шаг выполнения Chain.
//
// Step является изолированным, тестируемым и переиспользуемым компонентом.
// Каждый Step работает с ChainContext через thread-safe методы.
//
// Rule 7: Step возвращает ошибку (не panic), которая передаётся через StepResult.
//
// Примеры Step:
//   - LLMInvocationStep — вызывает LLM
//   - ToolExecutionStep — выполняет Tool
//   - PostPromptStep — загружает post-prompt
//   - ValidationStep — валидирует состояние
//
// PHASE 2 REFACTOR: Теперь возвращает StepResult вместо (NextAction, error).
type Step interface {
	// Name возвращает уникальное имя Step (для логирования).
	Name() string

	// Execute выполняет Step и возвращает StepResult.
	//
	// Step НЕ должен модифицировать ChainInput напрямую.
	// Все изменения состояния должны проходить через ChainContext методы.
	//
	// Возвращает:
	//   - StepResult — комбинация Action, Signal и Error
	Execute(ctx context.Context, chainCtx *ChainContext) StepResult
}

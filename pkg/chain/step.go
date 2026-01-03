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

	// ActionBranch — перейти к другому Step (для будущей Graph реализации).
	ActionBranch
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
	case ActionBranch:
		return "Branch"
	default:
		return fmt.Sprintf("Unknown(%d)", a)
	}
}

// Step представляет атомарный шаг выполнения Chain.
//
// Step является изолированным, тестируемым и переиспользуемым компонентом.
// Каждый Step работает с ChainContext через thread-safe методы.
//
// Rule 7: Step возвращает ошибку (не panic), которая передаётся в ActionError.
//
// Примеры Step:
//   - LLMInvocationStep — вызывает LLM
//   - ToolExecutionStep — выполняет Tool
//   - PostPromptStep — загружает post-prompt
//   - ValidationStep — валидирует состояние
type Step interface {
	// Name возвращает уникальное имя Step (для логирования).
	Name() string

	// Execute выполняет Step и возвращает NextAction.
	//
	// Step НЕ должен модифицировать ChainInput напрямую.
	// Все изменения состояния должны проходить через ChainContext методы.
	//
	// Возвращает:
	//   - NextAction — что делать дальше (continue/break/error/branch)
	//   - error — ошибка выполнения (для ActionError)
	Execute(ctx context.Context, chainCtx *ChainContext) (NextAction, error)
}

// StepFunc — функциональная обёртка для простых Step.
//
// Позволяет создавать Step на лету без структур.
//
// Пример:
//
//	step := chain.StepFunc{
//		name: "print_hello",
//		fn: func(ctx context.Context, c *chain.ChainContext) (chain.NextAction, error) {
//			fmt.Println("Hello from step!")
//			return chain.ActionContinue, nil
//		},
//	}
type StepFunc struct {
	name string
	fn   func(context.Context, *ChainContext) (NextAction, error)
}

// Name возвращает имя StepFunc.
func (s StepFunc) Name() string {
	return s.name
}

// Execute выполняет функцию StepFunc.
func (s StepFunc) Execute(ctx context.Context, chainCtx *ChainContext) (NextAction, error) {
	return s.fn(ctx, chainCtx)
}

// NewStepFunc создаёт новый StepFunc из функции.
//
// Rule 10: Godoc на public API.
func NewStepFunc(name string, fn func(context.Context, *ChainContext) (NextAction, error)) Step {
	return StepFunc{
		name: name,
		fn:   fn,
	}
}

// Package chain предоставляет Chain Pattern для AI агента.
//
// Chain позволяет компоновать сложное поведение из простых шагов (Step).
// Каждый Step является изолированным, тестируемым и переиспользуемым.
//
// Правила из dev_manifest.md:
//   - Rule 1: Работает с Tool interface ("Raw In, String Out")
//   - Rule 2: Конфигурируется через YAML
//   - Rule 3: Tools вызываются через Registry
//   - Rule 4: LLM вызывается через llm.Provider
//   - Rule 5: Thread-safe через ChainContext
//   - Rule 7: Все ошибки возвращаются, нет panic
//   - Rule 10: Godoc на всех public API
package chain

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// Chain представляет последовательность шагов для выполнения запроса.
//
// Chain является иммутабельным после создания и thread-safe для выполнения.
type Chain interface {
	// Execute выполняет цепочку и возвращает результат.
	Execute(ctx context.Context, input ChainInput) (ChainOutput, error)
}

// ChainInput — входные данные для выполнения цепочки.
type ChainInput struct {
	// UserQuery — запрос пользователя
	UserQuery string

	// State — framework core состояние (thread-safe)
	// Rule 6: Использует pkg/state.CoreState вместо internal/app
	State *state.CoreState

	// Registry — реестр инструментов (Rule 3)
	Registry *tools.Registry

	// Config — конфигурация цепочки (из YAML)
	Config ChainConfig
}

// ChainOutput — результат выполнения цепочки.
type ChainOutput struct {
	// Result — финальный ответ агента
	Result string

	// Iterations — количество выполненных итераций
	Iterations int

	// Duration — общее время выполнения
	Duration time.Duration

	// FinalState — финальное состояние истории сообщений
	FinalState []llm.Message

	// DebugPath — путь к сохраненному debug логу (если включен)
	DebugPath string

	// Signal — типизированный сигнал от последнего шага
	// PHASE 2 REFACTOR: Указывает на особые условия завершения
	//   - SignalNone: нормальное завершение
	//   - SignalFinalAnswer: финальный ответ получен
	//   - SignalNeedUserInput: требуется пользовательский ввод
	//   - SignalError: ошибка выполнения
	Signal ExecutionSignal
}

// ChainConfig — конфигурация цепочки из YAML.
type ChainConfig struct {
	// Type — тип цепочки ("react", "sequential", "conditional")
	Type string

	// Description — описание цепочки
	Description string

	// MaxIterations — максимальное количество итераций (для ReAct)
	MaxIterations int

	// Timeout — таймаут выполнения
	Timeout time.Duration

	// Steps — шаги цепочки
	Steps []StepConfig

	// Debug — конфигурация debug логирования
	Debug DebugConfig

	// PostPromptsDir — директория с post-prompts
	PostPromptsDir string
}

// StepConfig — конфигурация шага из YAML.
type StepConfig struct {
	// Name — имя шага
	Name string

	// Type — тип шага ("llm", "tools", "custom")
	Type string

	// Config — дополнительные параметры шага
	Config map[string]interface{}
}

// DebugConfig — конфигурация debug логирования для Chain.
type DebugConfig struct {
	// Enabled — включено ли debug логирование
	Enabled bool

	// SaveLogs — сохранять ли логи в файлы
	SaveLogs bool

	// LogsDir — директория для логов
	LogsDir string

	// IncludeToolArgs — включать аргументы инструментов
	IncludeToolArgs bool

	// IncludeToolResults — включать результаты инструментов
	IncludeToolResults bool

	// MaxResultSize — максимальный размер результата (символов)
	MaxResultSize int
}

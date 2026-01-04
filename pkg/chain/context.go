// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"fmt"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
	"github.com/ilkoid/poncho-ai/pkg/state"
)

// ChainContext содержит состояние выполнения цепочки.
//
// Thread-safe через sync.RWMutex (Rule 5).
// Все изменения состояния должны проходить через методы этого типа.
//
// REFACTORED 2026-01-04: Удалено дублирующее поле messages.
// Теперь используется state.CoreState как единый source of truth для истории.
type ChainContext struct {
	mu sync.RWMutex

	// Входные данные (неизменяемые после создания)
	Input *ChainInput

	// Ссылка на CoreState (единый source of truth для истории)
	// Rule 6: Используем pkg/state.CoreState вместо дублирования
	State *state.CoreState

	// Текущее состояние
	currentIteration int

	// Post-prompt состояние
	activePostPrompt   string
	activePromptConfig *prompt.PromptConfig

	// LLM параметры текущей итерации (определяются в runtime)
	actualModel      string
	actualTemperature float64
	actualMaxTokens  int
}

// NewChainContext создаёт новый контекст выполнения цепочки.
//
// REFACTORED 2026-01-04: Теперь требует state.CoreState как обязательный параметр.
// История сообщений хранится в CoreState, ChainContext не дублирует данные.
func NewChainContext(input ChainInput) *ChainContext {
	return &ChainContext{
		Input:            &input,
		State:            input.State, // CoreState из ChainInput
		currentIteration: 0,
	}
}

// GetInput возвращает входные данные (thread-safe).
func (c *ChainContext) GetInput() *ChainInput {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Input
}

// GetCurrentIteration возвращает номер текущей итерации (thread-safe).
func (c *ChainContext) GetCurrentIteration() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentIteration
}

// IncrementIteration увеличивает счётчик итераций (thread-safe).
func (c *ChainContext) IncrementIteration() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.currentIteration++
	return c.currentIteration
}

// ============================================================
// Message Methods (делегируют в CoreState)
// ============================================================

// GetMessages возвращает копию сообщений из CoreState (thread-safe).
//
// REFACTORED 2026-01-04: Теперь делегирует в CoreState.GetHistory().
// Раньше дублировало историю в c.messages.
func (c *ChainContext) GetMessages() []llm.Message {
	return c.State.GetHistory()
}

// GetLastMessage возвращает последнее сообщение из CoreState (thread-safe).
//
// REFACTORED 2026-01-04: Теперь использует CoreState.GetHistory().
func (c *ChainContext) GetLastMessage() *llm.Message {
	history := c.State.GetHistory()
	if len(history) == 0 {
		return nil
	}
	// Возвращаем копию
	msg := history[len(history)-1]
	return &msg
}

// AppendMessage добавляет сообщение в историю CoreState (thread-safe).
//
// REFACTORED 2026-01-04: Теперь делегирует в CoreState.Append().
// Раньше дублировало историю в c.messages.
func (c *ChainContext) AppendMessage(msg llm.Message) error {
	return c.State.Append(msg)
}

// SetMessages заменяет список сообщений в CoreState (thread-safe).
//
// REFACTORED 2026-01-04: Теперь очищает историю в CoreState и добавляет новые сообщения.
// Используется для восстановления состояния.
func (c *ChainContext) SetMessages(msgs []llm.Message) error {
	// Очищаем текущую историю через Update с возвратом пустого слайса
	// REFACTORED 2026-01-04: ClearHistory() удален, используем Set напрямую
	if err := c.State.Set(state.KeyHistory, []llm.Message{}); err != nil {
		return fmt.Errorf("failed to clear history: %w", err)
	}

	// Добавляем все сообщения
	for _, msg := range msgs {
		if err := c.State.Append(msg); err != nil {
			return fmt.Errorf("failed to append message: %w", err)
		}
	}

	return nil
}

// ============================================================
// Post-prompt Methods
// ============================================================

// GetActivePostPrompt возвращает активный post-prompt (thread-safe).
func (c *ChainContext) GetActivePostPrompt() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.activePostPrompt
}

// GetActivePromptConfig возвращает конфигурацию активного post-prompt (thread-safe).
func (c *ChainContext) GetActivePromptConfig() *prompt.PromptConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.activePromptConfig
}

// SetActivePostPrompt устанавливает активный post-prompt (thread-safe).
//
// Сбрасывает предыдущий активный post-prompt.
func (c *ChainContext) SetActivePostPrompt(prompt string, config *prompt.PromptConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.activePostPrompt = prompt
	c.activePromptConfig = config
}

// ClearActivePostPrompt сбрасывает активный post-prompt (thread-safe).
func (c *ChainContext) ClearActivePostPrompt() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.activePostPrompt = ""
	c.activePromptConfig = nil
}

// ============================================================
// LLM Parameters Methods
// ============================================================

// GetActualModel возвращает модель для текущей итерации (thread-safe).
func (c *ChainContext) GetActualModel() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.actualModel
}

// SetActualModel устанавливает модель для текущей итерации (thread-safe).
func (c *ChainContext) SetActualModel(model string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.actualModel = model
}

// GetActualTemperature возвращает температуру для текущей итерации (thread-safe).
func (c *ChainContext) GetActualTemperature() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.actualTemperature
}

// SetActualTemperature устанавливает температуру для текущей итерации (thread-safe).
func (c *ChainContext) SetActualTemperature(temp float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.actualTemperature = temp
}

// GetActualMaxTokens возвращает max_tokens для текущей итерации (thread-safe).
func (c *ChainContext) GetActualMaxTokens() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.actualMaxTokens
}

// SetActualMaxTokens устанавливает max_tokens для текущей итерации (thread-safe).
func (c *ChainContext) SetActualMaxTokens(tokens int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.actualMaxTokens = tokens
}

// ============================================================
// Context Building
// ============================================================

// BuildContextMessages формирует сообщения для LLM на основе текущего состояния (thread-safe).
//
// REFACTORED 2026-01-04: Теперь использует CoreState.BuildAgentContext()
// который собирает полный контекст (системный промпт, рабочая память,
// контекст плана, история диалога).
//
// Использует активный post-prompt если установлен.
func (c *ChainContext) BuildContextMessages(systemPrompt string) []llm.Message {
	c.mu.RLock()
	activePostPrompt := c.activePostPrompt
	c.mu.RUnlock()

	// Определяем системный промпт
	actualSystemPrompt := systemPrompt
	if activePostPrompt != "" {
		actualSystemPrompt = activePostPrompt
	}

	// Используем CoreState.BuildAgentContext для сборки полного контекста
	// REFACTORED: Раньше использовали c.messages (дублирование)
	messages := c.State.BuildAgentContext(actualSystemPrompt)

	return messages
}

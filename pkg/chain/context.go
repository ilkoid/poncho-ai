// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"fmt"
	"strings"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
)

// ChainContext содержит состояние выполнения цепочки.
//
// Thread-safe через sync.RWMutex (Rule 5).
// Все изменения состояния должны проходить через методы этого типа.
type ChainContext struct {
	mu sync.RWMutex

	// Входные данные (неизменяемые после создания)
	Input *ChainInput

	// Текущее состояние
	currentIteration int
	messages          []llm.Message

	// Post-prompt состояние
	activePostPrompt     string
	activePromptConfig   *prompt.PromptConfig

	// LLM параметры текущей итерации (определяются в runtime)
	actualModel       string
	actualTemperature  float64
	actualMaxTokens   int
}

// NewChainContext создаёт новый контекст выполнения цепочки.
func NewChainContext(input ChainInput) *ChainContext {
	return &ChainContext{
		Input:            &input,
		currentIteration: 0,
		messages:         make([]llm.Message, 0, 10),
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

// GetMessages возвращает копию сообщений (thread-safe).
func (c *ChainContext) GetMessages() []llm.Message {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Возвращаем копию для избежания race conditions
	result := make([]llm.Message, len(c.messages))
	copy(result, c.messages)
	return result
}

// GetLastMessage возвращает последнее сообщение (thread-safe).
func (c *ChainContext) GetLastMessage() *llm.Message {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.messages) == 0 {
		return nil
	}
	// Возвращаем копию
	msg := c.messages[len(c.messages)-1]
	return &msg
}

// AppendMessage добавляет сообщение в историю (thread-safe).
func (c *ChainContext) AppendMessage(msg llm.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.messages = append(c.messages, msg)
}

// SetMessages заменяет список сообщений (thread-safe).
// Используется для восстановления состояния.
func (c *ChainContext) SetMessages(msgs []llm.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.messages = make([]llm.Message, len(msgs))
	copy(c.messages, msgs)
}

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

// BuildContextMessages формирует сообщения для LLM на основе текущего состояния (thread-safe).
//
// Использует активный post-prompt если установлен.
func (c *ChainContext) BuildContextMessages(systemPrompt string) []llm.Message {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Определяем системный промпт
	actualSystemPrompt := systemPrompt
	if c.activePostPrompt != "" {
		actualSystemPrompt = c.activePostPrompt
	}

	// Формируем сообщения
	messages := make([]llm.Message, 0, len(c.messages)+2)

	// Системный промпт
	if actualSystemPrompt != "" {
		messages = append(messages, llm.Message{
			Role:    llm.RoleSystem,
			Content: actualSystemPrompt,
		})
	}

	// История диалога
	messages = append(messages, c.messages...)

	return messages
}

// String возвращает строковое представление контекста (для дебага).
func (c *ChainContext) String() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var sb strings.Builder
	sb.WriteString("ChainContext{")
	sb.WriteString(fmt.Sprintf("Iteration: %d, ", c.currentIteration))
	sb.WriteString(fmt.Sprintf("Messages: %d, ", len(c.messages)))
	sb.WriteString(fmt.Sprintf("Model: %s, ", c.actualModel))
	sb.WriteString(fmt.Sprintf("Temp: %.2f", c.actualTemperature))
	if c.activePostPrompt != "" {
		sb.WriteString(fmt.Sprintf(", PostPrompt: %s", c.activePostPrompt))
	}
	sb.WriteString("}")

	return sb.String()
}

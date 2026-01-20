// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"fmt"
	"strings"
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
	// Очищаем текущую историю через type-safe SetType
	// REFACTORED 2026-01-20: Используем SetType для типобезопасности
	if err := state.SetType[[]llm.Message](c.State, state.KeyHistory, []llm.Message{}); err != nil {
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

// BuildContextMessagesForModel формирует сообщения для LLM с учетом типа модели.
//
// Фильтрует контекст в зависимости от того, является ли модель vision-моделью:
//   - Vision модели: полный контекст (включая VisionDescription и Images)
//   - Chat модели: контекст WITHOUT VisionDescription и без Images из истории
//
// Thread-safe. Определяет тип модели по actualModel из ChainContext.
func (c *ChainContext) BuildContextMessagesForModel(systemPrompt string) []llm.Message {
	c.mu.RLock()
	actualModel := c.actualModel
	activePostPrompt := c.activePostPrompt
	c.mu.RUnlock()

	// Определяем системный промпт
	actualSystemPrompt := systemPrompt
	if activePostPrompt != "" {
		actualSystemPrompt = activePostPrompt
	}

	// Получаем базовый контекст
	messages := c.State.BuildAgentContext(actualSystemPrompt)

	// Если модель не vision - фильтруем контент
	if !c.isModelVision(actualModel) {
		messages = c.filterMessagesForChatModel(messages)
	}

	return messages
}

// isModelVision проверяет, является ли модель vision-моделью.
// Использует эвристику по названию (TODO: использовать ModelRegistry).
func (c *ChainContext) isModelVision(modelName string) bool {
	if modelName == "" {
		return false
	}

	// Эвристика по названию
	return strings.Contains(strings.ToLower(modelName), "vision") ||
		strings.Contains(strings.ToLower(modelName), "v-")
}

// filterMessagesForChatModel фильтрует сообщения для chat-модели.
//
// Удаляет:
//   1. VisionDescription из системных промптов (блок "КОНТЕКСТ АРТИКУЛА")
//   2. Images из всех сообщений истории
//
// Возвращает новый слайс, не модифицирует оригинал.
func (c *ChainContext) filterMessagesForChatModel(messages []llm.Message) []llm.Message {
	filtered := make([]llm.Message, 0, len(messages))

	for _, msg := range messages {
		filteredMsg := msg

		// Если это системное сообщение - убираем блок КОНТЕКСТ АРТИКУЛА
		if msg.Role == llm.RoleSystem {
			filteredMsg.Content = c.removeVisionContextFromSystem(msg.Content)
		}

		// Убираем Images из всех сообщений
		filteredMsg.Images = nil

		filtered = append(filtered, filteredMsg)
	}

	return filtered
}

// removeVisionContextFromSystem удаляет блок с описаниями изображений из системного промпта.
//
// Ищет и удаляет текст вида:
//   КОНТЕКСТ АРТИКУЛА (Результаты анализа файлов):
//   - Файл [tag] filename: description
//
// Также удаляет пустую строку перед блоком, если она есть.
func (c *ChainContext) removeVisionContextFromSystem(content string) string {
	// Ищем начало блока
	marker := "КОНТЕКСТ АРТИКУЛА"
	idx := strings.Index(content, marker)

	if idx == -1 {
		return content // Блок не найден
	}

	// Ищем начало строки (новая строка перед маркером)
	newlineBefore := strings.LastIndex(content[:idx], "\n")
	if newlineBefore == -1 {
		return "" // Блок в начале строки
	}

	// Проверяем, есть ли перед нами еще одна пустая строка
	result := content[:newlineBefore]
	// Удаляем завершающий \n если он есть
	result = strings.TrimSuffix(result, "\n")
	// Удаляем еще один \n если был двойной перенос
	result = strings.TrimSuffix(result, "\n")

	return result
}

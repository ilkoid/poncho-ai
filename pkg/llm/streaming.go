// Package llm предоставляет типы и интерфейсы для работы с LLM провайдерами.
//
// Этот файл определяет абстракции для потоковой передачи (streaming) ответов от LLM.
package llm

import "context"

// StreamingProvider — интерфейс для LLM провайдеров с поддержкой стриминга.
//
// Отдельный интерфейс от Provider для обратной совместимости.
// Провайдеры могут реализовать оба интерфейса или только Provider.
//
// # Rule 4: LLM Abstraction
//
// Работаем через интерфейс, конкретные реализации (OpenAI, Anthropic и т.д.)
// скрыты за этим абстракцией.
//
// # Rule 11: Context Propagation
//
// Все методы уважают context.Context и прерывают операцию при отмене.
type StreamingProvider interface {
	// Provider — базовый интерфейс для синхронной генерации.
	// StreamingProvider расширяет Provider, сохраняя обратную совместимость.
	Provider

	// GenerateStream выполняет запрос к API с потоковой передачей ответа.
	//
	// Параметры:
	//   - ctx: контекст для отмены операции (Rule 11)
	//   - messages: история сообщений
	//   - callback: функция для обработки каждого чанка
	//   - opts: опциональные параметры (tools, GenerateOption, StreamOption)
	//
	// Возвращает финальное сообщение после завершения стриминга.
	//
	// Callback вызывается для каждой порции данных:
	//   - ChunkThinking: reasoning_content из thinking mode
	//   - ChunkContent: обычный контент ответа
	//   - ChunkError: ошибка стриминга
	//   - ChunkDone: завершение стриминга
	//
	// # Thread Safety
	//
	// Callback может вызываться из разных goroutine, должен быть thread-safe.
	GenerateStream(
		ctx context.Context,
		messages []Message,
		callback func(StreamChunk),
		opts ...any,
	) (Message, error)
}

// StreamChunk представляет одну порцию данных из потокового ответа.
//
// Структура оптимизирована для отправки через events.Event и содержит
// как инкрементальные изменения (Delta), так и накопленное состояние (Content).
type StreamChunk struct {
	// Type определяет тип чанка
	Type ChunkType

	// Content содержит накопленный текстовый контент на данный момент
	Content string

	// ReasoningContent содержит накопленный reasoning_content из thinking mode
	ReasoningContent string

	// Delta — инкрементальные изменения (для UI обновлений в реальном времени)
	Delta string

	// Done — флаг завершения стриминга
	Done bool

	// Error — ошибка если произошла (только когда Type == ChunkError)
	Error error
}

// ChunkType определяет тип стримингового чанка.
type ChunkType string

const (
	// ChunkThinking — reasoning_content из thinking mode (Zai GLM).
	// Отправляется только когда thinking параметр включен.
	ChunkThinking ChunkType = "thinking"

	// ChunkContent — обычный контент ответа.
	// Накапливается по мере поступления от LLM.
	ChunkContent ChunkType = "content"

	// ChunkError — ошибка стриминга.
	// Содержит ошибку в поле Error.
	ChunkError ChunkType = "error"

	// ChunkDone — завершение стриминга.
	// Отправляется когда все данные получены.
	ChunkDone ChunkType = "done"
)

// IsStreamingMode проверяет, включен ли стриминг в опциях.
//
// По умолчанию возвращает true (opt-out дизайн): стриминг включён,
// если явно не выключен через WithStream(false).
//
// # Usage
//
//	opts := []any{WithStream(false)}  // выключить стриминг
//	isStreaming := IsStreamingMode(opts...) // false
func IsStreamingMode(opts ...any) bool {
	for _, opt := range opts {
		if streamOpt, ok := opt.(StreamOption); ok {
			var so StreamOptions
			streamOpt(&so)
			return so.Enabled
		}
	}
	return true // Default: streaming enabled (opt-out)
}

// IsThinkingOnly проверяет, нужно ли отправлять только reasoning_content.
//
// По умолчанию возвращает true: отправляются события только для
// reasoning_content из thinking mode (требование пользователя).
func IsThinkingOnly(opts ...any) bool {
	for _, opt := range opts {
		if streamOpt, ok := opt.(StreamOption); ok {
			var so StreamOptions
			streamOpt(&so)
			return so.ThinkingOnly
		}
	}
	return true // Default: thinking only
}

//go:build short

// Интерфейс Провайдера через который работает всё приложение.

package llm

import "context"

// Пакет llm/provider.go определяет интерфейс, который должны реализовать
// все адаптеры (OpenAI, Anthropic, Ollama и т.д.).

// Provider — абстракция над LLM API.
type Provider interface {
	// Generate принимает контекст и историю сообщений.
	// Возвращает ответ модели в унифицированном формате Message.
	// tools — опциональный список определений функций (если провайдер поддерживает Function Calling).
	Generate(ctx context.Context, messages []Message, tools ...any) (Message, error)
}
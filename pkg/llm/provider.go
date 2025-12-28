// Интерфейс Провайдера через который работает всё приложение.

package llm

import "context"

// Пакет llm/provider.go определяет интерфейс, который должны реализовать
// все адаптеры (OpenAI, Anthropic, Ollama и т.д.).

// Provider — абстракция над LLM API.
type Provider interface {
	// Generate принимает контекст, историю сообщений и опциональные параметры.
	// Возвращает ответ модели в унифицированном формате Message.
	//
	// Параметры (variadic):
	//   - tools: []ToolDefinition или []map[string]interface{} - для Function Calling
	//   - opts: GenerateOption - для runtime переопределения параметров модели
	//
	// Примеры:
	//   Generate(ctx, messages, toolDefs)                                    // только tools
	//   Generate(ctx, messages, WithModel("glm-4.6"), WithTemperature(0.7)) // только opts
	//   Generate(ctx, messages, toolDefs, WithTemperature(0.5))             // tools + opts
	Generate(ctx context.Context, messages []Message, opts ...any) (Message, error)
}

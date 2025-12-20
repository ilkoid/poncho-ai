package llm

// Пакет llm содержит абстракции и типы данных для взаимодействия с языковыми моделями.
// Этот файл определяет универсальную структуру сообщений, чтобы отвязать бизнес-логику
// от конкретных SDK (OpenAI, Anthropic и т.д.).

// Role определяет, кто автор сообщения.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool" // Результат выполнения функции
)

// ToolCall описывает запрос модели на вызов инструмента.
type ToolCall struct {
	ID   string // Уникальный ID вызова (нужен для mapping'а ответа)
	Name string // Имя функции (например, "wb_update_card")
	Args string // JSON-строка с аргументами
}

// Message — универсальная единица общения в ReAct цикле.
type Message struct {
	Role    Role
	Content string

	// ToolCalls заполняется, если Role == RoleAssistant и модель хочет вызвать тулы.
	ToolCalls []ToolCall

	// ToolCallID заполняется, если Role == RoleTool (ссылка на запрос).
	ToolCallID string

	// Images — пути к файлам или base64.
	// Используется ТОЛЬКО для одноразовых Vision-запросов (analyze image).
	// В основную историю ReAct эти данные попадать НЕ должны (экономия токенов).
	Images []string
}

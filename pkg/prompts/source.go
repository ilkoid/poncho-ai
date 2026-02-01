package prompts

// PromptSource — интерфейс для загрузки промптов из различных источников.
//
// OCP Principle: Открыт для расширения (новые источники), закрыт для изменения.
// Интерфейс обоснован: ≥3 реализации (File, Database, API).
//
// Rule 6: pkg/prompts не импортирует internal/ или бизнес-логику.
type PromptSource interface {
	// Load загружает промпт по идентификатору.
	// Возвращает ошибку, если источник не содержит промпт.
	Load(promptID string) (*PromptFile, error)
}

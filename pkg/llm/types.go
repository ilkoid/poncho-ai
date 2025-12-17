// Базовые типы - определяем универсальный язык общения с моделями
package llm

// ChatRequest — унифицированный запрос к любой модели
type ChatRequest struct {
	Model       string
	Temperature float64
	MaxTokens   int
	Format      string    // "json_object" или пустая строка
	Messages    []Message // История чата
}

// Message — одно сообщение
type Message struct {
	Role    string        // "system", "user", "assistant"
	Content []ContentPart // Мультимодальное содержимое
}

// ContentPart — часть сообщения (текст или картинка)
type ContentPart struct {
	Type     string // "text" или "image_url"
	Text     string // Заполнено, если Type == "text"
	ImageURL string // Заполнено, если Type == "image_url"
}

// Константы для удобства
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	
	TypeText  = "text"
	TypeImage = "image_url"
)


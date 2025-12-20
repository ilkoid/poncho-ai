# internal/app/state.go

```go
package app

import (
	"github.com/ilkoid/poncho-ai/pkg/classifier"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// GlobalState хранит данные сессии
type GlobalState struct {
    Config       *config.AppConfig
    S3           *s3storage.Client
    Dictionaries *wb.Dictionaries // <--- Чтобы доступ был отовсюду
    
    // Данные текущей сессии
    CurrentArticleID string
    CurrentModel     string
    IsProcessing     bool

    // Files хранит классифицированные файлы артикула
    // Ключ: тег (например, "sketch", "plm_data")
    // Значение: список файлов
    Files map[string][]classifier.ClassifiedFile // <--- Добавляем это поле
}

// NewState создает начальное состояние
func NewState(cfg *config.AppConfig, s3Client *s3storage.Client) *GlobalState {
    return &GlobalState{
        Config:           cfg,
        S3:               s3Client,
        CurrentArticleID: "NONE",
        CurrentModel:     cfg.Models.DefaultVision,
        IsProcessing:     false,
        
        // Инициализируем пустую карту, чтобы не было panic при чтении
        Files:            make(map[string][]classifier.ClassifiedFile), 
    }
}
```

=================

# internal/ui/model.go

```go
//  Структура и Init
package ui

import (
    "fmt"

    "github.com/ilkoid/poncho-ai/internal/app" // Импортируй свой app пакет

    "github.com/charmbracelet/bubbles/textarea"
    "github.com/charmbracelet/bubbles/viewport"
    tea "github.com/charmbracelet/bubbletea"
)

// MainModel - главная структура UI
type MainModel struct {
    viewport viewport.Model
    textarea textarea.Model
    
    appState *app.GlobalState
    
    // err хранит ошибку запуска, если была
    err error
    
    // ready флаг для первой инициализации размеров
    ready bool
}

// InitialModel создает начальное состояние UI
func InitialModel(state *app.GlobalState) MainModel {
    // 1. Настройка поля ввода
    ta := textarea.New()
    ta.Placeholder = "Введите команду (например: load 123)..."
    ta.Focus()
    ta.Prompt = "┃ "
    ta.CharLimit = 500
    ta.SetHeight(3)
    ta.ShowLineNumbers = false

    // 2. Настройка вьюпорта (лог чата)
    // Размеры (0,0) обновятся при первом событии WindowSizeMsg
    vp := viewport.New(0, 0)
    vp.SetContent(fmt.Sprintf("%s\n%s\n", 
        systemMsgStyle("Poncho AI v0.1 Initialized."),
        systemMsgStyle("System ready. Waiting for input..."),
    ))

    return MainModel{
        textarea: ta,
        viewport: vp,
        appState: state,
        ready:    false,
    }
}

// Init запускается один раз при старте
func (m MainModel) Init() tea.Cmd {
    return textarea.Blink // Заставляет курсор мигать
}

```

=================

# internal/ui/styles.go

```go
// Красота

package ui

import "github.com/charmbracelet/lipgloss"

var (
	// Цвета (можно настроить под бренд)
	primaryColor   = lipgloss.Color("62")  // Фиолетовый
	secondaryColor = lipgloss.Color("205") // Розовый
	grayColor      = lipgloss.Color("240")

	// Стили хедера
	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(primaryColor).
			Padding(0, 1).
			Bold(true)

	// Стили для сообщений в логе
	userMsgStyle = lipgloss.NewStyle().
			Foreground(secondaryColor).
			Bold(true).
			Render

	systemMsgStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#04B575")). // Зеленый
			Render

	errorMsgStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF0000")).
			Bold(true).
			Render

	// Экспортированные версии стилей для использования в других пакетах
	UserMsgStyle   = userMsgStyle
	SystemMsgStyle = systemMsgStyle
	ErrorMsgStyle  = errorMsgStyle
)

```

=================

# internal/ui/update.go

```go
// Логика - Обрабатывает нажатия клавиш и результаты команд.

package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ilkoid/poncho-ai/internal/app"
	"github.com/ilkoid/poncho-ai/pkg/classifier"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
)

// CommandResultMsg - сообщение, которое возвращает worker после работы
type CommandResultMsg struct {
    Output string
    Err    error
}

func (m MainModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    var (
        tiCmd tea.Cmd
        vpCmd tea.Cmd
    )

    m.textarea, tiCmd = m.textarea.Update(msg)
    m.viewport, vpCmd = m.viewport.Update(msg)

    switch msg := msg.(type) {

    // 1. Изменение размера окна терминала
    case tea.WindowSizeMsg:
        headerHeight := 1
        footerHeight := m.textarea.Height() + 2 // + граница
        
        // Вычисляем высоту для области контента
        vpHeight := msg.Height - headerHeight - footerHeight
        if vpHeight < 0 { 
            vpHeight = 0 
        }

        // Обновляем размеры существующего вьюпорта
        m.viewport.Width = msg.Width
        m.viewport.Height = vpHeight
        
        // Только при первом запуске (если нужно инициализировать контент)
        if !m.ready {
            m.ready = true
            // Опционально: можно принудительно обновить контент, если он зависит от ширины
        }
        
        m.textarea.SetWidth(msg.Width)


    // 2. Клавиши
    case tea.KeyMsg:
        switch msg.Type {
        case tea.KeyCtrlC, tea.KeyEsc:
            return m, tea.Quit
        
        case tea.KeyEnter:
            input := m.textarea.Value()
            if strings.TrimSpace(input) == "" {
                return m, nil
            }
            
            // Очищаем ввод
            m.textarea.Reset()

            // Добавляем сообщение пользователя в лог
            m.appendLog(userMsgStyle("USER > ") + input)

            // Запускаем асинхронную команду
            return m, performCommand(input, m.appState)
        }

    // 3. Результат выполнения команды (прилетел асинхронно)
    case CommandResultMsg:
        if msg.Err != nil {
            m.appendLog(errorMsgStyle("ERROR: ") + msg.Err.Error())
        } else {
            m.appendLog(systemMsgStyle("SYSTEM: ") + msg.Output)
        }
        // Возвращаем фокус на ввод
        m.textarea.Focus() 
    }

    return m, tea.Batch(tiCmd, vpCmd)
}

// Хелпер для добавления строки в лог и прокрутки вниз
func (m *MainModel) appendLog(str string) {
    newContent := fmt.Sprintf("%s\n%s", m.viewport.View(), str)
    m.viewport.SetContent(newContent)
    m.viewport.GotoBottom()
}

// performCommand - симуляция работы (позже подключим реальный контроллер)
// performCommand — это "мозг", обрабатывающий ввод пользователя.
// Она возвращает tea.Cmd, который выполнится асинхронно, чтобы не завис UI.
func performCommand(input string, state *app.GlobalState) tea.Cmd {
	return func() tea.Msg {
		// Создаем контекст с таймаутом (чтобы не висеть вечно)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Разбираем ввод на команду и аргументы
		parts := strings.Fields(input)
		if len(parts) == 0 {
			return nil // Пустой ввод
		}
		cmd := parts[0]
		args := parts[1:]

		switch cmd {

		// === КОМАНДА 1: LOAD <ARTICLE_ID> ===
		// Загружает метаданные из S3 и раскладывает файлы по полочкам
		case "load":
			if len(args) < 1 {
				return CommandResultMsg{Err: fmt.Errorf("usage: load <article_id>")}
			}
			articleID := args[0]

			// 1. Получаем "сырой" список файлов из S3
			// (Предполагаем, что state.S3 уже инициализирован в main.go)
			if state.S3 == nil {
				return CommandResultMsg{Err: fmt.Errorf("s3 client is not initialized")}
			}
			
			rawObjects, err := state.S3.ListFiles(ctx, articleID)
			if err != nil {
				return CommandResultMsg{Err: fmt.Errorf("s3 error: %w", err)}
			}

			// 2. Классифицируем файлы согласно правилам из config.yaml
			classifierEngine := classifier.New(state.Config.FileRules)
			classifiedFiles, err := classifierEngine.Process(rawObjects)
			if err != nil {
				return CommandResultMsg{Err: fmt.Errorf("classification error: %w", err)}
			}

			// 3. Обновляем глобальный State (потокобезопасно, т.к. мы в одной горутине tea.Cmd)
			state.CurrentArticleID = articleID
			state.Files = classifiedFiles

			// 4. Формируем красивый отчет для пользователя
			var report strings.Builder
			report.WriteString(fmt.Sprintf("✅ Article %s loaded successfully.\n", articleID))
			report.WriteString("Found files:\n")
			
			// Проходимся по всем найденным категориям
			for tag, files := range classifiedFiles {
				report.WriteString(fmt.Sprintf("  • [%s]: %d files\n", strings.ToUpper(tag), len(files)))
			}
			
			// Добавим предупреждение, если важных категорий нет (опционально)
			if len(classifiedFiles["sketch"]) == 0 {
				report.WriteString("⚠️ WARNING: No sketches found!\n")
			}

			return CommandResultMsg{Output: report.String()}


		// === КОМАНДА 2: RENDER <PROMPT_FILE> ===
		// Тестирует промпт, подставляя данные из загруженного артикула
		case "render":
			if len(args) < 1 {
				return CommandResultMsg{Err: fmt.Errorf("usage: render <prompt_file.yaml>")}
			}
			filename := args[0]

			// Проверяем, загружен ли вообще артикул
			if state.CurrentArticleID == "NONE" {
				return CommandResultMsg{Err: fmt.Errorf("no article loaded. use 'load <id>' first")}
			}

			// 1. Загружаем сам файл промпта
			// state.Config.App.PromptsDir берется из конфига (например "./prompts")
			fullPath := fmt.Sprintf("%s/%s", state.Config.App.PromptsDir, filename)
			p, err := prompt.Load(fullPath)
			if err != nil {
				return CommandResultMsg{Err: fmt.Errorf("failed to load prompt '%s': %w", filename, err)}
			}

			// 2. Готовим данные для шаблона (Data Context)
			// Берем реальные данные из State.
			// Например, берем первый попавшийся эскиз для демонстрации.
			imageURL := "NO_IMAGE_FOUND"
			if sketches, ok := state.Files["sketch"]; ok && len(sketches) > 0 {
				// В реальном S3 URL может быть подписанным (Presigned), но пока просто ключ
				imageURL = fmt.Sprintf("s3://%s/%s", state.Config.S3.Bucket, sketches[0].OriginalKey)
			}

			templateData := map[string]interface{}{
				"ArticleID": state.CurrentArticleID,
				"ImageURL":  imageURL,
				// Можно добавить сюда содержимое JSON из категории plm_data, если нужно
			}

			// 3. Рендерим сообщения
			messages, err := p.RenderMessages(templateData)
			if err != nil {
				return CommandResultMsg{Err: fmt.Errorf("render error: %w", err)}
			}

			// 4. Выводим результат (симуляция отправки)
			var output strings.Builder
			output.WriteString(fmt.Sprintf("📋 Rendered Prompt for model: %s\n", p.Config.Model))
			output.WriteString("--------------------------------------------------\n")
			
			for _, m := range messages {
				// Обрезаем длинный текст для красоты лога
				contentPreview := m.Content
				if len(contentPreview) > 200 {
					contentPreview = contentPreview[:200] + "...(truncated)"
				}
				output.WriteString(fmt.Sprintf("[%s]: %s\n\n", strings.ToUpper(m.Role), contentPreview))
			}

			return CommandResultMsg{Output: output.String()}


		// === КОМАНДА 3: PING ===
		case "ping":
			return CommandResultMsg{Output: "Pong! System is alive."}

		// Неизвестная команда
		default:
			return CommandResultMsg{Err: fmt.Errorf("unknown command: '%s'. Try 'load <id>' or 'render <file>'", cmd)}
		}
	}
}
```

=================

# internal/ui/view.go

```go
// Рендер
package ui

import (
    "fmt"
    "github.com/charmbracelet/lipgloss"
)

func (m MainModel) View() string {
    if !m.ready {
        return "Initializing UI..."
    }

    // Формируем строку статуса (Header)
    status := fmt.Sprintf(" ACT: %s | MODEL: %s ", 
        m.appState.CurrentArticleID, 
        m.appState.CurrentModel,
    )
    
    // Растягиваем хедер на всю ширину
    header := headerStyle.
        Width(m.viewport.Width).
        Render(status)

    // Разделительная линия
    border := lipgloss.NewStyle().
        Foreground(grayColor).
        Width(m.viewport.Width).
        Render("──────────────────────────────────────────────────")

    // Собираем всё вместе: Header + Viewport + Border + Input
    return fmt.Sprintf("%s\n%s\n%s\n%s",
        header,
        m.viewport.View(),
        border,
        m.textarea.View(),
    )
}

```

=================

# pkg/classifier/engine.go

```go
package classifier

import (
	"path/filepath"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
)

// ClassifiedFile - файл с присвоенным тегом
type ClassifiedFile struct {
	Tag          string // "sketch", "plm" и т.д.
	OriginalKey  string
	Size         int64
}

// Engine выполняет классификацию
type Engine struct {
	rules []config.FileRule
}

func New(rules []config.FileRule) *Engine {
	return &Engine{rules: rules}
}

// Process принимает список сырых объектов и возвращает карту [Tag] -> Список файлов
func (e *Engine) Process(objects []s3storage.StoredObject) (map[string][]ClassifiedFile, error) {
	result := make(map[string][]ClassifiedFile)

	for _, obj := range objects {
		filename := filepath.Base(obj.Key) // Смотрим только на имя файла, не на путь
		
		matched := false
		for _, rule := range e.rules {
			for _, pattern := range rule.Patterns {
				// Используем Case-insensitive сравнение для расширений
				// (на самом деле filepath.Match в Linux чувствителен к регистру, 
				// для надежности лучше приводить к нижнему регистру оба)
				isMatch, _ := filepath.Match(strings.ToLower(pattern), strings.ToLower(filename))
				
				if isMatch {
					result[rule.Tag] = append(result[rule.Tag], ClassifiedFile{
						Tag:         rule.Tag,
						OriginalKey: obj.Key,
						Size:        obj.Size,
					})
					matched = true
					break // Файл попал в категорию, дальше не проверяем (или проверяем, если нужен мульти-тег?)
				}
			}
			if matched {
				break
			}
		}
		
		// Если файл не попал ни под одно правило, можно сохранить его в "other"
		if !matched {
			result["other"] = append(result["other"], ClassifiedFile{
				Tag:         "other",
				OriginalKey: obj.Key,
				Size:        obj.Size,
			})
		}
	}

	return result, nil
}

```

=================

# pkg/config/config.go

```go
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// AppConfig — корневая структура конфигурации.
// Она зеркалит структуру твоего config.yaml.
type AppConfig struct {
	Models          ModelsConfig          `yaml:"models"`
	Tools           map[string]ToolConfig `yaml:"tools"`
	S3              S3Config              `yaml:"s3"`
	ImageProcessing ImageProcConfig       `yaml:"image_processing"`
	App             AppSpecific           `yaml:"app"`
    FileRules 		[]FileRule            `yaml:"file_rules"` // Новая секция
	WB				 WBConfig             `yaml:"wb"`
}

type WBConfig struct {
    APIKey string `yaml:"api_key"`
}

type FileRule struct {
    Tag      string   `yaml:"tag"`      // Например "sketch", "plm", "marketing"
    Patterns []string `yaml:"patterns"` // Glob паттерны: "*.jpg", "*_spec.json"
    Required bool     `yaml:"required"` // Если true и файлов нет -> ошибка валидации артикула
}

// ModelsConfig — настройки AI моделей.
type ModelsConfig struct {
	DefaultVision string              `yaml:"default_vision"` // Алиас по умолчанию (например, "glm-4.6v-flash")
	DefaultChat   string              `yaml:"default_chat"`   // Алиас для чата по умолчанию (например, "glm-4.5")
	Definitions   map[string]ModelDef `yaml:"definitions"`    // Словарь определений моделей
}

// ModelDef — параметры конкретной модели.
type ModelDef struct {
	Provider    string        `yaml:"provider"`   // "zai", "openai" и т.д.
	ModelName   string        `yaml:"model_name"` // Реальное имя в API
	APIKey      string        `yaml:"api_key"`    // Поддерживает ${VAR}
	MaxTokens   int           `yaml:"max_tokens"`
	Temperature float64       `yaml:"temperature"`
	Timeout     time.Duration `yaml:"timeout"` // Go умеет парсить строки вида "60s", "1m"
    BaseURL string `yaml:"base_url"` // <--- Добавить
}

// ToolConfig — настройки инструментов (импорт, поиск и т.д.).
type ToolConfig struct {
	Enabled    bool          `yaml:"enabled"`
	Timeout    time.Duration `yaml:"timeout"`
	RetryCount int           `yaml:"retry_count"`
}

// S3Config — настройки объектного хранилища.
type S3Config struct {
	Endpoint  string `yaml:"endpoint"`
	Region    string `yaml:"region"`
	Bucket    string `yaml:"bucket"`
	AccessKey string `yaml:"access_key"` // Поддерживает ${VAR}
	SecretKey string `yaml:"secret_key"` // Поддерживает ${VAR}
	UseSSL    bool   `yaml:"use_ssl"`
}

// ImageProcConfig — настройки обработки изображений.
type ImageProcConfig struct {
	MaxWidth int `yaml:"max_width"`
	Quality  int `yaml:"quality"`
}

// AppSpecific — общие настройки приложения.
type AppSpecific struct {
	Debug      bool   `yaml:"debug"`
	PromptsDir string `yaml:"prompts_dir"`
}

// Load читает YAML файл, подставляет ENV переменные и возвращает готовую структуру.
func Load(path string) (*AppConfig, error) {
	// 1. Проверяем существование файла
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("config file not found at: %s", path)
	}

	// 2. Читаем файл целиком
	rawBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// 3. Подставляем переменные окружения.
	// os.ExpandEnv заменяет ${VAR} или $VAR на значение из системы.
	contentWithEnv := os.ExpandEnv(string(rawBytes))

	// 4. Парсим YAML в структуру
	var cfg AppConfig
	if err := yaml.Unmarshal([]byte(contentWithEnv), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse yaml: %w", err)
	}

	// 5. Валидируем критические настройки
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &cfg, nil
}

// validate проверяет обязательные поля.
func (c *AppConfig) validate() error {
	if c.S3.Bucket == "" {
		return fmt.Errorf("s3.bucket is required")
	}
	if c.S3.Endpoint == "" {
		return fmt.Errorf("s3.endpoint is required")
	}
	// Можно добавить проверку наличия дефолтной модели
	if c.Models.DefaultVision != "" {
		if _, ok := c.Models.Definitions[c.Models.DefaultVision]; !ok {
			return fmt.Errorf("default_vision model '%s' is not defined in definitions", c.Models.DefaultVision)
		}
	}
	return nil
}

// Helper методы для удобства доступа (Syntactic sugar)

// GetVisionModel возвращает конфигурацию модели по умолчанию или по имени.
func (c *AppConfig) GetVisionModel(name string) (ModelDef, bool) {
	if name == "" {
		name = c.Models.DefaultVision
	}
	m, ok := c.Models.Definitions[name]
	return m, ok
}

```

=================

# pkg/factory/llm_factory.go

```go
package factory

import (
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/llm/openai" // Импорт конкретной реализации
)

// NewLLMProvider создает провайдера на основе конфига
func NewLLMProvider(cfg config.ModelDef) (llm.Provider, error) {
	switch cfg.Provider {
	case "zai", "openai", "deepseek":
		baseURL := cfg.BaseURL
		
		// Fallback defaults если URL не задан в конфиге
		if baseURL == "" {
			if cfg.Provider == "zai" {
				baseURL = "https://open.bigmodel.cn/api/paas/v4"
			} else if cfg.Provider == "openai" {
				baseURL = "https://api.openai.com/v1"
			}
		}

		return openai.New(cfg.APIKey, baseURL, cfg.Timeout), nil
	
	default:
		return nil, fmt.Errorf("unknown provider type: %s", cfg.Provider)
	}
}

```

=================

# pkg/llm/openai/client.go

```go
/*
Адаптер OpenAI-Compatible (pkg/llm/adapters/openai/client.go)
Большинство современных API (включая GLM-4.6 и DeepSeek) совместимы с форматом OpenAI. Адаптер покрывает 99% нужд.
Важно: используем стандартную библиотеку net/http и encoding/json, чтобы не тащить тяжелые SDK.
*/

package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	_ "log"
	"net/http"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/llm"
)

// Client реализует интерфейс llm.Provider
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// New создает нового клиента
func New(apiKey, baseURL string, timeout time.Duration) *Client {
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		http: &http.Client{
			Timeout: timeout,
		},
	}
}

// Структуры для JSON API (внутренние)
type apiRequest struct {
	Model       string       `json:"model"`
	Messages    []apiMessage `json:"messages"`
	Temperature float64      `json:"temperature,omitempty"`
	MaxTokens   int          `json:"max_tokens,omitempty"`
	Stream      bool         `json:"stream"`
	// Поддержка JSON режима
	ResponseFormat *apiRespFormat `json:"response_format,omitempty"`
}

type apiRespFormat struct {
	Type string `json:"type"` // "json_object"
}

type apiMessage struct {
	Role    string       `json:"role"`
	Content interface{}  `json:"content"` // string или []apiContent
}

type apiContent struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *apiImage `json:"image_url,omitempty"`
}

type apiImage struct {
	URL string `json:"url"`
}

type apiResponse struct {
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}


// Chat — реализация интерфейса
func (c *Client) Chat(ctx context.Context, req llm.ChatRequest) (string, error) {
	// 1. Конвертация нашего формата в формат API
	apiMsgs := make([]apiMessage, len(req.Messages))
	for i, msg := range req.Messages {
		if len(msg.Content) == 1 && msg.Content[0].Type == llm.TypeText {
			apiMsgs[i] = apiMessage{
				Role:    msg.Role,
				Content: msg.Content[0].Text,
			}
			continue
		}

		contentList := make([]apiContent, len(msg.Content))
		for j, part := range msg.Content {
			if part.Type == llm.TypeText {
				contentList[j] = apiContent{Type: "text", Text: part.Text}
			} else if part.Type == llm.TypeImage {
				contentList[j] = apiContent{
					Type:     "image_url",
					ImageURL: &apiImage{URL: part.ImageURL},
				}
			}
		}
		apiMsgs[i] = apiMessage{
			Role:    msg.Role,
			Content: contentList,
		}
	}

	apiReq := apiRequest{
		Model:       req.Model,
		Messages:    apiMsgs,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      false,
	}

	if req.Format == "json_object" {
		apiReq.ResponseFormat = &apiRespFormat{Type: "json_object"}
	}

	// 2. Сериализация
	bodyBytes, err := json.Marshal(apiReq)
	if err != nil {
		return "", fmt.Errorf("marshal error: %w", err)
	}

	// 3. Запрос
	url := fmt.Sprintf("%s/chat/completions", c.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("request creation error: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("api call error: %w", err)
	}
	defer resp.Body.Close()

	// 4. Чтение ответа
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("api error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result apiResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("unmarshal response error: %w. body: %s", err, string(respBody))
	}

	if result.Error != nil {
		return "", fmt.Errorf("api returned error: %s (code: %s)", result.Error.Message, result.Error.Code)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("empty choices in response")
	}

	msg := result.Choices[0].Message
	
	// FIX: Поддержка моделей, возвращающих ответ в reasoning_content
	content := msg.Content
	if content == "" && msg.ReasoningContent != "" {
		content = msg.ReasoningContent
	}

	return content, nil
}


```

=================

# pkg/llm/provider.go

```go
// Интерфейс Провайдера через который работает всё приложение.

package llm

import "context"

// Provider — контракт для любого AI-сервиса
type Provider interface {
	// Chat отправляет запрос и возвращает текстовый ответ (или JSON строку)
	Chat(ctx context.Context, req ChatRequest) (string, error)
}

```

=================

# pkg/llm/types.go

```go
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


```

=================

# pkg/prompt/loader.go

```go
// Загрузка и Рендер - чтение файла и text/template.

package prompt

import (
	"bytes"
	"fmt"
	"os"
	"text/template"

	"gopkg.in/yaml.v3"
)

// Load загружает и парсит YAML файл промпта
func Load(path string) (*PromptFile, error) {
	// 1. Проверяем наличие
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("prompt file not found: %s", path)
	}

	// 2. Читаем байты
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read error: %w", err)
	}

	// 3. Парсим YAML
	var pf PromptFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("yaml parse error: %w", err)
	}

	return &pf, nil
}

// RenderMessages принимает данные (struct или map) и возвращает готовые сообщения
// где все {{.Field}} заменены на значения.
func (pf *PromptFile) RenderMessages(data interface{}) ([]Message, error) {
	rendered := make([]Message, len(pf.Messages))

	for i, msg := range pf.Messages {
		// Создаем шаблон
		tmpl, err := template.New("msg").Parse(msg.Content)
		if err != nil {
			return nil, fmt.Errorf("template parse error in message #%d (%s): %w", i, msg.Role, err)
		}

		// Рендерим в буфер
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("template execute error in message #%d: %w", i, err)
		}

		// Сохраняем результат
		rendered[i] = Message{
			Role:    msg.Role,
			Content: buf.String(),
		}
	}

	return rendered, nil
}

```

=================

# pkg/prompt/model.go

```go
// Структуры данных - описывает формат YAML файла промпта. 
package prompt

// PromptFile описывает структуру YAML-файла с промптом
type PromptFile struct {
	Config   PromptConfig `yaml:"config"`
	Messages []Message    `yaml:"messages"`
}

// PromptConfig - настройки модели для конкретного промпта
type PromptConfig struct {
	Model       string  `yaml:"model"`       // Например "zai-vision/glm-4.5v"
	Temperature float64 `yaml:"temperature"` 
	MaxTokens   int     `yaml:"max_tokens"`
	Format      string  `yaml:"format"`      // "json_object" или text
}

// Message - одно сообщение в чате
type Message struct {
	Role    string `yaml:"role"`    // system, user, assistant
	Content string `yaml:"content"` // Шаблон с {{.Variables}}
}

```

=================

# pkg/s3storage/client.go

```go
// "Тупой" клиент. классификатор файлов будет отдельно

package s3storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	_ "path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

type Client struct {
    api    *minio.Client
    bucket string
}

// StoredObject - сырой объект из S3
type StoredObject struct {
	Key          string
	Size         int64
	LastModified time.Time
}

type FileMeta struct {
    Key  string
    Size int64
    Type string
}

// New создает клиент, используя наш конфиг
func New(cfg config.S3Config) (*Client, error) {
    minioClient, err := minio.New(cfg.Endpoint, &minio.Options{
        Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
        Secure: cfg.UseSSL,
        Region: cfg.Region,
    })
    if err != nil {
        return nil, err
    }

    return &Client{
        api:    minioClient,
        bucket: cfg.Bucket,
    }, nil
}

// ListFiles возвращает ВСЕ файлы по префиксу (артикулу)
func (c *Client) ListFiles(ctx context.Context, prefix string) ([]StoredObject, error) {
	// Нормализация префикса (добавляем слеш, если это "папка")
	if !strings.HasSuffix(prefix, "/") && prefix != "" {
		prefix += "/"
	}

	var objects []StoredObject
	
	opts := minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}

	for obj := range c.api.ListObjects(ctx, c.bucket, opts) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		// Пропускаем саму "папку"
		if obj.Key == prefix {
			continue
		}
		objects = append(objects, StoredObject{
			Key:          obj.Key,
			Size:         obj.Size,
			LastModified: obj.LastModified,
		})
	}
	
	if len(objects) == 0 {
		// Это можно считать ошибкой или просто пустым списком - зависит от логики
		// Для утилиты лучше вернуть ошибку, чтобы пользователь сразу понял
		return nil, fmt.Errorf("path '%s' not found or empty", prefix)
	}

	return objects, nil
}

// DownloadFile скачивает объект целиком в память
func (c *Client) DownloadFile(ctx context.Context, key string) ([]byte, error) {
    obj, err := c.api.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
    if err != nil {
        return nil, err
    }
    defer obj.Close()

    // Читаем в буфер
    buf := new(bytes.Buffer)
    if _, err := io.Copy(buf, obj); err != nil {
        return nil, err
    }

    return buf.Bytes(), nil
}

```

=================

# pkg/tools/registry.go

```go
// Реестр для хранения и поиска инструментов.
package tools

import (
	"fmt"
	"sync"
)

// Registry — потокобезопасное хранилище инструментов.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry создает новый пустой реестр.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register добавляет инструмент в реестр.
func (r *Registry) Register(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Definition().Name] = tool
}

// Get ищет инструмент по имени.
func (r *Registry) Get(name string) (Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	tool, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool '%s' not found", name)
	}
	return tool, nil
}

// GetDefinitions возвращает список всех определений для отправки в LLM.
func (r *Registry) GetDefinitions() []ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	defs := make([]ToolDefinition, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, t.Definition())
	}
	return defs
}

```

=================

# pkg/tools/std/s3_tools.go

```go
/* инструменты для работы с S3 в пакете pkg/tools/std/
Нам понадобятся два инструмента:

list_s3_files: Аналог ls. Позволяет агенту "осмотреться" в бакете и найти нужные файлы (артикулы, документы).
read_s3_object: Аналог cat. Позволяет агенту прочитать содержимое файла (JSON текст или получить ссылку на картинку).
*/
package std

import (
	"bytes"
	"context"
	"encoding/base64" // Теперь используем!
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"path/filepath"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/config" // Нужен конфиг для параметров ресайза
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/tools"

	"github.com/nfnt/resize" // go get github.com/nfnt/resize
)

// --- Tool: list_s3_files ---
// Позволяет агенту узнать, какие файлы есть по указанному пути (префиксу).

type S3ListTool struct {
	client *s3storage.Client
}

func NewS3ListTool(c *s3storage.Client) *S3ListTool {
	return &S3ListTool{client: c}
}

func (t *S3ListTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "list_s3_files",
		Description: "Возвращает список файлов в S3 хранилище по указанному пути (префиксу). Используй это, чтобы найти артикулы или проверить наличие файлов.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"prefix": map[string]interface{}{
					"type":        "string",
					"description": "Путь к папке (например '12345/' или пусто для корня).",
				},
			},
			// prefix не обязателен (тогда покажет корень)
		},
	}
}

func (t *S3ListTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Prefix string `json:"prefix"`
	}
	// Если аргументы пустые или кривые, пробуем продолжить с дефолтом
	if argsJSON != "" {
		_ = json.Unmarshal([]byte(argsJSON), &args)
	}

	// Вызываем наш S3 клиент
	files, err := t.client.ListFiles(ctx, args.Prefix)
	if err != nil {
		return "", fmt.Errorf("s3 list error: %w", err)
	}

	// Упрощаем ответ для LLM (экономим токены)
	// Отдаем только имена и размеры, без метаданных
	type simpleFile struct {
		Key  string `json:"key"`
		Size string `json:"size"` // "10.5 KB" читаемее для LLM, чем байты
	}

	simpleList := make([]simpleFile, 0, len(files))
	for _, f := range files {
		simpleList = append(simpleList, simpleFile{
			Key:  f.Key,
			Size: formatSize(f.Size),
		})
	}

	data, err := json.Marshal(simpleList)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// --- Tool: read_s3_object ---
// Позволяет прочитать содержимое файла.
// Если это текст/JSON — возвращает текст.
// Если это картинка — возвращает сообщение, что это бинарный файл (или base64, если попросят).
// Для агента безопаснее читать только текст, а картинки обрабатывать через Vision-инструменты.

type S3ReadTool struct {
	client *s3storage.Client
}

func NewS3ReadTool(c *s3storage.Client) *S3ReadTool {
	return &S3ReadTool{client: c}
}

func (t *S3ReadTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "read_s3_object",
		Description: "Читает содержимое файла из S3. Поддерживает текстовые файлы (JSON, TXT, MD). Не используй для картинок.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"key": map[string]interface{}{
					"type":        "string",
					"description": "Полный путь к файлу (ключ), полученный из list_s3_files.",
				},
			},
			"required": []string{"key"},
		},
	}
}

func (t *S3ReadTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Простая защита от дурака (чтобы не качать гигабайтные видео)
	ext := strings.ToLower(filepath.Ext(args.Key))
	if isBinaryExt(ext) {
		return "", fmt.Errorf("file type '%s' is binary/image. Use specialized vision tools for images", ext)
	}

	// Скачиваем
	contentBytes, err := t.client.DownloadFile(ctx, args.Key)
	if err != nil {
		return "", fmt.Errorf("s3 download error: %w", err)
	}

	// Возвращаем как строку (предполагаем UTF-8)
	// Если нужно вернуть JSON как есть — возвращаем.
	// Ограничиваем длину, чтобы не забить контекст LLM (например, 20KB)
	const maxTextSize = 20000 
	if len(contentBytes) > maxTextSize {
		return string(contentBytes[:maxTextSize]) + "\n...[TRUNCATED]", nil
	}

	return string(contentBytes), nil
}

// --- Helpers ---

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func isBinaryExt(ext string) bool {
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".zip", ".pdf", ".mp4":
		return true
	}
	return false
}

// --- Tool: read_s3_image ---
/*
реализуем инструмент read_image_base64 (или улучшим read_s3_object), который будет:

- Скачивать картинку.
- Ресайзить её согласно конфигу.
- Возвращать Base64 строку (готовую для отправки в Vision API).
- Для ресайза нам понадобится библиотека github.com/nfnt/resize или стандартная image.

Улучшенный s3_tools.go с поддержкой изображений: добавим новый инструмент S3ReadImageTool. Он будет специализированным.
*/

type S3ReadImageTool struct {
	client *s3storage.Client
	cfg    config.ImageProcConfig
}

func NewS3ReadImageTool(c *s3storage.Client, cfg config.ImageProcConfig) *S3ReadImageTool {
	return &S3ReadImageTool{
		client: c,
		cfg:    cfg,
	}
}

func (t *S3ReadImageTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "read_s3_image_base64",
		Description: "Скачивает изображение из S3, оптимизирует его (resize) и возвращает в формате Base64. Используй это для Vision-анализа.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"key": map[string]interface{}{
					"type": "string",
				},
			},
			"required": []string{"key"},
		},
	}
}

func (t *S3ReadImageTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &args)

	// 1. Проверяем расширение
	ext := strings.ToLower(filepath.Ext(args.Key))
	if !isImageExt(ext) {
		return "", fmt.Errorf("file '%s' is not an image", args.Key)
	}

	// 2. Скачиваем байты
	rawBytes, err := t.client.DownloadFile(ctx, args.Key)
	if err != nil {
		return "", err
	}

	// 3. Декодируем и Ресайзим (если включено в конфиге)
	// Если конфиг пустой или ширина 0 -> пропускаем ресайз
	if t.cfg.MaxWidth > 0 {
		img, _, err := image.Decode(bytes.NewReader(rawBytes))
		if err != nil {
			return "", fmt.Errorf("image decode error: %w", err)
		}

		// Ресайз с сохранением пропорций (width, 0, ...)
		// Используем Lanczos3 для качества
		newImg := resize.Resize(uint(t.cfg.MaxWidth), 0, img, resize.Lanczos3)

		// Кодируем обратно в JPEG (для уменьшения веса)
		buf := new(bytes.Buffer)
		err = jpeg.Encode(buf, newImg, &jpeg.Options{Quality: t.cfg.Quality})
		if err != nil {
			return "", fmt.Errorf("jpeg encode error: %w", err)
		}
		rawBytes = buf.Bytes()
	}

	// 4. Base64 encode
	b64 := base64.StdEncoding.EncodeToString(rawBytes)
	
	// Возвращаем как префикс Data URI (чтобы сразу вставлять в API)
	// Или просто raw base64, зависит от того, что ждет провайдер.
	// Обычно провайдеры (OpenAI) хотят data:image/jpeg;base64,...
	mimeType := "image/jpeg" // Мы конвертировали в jpeg при ресайзе
	if t.cfg.MaxWidth == 0 && ext == ".png" {
		mimeType = "image/png"
	}

	return fmt.Sprintf("data:%s;base64,%s", mimeType, b64), nil
}

func isImageExt(ext string) bool {
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	}
	return false
}

// Регистрация в main.go: reg.Register(std.NewS3ReadImageTool(s3Client, cfg.ImageProcessing))

```

=================

# pkg/tools/std/wb_catalog.go

```go
// Реализация конкретных инструментов для WB (Subjects, Categories).
package std

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// --- Tool: get_wb_parent_categories ---

type WbParentCategoriesTool struct {
	client *wb.Client
}

func NewWbParentCategoriesTool(c *wb.Client) *WbParentCategoriesTool {
	return &WbParentCategoriesTool{client: c}
}

func (t *WbParentCategoriesTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_parent_categories",
		Description: "Возвращает список родительских категорий Wildberries (например: Женщинам, Электроника). Используй это, чтобы найти ID категории.",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{}, // Нет параметров
		},
	}
}

func (t *WbParentCategoriesTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// Аргументы не нужны, но JSON может быть "{}"
	cats, err := t.client.GetParentCategories(ctx)
	if err != nil {
		return "", fmt.Errorf("wb api error: %w", err)
	}
	
	// Сериализуем результат
	data, err := json.Marshal(cats)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// --- Tool: get_wb_subjects ---

type WbSubjectsTool struct {
	client *wb.Client
}

func NewWbSubjectsTool(c *wb.Client) *WbSubjectsTool {
	return &WbSubjectsTool{client: c}
}

func (t *WbSubjectsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_subjects",
		Description: "Возвращает список предметов (подкатегорий) для заданной родительской категории.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"parentID": map[string]interface{}{
					"type":        "integer",
					"description": "ID родительской категории (получи его из get_wb_parent_categories)",
				},
			},
			"required": []string{"parentID"},
		},
	}
}

func (t *WbSubjectsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		ParentID int `json:"parentID"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments json: %w", err)
	}

	// Используем метод GetAllSubjects (с пагинацией), который мы делали ранее
	subjects, err := t.client.GetAllSubjectsLazy(ctx, args.ParentID)
	if err != nil {
		return "", fmt.Errorf("wb api error: %w", err)
	}

	data, err := json.Marshal(subjects)
	if err != nil {
		return "", err
	}
	return string(data), nil
}


```

=================

# pkg/tools/types.go

```go
// Интерфейс Tool и структуры определений. 

package tools

import "context"

// ToolDefinition описывает инструмент для LLM (Function Calling API format).
type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"` // JSON Schema объекта аргументов
}

// Tool — контракт, который должен реализовать любой инструмент.
type Tool interface {
	// Definition возвращает описание инструмента для LLM.
	Definition() ToolDefinition
	
	// Execute выполняет логику инструмента.
	// argsJSON — это сырой JSON с аргументами, который прислала LLM.
	// Возвращает результат (обычно JSON) или ошибку.
	Execute(ctx context.Context, argsJSON string) (string, error)
}


```

=================

# pkg/wb/client.go

```go
package wb

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "strconv"
    "time"

    "golang.org/x/time/rate" // <--- Добавь этот импорт
)

const (
    // Лимиты для Content API (согласно документации)
    BurstLimit    = 5
    RateLimit     = 100 // запросов в минуту
    RetryAttempts = 3
	DefaultBaseURL = "https://content-api.wildberries.ru"
)

type Client struct {
    apiKey     string
    baseURL    string
    httpClient *http.Client
    limiter    *rate.Limiter // <--- Лимитер
}

func New(apiKey string) *Client {
    // 100 req/min = 1.66 req/sec
    // Но лучше быть чуть консервативнее, скажем 1.5 rps
    r := rate.Limit(1.6) 
    
    return &Client{
        apiKey:  apiKey,
        baseURL: DefaultBaseURL,
        httpClient: &http.Client{
            Timeout: 30 * time.Second,
        },
        // Burst=5, Rate=1.6 req/s
        limiter: rate.NewLimiter(r, BurstLimit),
    }
}

// genericGet с поддержкой Rate Limit и Retries
func (c *Client) get(ctx context.Context, path string, params url.Values, dest interface{}) error {
    u, err := url.Parse(c.baseURL + path)
    if err != nil {
        return fmt.Errorf("invalid url: %w", err)
    }
    if params != nil {
        u.RawQuery = params.Encode()
    }

    var lastErr error

    // Retry loop
    for i := 0; i < RetryAttempts; i++ {
        // 1. Ждем разрешения от лимитера (блокирует горутину, если превысили лимит)
        if err := c.limiter.Wait(ctx); err != nil {
            return fmt.Errorf("rate limiter wait: %w", err)
        }

        req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
        if err != nil {
            return err
        }

        req.Header.Set("Authorization", c.apiKey)
        req.Header.Set("Content-Type", "application/json")
        req.Header.Set("Accept", "application/json")
        // Можно добавить локаль, если нужно
        // req.Header.Set("Accept-Language", "ru")

        resp, err := c.httpClient.Do(req)
        if err != nil {
            lastErr = err
            continue // Сетевая ошибка, пробуем еще
        }
        defer resp.Body.Close()

        body, _ := io.ReadAll(resp.Body)

        // Обработка 429 (Too Many Requests)
        if resp.StatusCode == http.StatusTooManyRequests {
            // Читаем заголовок X-Ratelimit-Retry или Retry-After
            retryAfter := 1 * time.Second // Дефолт
            if s := resp.Header.Get("X-Ratelimit-Retry"); s != "" {
                if sec, err := strconv.Atoi(s); err == nil {
                    retryAfter = time.Duration(sec) * time.Second
                }
            }
            
            // Ждем и ретраем
            select {
            case <-ctx.Done():
                return ctx.Err()
            case <-time.After(retryAfter):
                continue
            }
        }

        if resp.StatusCode != http.StatusOK {
            return fmt.Errorf("wb api error: status %d, body: %s", resp.StatusCode, string(body))
        }

        if err := json.Unmarshal(body, dest); err != nil {
            return fmt.Errorf("unmarshal error: %w", err)
        }

        return nil // Успех
    }

    return fmt.Errorf("max retries exceeded, last error: %v", lastErr)
}
// структура и метод для пинга контентного api wb (именно контентного так как для разных api свои ручки)
type PingResponse struct {
    Status string `json:"Status"`
    TS     string `json:"TS"` // Timestamp
}

// Ping проверяет связь именно с сервисом Content API
func (c *Client) Ping(ctx context.Context) error {
    // В документации сказано, что URL для Content: https://content-api.wildberries.ru/ping
    // Наш c.baseURL по умолчанию как раз https://content-api.wildberries.ru
    
    // ВАЖНО: Ping возвращает простой JSON, а не обертку APIResponse[T].
    // Поэтому используем c.get() с умом или пишем отдельный запрос, если c.get заточен под APIResponse.
    // Но наш c.get() просто делает Unmarshal в dest, так что всё ок.

    var resp PingResponse
    
    // Путь /ping
    // Params nil
    err := c.get(ctx, "/ping", nil, &resp)
    if err != nil {
        return fmt.Errorf("ping failed: %w", err)
    }

    if resp.Status != "OK" {
        return fmt.Errorf("ping status not OK: %s", resp.Status)
    }

    return nil
}

```

=================

# pkg/wb/colors.go

```go
/* 
Для реализации нечеткого поиска (Fuzzy Search) по названиям цветов в Go подойдет библиотека, вычисляющая расстояние Левенштейна или использующая триграммы. Для простоты  github.com/lithammer/fuzzysearch или простая реализация на базе стандартной библиотеки (если не хотим лишних зависимостей).

Учитывая, что справочник цветов не такой уж огромный (не миллионы, а тысячи), простой перебор с ранжированием по сходству будет работать мгновенно.

Как это интегрировать в Flow?
Загрузка при старте:
В main.go или при первом обращении к тулу загружаем цвета:

go
colors, err := wbClient.GetColors(ctx)
colorService := wb.NewColorService(colors)
Использование в Tool:
Когда LLM просит найти цвет, мы вызываем colorService.FindTopMatches("персиковый", 5).
Это вернет:

"персиковый"

"персиковый джем"

"персиковый мелок"

И этот короткий список мы отдаем LLM, чтобы она выбрала лучший вариант.
*/
package wb

import (
    "sort"
    "strings"
    _ "unicode"
)

// SearchMatch - результат поиска
type SearchMatch struct {
    Color Color
    Score float64 // Чем больше, тем лучше (0.0 - 1.0)
}

// ColorService - обертка над списком цветов для поиска
type ColorService struct {
    colors []Color
}

func NewColorService(colors []Color) *ColorService {
    return &ColorService{colors: colors}
}

// FindTopMatches ищет топ-N похожих цветов
func (s *ColorService) FindTopMatches(query string, topN int) []Color {
    query = strings.ToLower(strings.TrimSpace(query))
    var matches []SearchMatch

    for _, c := range s.colors {
        target := strings.ToLower(c.Name)
        
        // 1. Точное совпадение - высший приоритет
        if target == query {
            matches = append(matches, SearchMatch{Color: c, Score: 1.0})
            continue
        }

        // 2. Вхождение (substring)
        if strings.Contains(target, query) {
            // Штраф за лишние символы: len(query) / len(target)
            // Если ищем "красный", то "темно-красный" получит меньший скор, чем "красный"
            score := float64(len(query)) / float64(len(target)) * 0.9 
            matches = append(matches, SearchMatch{Color: c, Score: score})
            continue
        }

        // 3. Обратное вхождение (если запрос длиннее: "ярко-красный" ищем "красный")
        if strings.Contains(query, target) {
            score := float64(len(target)) / float64(len(query)) * 0.8
            matches = append(matches, SearchMatch{Color: c, Score: score})
            continue
        }
        
        // 4. (Опционально) Расстояние Левенштейна для опечаток
        // Можно добавить, если нужно, но для цветов обычно хватает substring
    }

    // Сортируем по убыванию Score
    sort.Slice(matches, func(i, j int) bool {
        return matches[i].Score > matches[j].Score
    })

    // Берем топ-N
    result := make([]Color, 0, topN)
    for i := 0; i < len(matches) && i < topN; i++ {
        result = append(result, matches[i].Color)
    }

    return result
}

```

=================

# pkg/wb/content.go

```go
// Бизнес-логика методов
package wb

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// GetParentCategories возвращает список родительских категорий
func (c *Client) GetParentCategories(ctx context.Context) ([]ParentCategory, error) {
	var resp APIResponse[[]ParentCategory]
	
	err := c.get(ctx, "/content/v2/object/parent/all", nil, &resp)
	if err != nil {
		return nil, err
	}
	
	if resp.Error {
		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
	}

	return resp.Data, nil
}

// GetSubjects возвращает список предметов (подкатегорий).
// Можно фильтровать по parentID, name и т.д. (см. доку).
// Для простоты пока без фильтров или с опциональными.
// func (c *Client) GetSubjects(ctx context.Context, parentID int) ([]Subject, error) {
// 	params := url.Values{}
// 	if parentID > 0 {
// 		params.Set("parentID", fmt.Sprintf("%d", parentID))
// 	}
	
// 	// Лимит WB может отдавать много данных, возможно нужна пагинация (offset/limit)
// 	// Но в API /object/all пагинация делается через top/limit? 
// 	// В доке написано: "limit: int, offset: int". 
// 	// Давай добавим дефолтные лимиты, чтобы не качать всё
// 	params.Set("limit", "1000") 

// 	var resp APIResponse[[]Subject]
	
// 	err := c.get(ctx, "/content/v2/object/all", params, &resp)
// 	if err != nil {
// 		return nil, err
// 	}

// 	if resp.Error {
// 		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
// 	}

// 	return resp.Data, nil
// }

// // GetAllSubjects выкачивает ВСЕ предметы, автоматически листая страницы. deprecated?
// func (c *Client) GetAllSubjects(ctx context.Context, parentID int) ([]Subject, error) {
//     var allSubjects []Subject
//     limit := 1000
//     offset := 0

//     for {
//         params := url.Values{}
//         params.Set("limit", strconv.Itoa(limit))
//         params.Set("offset", strconv.Itoa(offset))
//         if parentID > 0 {
//             params.Set("parentID", strconv.Itoa(parentID))
//         }

//         var resp APIResponse[[]Subject]
//         // Наш умный .get() сам подождет лимиты
//         err := c.get(ctx, "/content/v2/object/all", params, &resp)
//         if err != nil {
//             return nil, err
//         }
//         if resp.Error {
//             return nil, fmt.Errorf("wb error: %s", resp.ErrorText)
//         }

//         // Добавляем полученное
//         allSubjects = append(allSubjects, resp.Data...)

//         // Если вернулось меньше лимита, значит это последняя страница
//         if len(resp.Data) < limit {
//             break
//         }

//         // Готовимся к следующей странице
//         offset += limit
//     }

//     return allSubjects, nil
// }

// FetchSubjectsPage - низкоуровневый запрос одной страницы + для GetAllSubjectsLazy
func (c *Client) FetchSubjectsPage(ctx context.Context, parentID, limit, offset int) ([]Subject, error) {
    params := url.Values{}
    params.Set("limit", strconv.Itoa(limit))
    params.Set("offset", strconv.Itoa(offset))
    if parentID > 0 {
        params.Set("parentID", strconv.Itoa(parentID))
    }

    var resp APIResponse[[]Subject]
    err := c.get(ctx, "/content/v2/object/all", params, &resp)
    if err != nil {
        return nil, err
    }
    if resp.Error {
        return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
    }
    return resp.Data, nil
}

// GetAllSubjects2 - "ленивый" метод, который выкачивает всё используя FetchSubjectsPage. Основной метод и для Tools в том числе
func (c *Client) GetAllSubjectsLazy(ctx context.Context, parentID int) ([]Subject, error) {
    var all []Subject
    limit := 1000
    offset := 0

    for {
        batch, err := c.FetchSubjectsPage(ctx, parentID, limit, offset)
        if err != nil {
            return nil, err
        }
        
        all = append(all, batch...)

        if len(batch) < limit {
            break
        }
        offset += limit
    }
    return all, nil
}

// GetCharacteristics получает хар-ки для предмета
func (c *Client) GetCharacteristics(ctx context.Context, subjectID int) ([]Characteristic, error) {
	path := fmt.Sprintf("/content/v2/object/charcs/%d", subjectID)
	
	var resp APIResponse[[]Characteristic]
	
	err := c.get(ctx, path, nil, &resp)
	if err != nil {
		return nil, err
	}

	if resp.Error {
		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
	}

	return resp.Data, nil
}

/* добавляем цвет wb. URL: /content/v2/directory/colors. Это справочник ("directory"), а не объект.
Внимание! 
Использование в AI-агенте (Tool для LLM)
Это классический кейс для RAG (Retrieval Augmented Generation).
Список цветов может быть на 5000+ строк. Мы не можем запихнуть его весь в контекст LLM.

Стратегия:

При старте приложения (или раз в сутки) скачиваем GetColors() и кэшируем в памяти (в GlobalState).

Когда нужно определить цвет товара, мы используем Fuzzy Search (нечеткий поиск) внутри Go, а не спрашиваем LLM "выбери из 5000 вариантов".

Пример сценария:

LLM проанализировала эскиз: "Цвет платья: светло-персиковый".

Мы (Go-код) ищем в справочнике colors что-то похожее на "светло-персиковый".

Находим: "персиковый", "персиковый мелок", "светло-персиковый".

Отдаем LLM эти 3 варианта: "Выбери точный цвет WB из: [...]".

LLM выбирает "персиковый мелок".

Иметь в виду, что использовать его надо с кэшированием, а не дергать каждый раз.
*/

// GetColors возвращает справочник всех допустимых цветов WB
func (c *Client) GetColors(ctx context.Context) ([]Color, error) {
    // Этот список может быть огромным. В доке не сказано про limit/offset.
    // Обычно справочники отдаются целиком или имеют поиск.
    // Если в query params нет limit, значит отдается всё или топ-N.
    // Судя по документации, параметров пагинации НЕТ, только locale.
    
    var resp APIResponse[[]Color]
    err := c.get(ctx, "/content/v2/directory/colors", nil, &resp)
    if err != nil {
        return nil, err
    }
    if resp.Error {
        return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
    }
    return resp.Data, nil
}

// Метод GetGenders GetGenders (в API называется "Kinds") возвращает справочник полов/видов.
// Пример: "Мужской", "Женский", "Детский"
func (c *Client) GetGenders(ctx context.Context) ([]string, error) {
    // URL из документации: /content/v2/directory/kinds
    var resp APIResponse[[]string]
    
    err := c.get(ctx, "/content/v2/directory/kinds", nil, &resp)
    if err != nil {
        return nil, err
    }
    
    if resp.Error {
        return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
    }

    return resp.Data, nil
}


// GetSeasons возвращает справочник сезонов.
func (c *Client) GetSeasons(ctx context.Context) ([]string, error) {
    // URL: /content/v2/directory/seasons
    var resp APIResponse[[]string]
    
    err := c.get(ctx, "/content/v2/directory/seasons", nil, &resp)
    if err != nil {
        return nil, err
    }
    
    if resp.Error {
        return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
    }

    return resp.Data, nil
}

// pkg/wb/types.go
type Tnved struct {
    Tnved string `json:"tnved"` // Код (строка, т.к. может начинаться с 0)
    IsKiz bool   `json:"isKiz"` // Требует ли маркировки КИЗ
}

// GetTnved возвращает список кодов ТНВЭД для конкретного предмета
func (c *Client) GetTnved(ctx context.Context, subjectID int, search string) ([]Tnved, error) {
    // Параметры
    params := url.Values{}
    params.Set("subjectID", fmt.Sprintf("%d", subjectID))
    if search != "" {
        params.Set("search", search) // Опциональный поиск по коду
    }
    
    var resp APIResponse[[]Tnved]
    
    // URL: /content/v2/directory/tnved
    err := c.get(ctx, "/content/v2/directory/tnved", params, &resp)
    if err != nil {
        return nil, err
    }
    
    if resp.Error {
        return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
    }

    return resp.Data, nil
}

/* 
Сценарий использования GetTnved (Flow)
Вот как это будет выглядеть в диалоге с агентом:

Пользователь: "Заведи карточку на шелковую блузку".
LLM: (Анализ...) "Блузка" -> это SubjectID 123 (нашла через поиск предметов).
LLM: "Мне нужно выбрать код ТНВЭД для блузки. Вызываю get_tnved(subjectID=123)".
Tool: Возвращает список:
6206100000 (из шелка)
6206200000 (из шерсти)
...
LLM: "Ага, раз блузка шелковая, беру код 6206100000".
Это подтверждает, что ТНВЭД должен быть инструментом (Tool), а не частью предзагруженного словаря.
=============================
*/

// GetVats возвращает список ставок НДС. Пример: ["22%", "Без НДС", "10%"]
func (c *Client) GetVats(ctx context.Context) ([]string, error) {
    // URL: /content/v2/directory/vat
    var resp APIResponse[[]string]
    
    // В доке пример с locale=ru, добавим это, хотя это дефолт
    params := url.Values{}
    params.Set("locale", "ru")

    err := c.get(ctx, "/content/v2/directory/vat", params, &resp)
    if err != nil {
        return nil, err
    }
    
    if resp.Error {
        return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
    }

    return resp.Data, nil
}


// GetCountries возвращает список стран производства.
func (c *Client) GetCountries(ctx context.Context) ([]Country, error) {
    // URL: /content/v2/directory/countries
    var resp APIResponse[[]Country]
    
    // locale=ru (хотя по дефолту ru)
    params := url.Values{}
    params.Set("locale", "ru")

    err := c.get(ctx, "/content/v2/directory/countries", params, &resp)
    if err != nil {
        return nil, err
    }
    
    if resp.Error {
        return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
    }

    return resp.Data, nil
}

/* 
Резюме по справочникам
Мы собрали фулл-хаус статических справочников:
Цвета (Colors) -> Номенклатура (nmID)
Пол (Genders) -> Обязательное поле карточки
Страна (Countries) -> Обязательное поле
Сезон (Seasons) -> Обязательное поле (часто)
НДС (Vats) -> Финансы
Динамический: ТНВЭД (по запросу).

Теперь у нас есть всё, чтобы AI-агент мог "собрать" JSON карточки товара, опираясь на реальные, валидные значения WB, а не галлюцинируя "Страна: Поднебесная" или "Сезон: Дождливый".
*/

```

=================

# pkg/wb/dictionaries.go

```go
package wb

import (
	"context"
	_ "fmt"
)

// Dictionaries - контейнер для всех справочников
type Dictionaries struct {
    Colors  []Color
    Genders []string
	Countries []Country
    Seasons []string
	Vats    []string // <--- Добавили НДС
}

// LoadDictionaries загружает все необходимые справочники параллельно
func (c *Client) LoadDictionaries(ctx context.Context) (*Dictionaries, error) {
    // В будущем лучше переделать на errgroup.Group для параллелизма, 
    // чтобы загрузка 3 справочников занимала время самого медленного, а не сумму.
    
    colors, err := c.GetColors(ctx)
    if err != nil { return nil, err }

    genders, err := c.GetGenders(ctx)
    if err != nil { return nil, err }

    seasons, err := c.GetSeasons(ctx) // <--- Добавили
    if err != nil { return nil, err }

    vats, err := c.GetVats(ctx) // <--- Загружаем
    if err != nil { return nil, err }

	countries, err := c.GetCountries(ctx) // <--- Загружаем
    if err != nil { return nil, err }

    return &Dictionaries{
        Colors:  colors,
        Genders: genders,
        Seasons: seasons,
		Vats:    vats,
		Countries: countries,
    }, nil
}

/* 
===
Использование в main.go
// ... внутри main
fmt.Print("📚 Loading WB dictionaries... ")
dicts, err := wbClient.LoadDictionaries(context.Background())
if err != nil {
    log.Fatal(err)
}
// Сохраняем в State
state.Dictionaries = dicts 
fmt.Printf("OK (%d colors, %d genders)\n", len(dicts.Colors), len(dicts.Genders))
===
Это решит проблему "разрозненных сущностей". Все справочные данные будут лежать в одном месте state.Dictionaries и будут доступны для Tools и LLM.

Пример Tool для пола:
LLM: "Пол: для мальчика"
Tool match_gender: Ищет "для мальчика" в state.Dictionaries.Genders. Находит "Детский" (если он там есть) или возвращает список доступных: ["Мужской", "Женский", "Детский", "Унисекс"].
*/

// ================

```

=================

# pkg/wb/types.go

```go
// Модели данных

package wb

// Common Response Wrapper
type APIResponse[T any] struct {
	Data      T           `json:"data"`
	Error     bool        `json:"error"`
	ErrorText string      `json:"errorText"`
	// AdditionalErrors игнорируем, так как тип плавает (string/null)
}

// 1. Parent Category
type ParentCategory struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	IsVisible bool   `json:"isVisible"`
}

// 2. Subject (Предмет)
type Subject struct {
	SubjectID   int    `json:"subjectID"`
	ParentID    int    `json:"parentID"`
	SubjectName string `json:"subjectName"`
	ParentName  string `json:"parentName"`
}

// 3. Characteristic (Характеристика)
type Characteristic struct {
	CharcID     int    `json:"charcID"`
	SubjectName string `json:"subjectName"`
	SubjectID   int    `json:"subjectID"`
	Name        string `json:"name"`
	Required    bool   `json:"required"`
	UnitName    string `json:"unitName"`
	MaxCount    int    `json:"maxCount"`
	Popular     bool   `json:"popular"`
	CharcType   int    `json:"charcType"` // 1: string, 4: number? Нужно уточнять в доке, но int безопасен
}

type Color struct {
    Name       string `json:"name"`       // "персиковый мелок"
    ParentName string `json:"parentName"` // "оранжевый"
}

type Country struct {
    Name     string `json:"name"`     // "Китай"
    FullName string `json:"fullName"` // "Китайская Народная Республика"
}


```

=================


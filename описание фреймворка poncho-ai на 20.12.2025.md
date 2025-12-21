Вот анализ логики фреймворка Poncho AI и его сокращенная версия кода.

### Анализ логики реализации фреймворка (Poncho AI)

Фреймворк спроектирован как **Tool-centric** и **Stateful** система с упором на TUI (текстовый интерфейс). Основная философия: "Минимум магии внутри, строгие контракты снаружи".

**1. Централизованное управление состоянием (`state.go`):**

* **Single Source of Truth:** `GlobalState` является единственным источником правды. Он потокобезопасен (защищен `sync.RWMutex`), что критично, так как UI (цикл отрисовки) и Агент (длительные сетевые запросы) работают в разных горутинах.
* **Working Memory ("Рабочая память"):** В отличие от простых чат-ботов, здесь есть явное разделение на "Историю чата" (`History`) и "Контекст задачи" (`Files`). Результаты работы Vision-моделей (описания картинок) сохраняются в метаданные файлов (`FileMeta`), а не просто теряются в истории.
* **Context Injection:** Метод `BuildAgentContext` реализует паттерн RAG "на лету": он собирает описания из "Рабочей памяти" и внедряет их в системный промпт перед каждым запросом к LLM. Это экономит токены (картинка не отправляется каждый раз) и удерживает контекст.

**2. Архитектура UI (Elm / Bubble Tea):**

* Строгое разделение на **Model** (данные), **View** (рендер) и **Update** (логика).
* **Асинхронность:** Тяжелые операции (сеть, AI) никогда не блокируют UI. Они запускаются через `tea.Cmd` и возвращают результат сообщением (`Msg`), которое обрабатывается в следующем цикле `Update`.

**3. Инструменты (Tools):**

* **Принцип "Raw In, String Out":** Инструменты принимают сырой JSON-строку и возвращают строку. Это перекладывает ответственность за парсинг на сам инструмент, делая ядро фреймворка агностичным к типам данных.
* **Dependency Injection:** Инструменты создаются через конструкторы, принимающие зависимости (например, `wb.Client` или `s3storage.Client`). Это делает их тестируемыми и независимыми от глобального состояния.

**4. Конфигурация и Классификация:**

* **Rule-based Classification:** Фреймворк не просто скачивает файлы, а классифицирует их по тегам (sketch, plm) на основе конфигурируемых правил (glob patterns). Это позволяет агенту оперировать понятиями "дай мне эскиз", а не именами файлов.

***

### Укороченная версия `go-code-export.md` (Architecture Reference)

В этой версии оставлены структуры данных, интерфейсы и сигнатуры функций. Реализация (тело функций) заменена на комментарии, описывающие логику. Это идеальный скелет для понимания того, куда добавлять новый функционал.

```go
// ==========================================
// INTERNAL/APP/STATE.GO
// Управление глобальным состоянием и памятью
// ==========================================

package app

import (
	"sync"
	"github.com/ilkoid/poncho-ai/pkg/classifier"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// FileMeta расширяет файл контекстом (описанием от AI), чтобы не гонять картинки в LLM.
type FileMeta struct {
	classifier.ClassifiedFile
	VisionDescription string   // Результат анализа Vision-модели
	Tags              []string // Теги
}

// GlobalState — Single Source of Truth.
type GlobalState struct {
	Config       *config.AppConfig
	S3           *s3storage.Client
	Dictionaries *wb.Dictionaries

	mu sync.RWMutex // Защищает runtime-поля ниже

	History []llm.Message           // История сообщений (User/Assistant/System)
	Files   map[string][]*FileMeta  // Working Memory: файлы, разбитые по категориям
	
	CurrentArticleID string
	CurrentModel     string
	IsProcessing     bool
}

// NewState инициализирует базовое состояние.
func NewState(cfg *config.AppConfig, s3Client *s3storage.Client) *GlobalState {
	// ... инициализация мап и дефолтных значений ...
	return &GlobalState{}
}

// AppendMessage thread-safe добавление сообщения.
func (s *GlobalState) AppendMessage(msg llm.Message) {
	// lock -> append -> unlock
}

// GetHistory возвращает КОПИЮ истории (защита от race condition).
func (s *GlobalState) GetHistory() []llm.Message {
	// rlock -> make copy -> runlock -> return
	return nil
}

// UpdateFileAnalysis сохраняет текстовое описание (от Vision) для конкретного файла.
func (s *GlobalState) UpdateFileAnalysis(tag string, filename string, description string) {
	// Ищет файл в s.Files[tag] по имени и обновляет VisionDescription
}

// BuildAgentContext собирает "пакет" для отправки в LLM.
// Ключевая логика: System Prompt + Working Memory (описания файлов) + Chat History.
func (s *GlobalState) BuildAgentContext(systemPrompt string) []llm.Message {
	// 1. Проходит по s.Files, собирает все VisionDescription в одну строку visualContext.
	// 2. Создает SystemMessage = systemPrompt + visualContext.
	// 3. Добавляет s.History.
	// 4. Возвращает итоговый слайс.
	return nil
}

// ==========================================
// INTERNAL/UI/MODEL.GO
// Модель данных TUI (The Elm Architecture)
// ==========================================

package ui

import (
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/ilkoid/poncho-ai/internal/app"
)

type MainModel struct {
	viewport viewport.Model   // Область просмотра чата
	textarea textarea.Model   // Поле ввода
	appState *app.GlobalState // Ссылка на бизнес-логику
	ready    bool             // Флаг инициализации размеров
}

func InitialModel(state *app.GlobalState) MainModel {
	// Настройка компонентов bubbles (размеры, плейсхолдеры)
	return MainModel{}
}

func (m MainModel) Init() tea.Cmd {
	return textarea.Blink
}

// ==========================================
// INTERNAL/UI/UPDATE.GO
// Логика обработки событий (Controller)
// ==========================================

package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/ilkoid/poncho-ai/internal/app"
)

// CommandResultMsg — результат асинхронной работы (ответа от Tool или LLM).
type CommandResultMsg struct {
	Output string
	Err    error
}

func (m MainModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Стандартный бойлерплейт обновления textarea и viewport
	
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Пересчет размеров viewport под терминал

	case tea.KeyMsg:
		// Обработка Ctrl+C, Enter
		// Если Enter:
		// 1. Очистить ввод.
		// 2. Добавить сообщение юзера в лог.
		// 3. Вернуть команду tea.Cmd (вызов performCommand).

	case CommandResultMsg:
		// Прилетел ответ от воркера: вывести в лог (System/Error)
	}

	return m, nil
}

// performCommand — "Мозг", парсящий текстовые команды (симуляция агента).
// Возвращает tea.Cmd (функцию, которая выполнится в отдельной горутине).
func performCommand(input string, state *app.GlobalState) tea.Cmd {
	return func() tea.Msg {
		// Парсинг input на cmd и args
		
		// switch cmd {
		// case "load":
		//    1. S3 ListFiles
		//    2. Classifier.Process (раскидать файлы по папкам)
		//    3. state.Files = result (обновить стейт)
		//    4. Вернуть отчет текстом
		
		// case "render":
		//    1. prompt.Load(file)
		//    2. Подготовить данные (взять CurrentArticleID, ссылки на картинки из state)
		//    3. p.RenderMessages(data)
		//    4. Вернуть превью промпта
		
		// default:
		//    Вернуть ошибку "unknown command"
		// }
		return nil
	}
}

// ==========================================
// PKG/CLASSIFIER/ENGINE.GO
// Логика сортировки файлов
// ==========================================

package classifier

import "github.com/ilkoid/poncho-ai/pkg/config"

type ClassifiedFile struct {
	Tag         string // "sketch", "plm"
	OriginalKey string
	Filename    string
}

type Engine struct {
	rules []config.FileRule
}

func New(rules []config.FileRule) *Engine {
	return &Engine{rules: rules}
}

// Process раскидывает сырой список S3-объектов по категориям согласно конфигу.
func (e *Engine) Process(objects []interface{}) (map[string][]ClassifiedFile, error) {
	// Цикл по объектам:
	//   Цикл по правилам (Rules):
	//     Если pattern совпал с filename -> добавить в map[Tag]
	// Если ни одно правило не совпало -> добавить в "other"
	return nil, nil
}

// ==========================================
// PKG/CONFIG/CONFIG.GO
// Конфигурация (структура YAML)
// ==========================================

package config

import "time"

// AppConfig — зеркало config.yaml
type AppConfig struct {
	Models          ModelsConfig          `yaml:"models"`
	Tools           map[string]ToolConfig `yaml:"tools"`
	S3              S3Config              `yaml:"s3"`
	ImageProcessing ImageProcConfig       `yaml:"image_processing"`
	App             AppSpecific           `yaml:"app"`
	FileRules       []FileRule            `yaml:"file_rules"` // Ключевая часть для классификатора
	WB              WBConfig              `yaml:"wb"`
}

type FileRule struct {
	Tag      string   `yaml:"tag"`      // Куда класть ("sketch")
	Patterns []string `yaml:"patterns"` // Как искать ("*.jpg")
	Required bool     `yaml:"required"`
}

type ModelDef struct {
	Provider  string `yaml:"provider"`
	ModelName string `yaml:"model_name"`
	APIKey    string `yaml:"api_key"`
	BaseURL   string `yaml:"base_url"`
}

// Load читает YAML, подставляет ENV переменные (${VAR}) и валидирует.
func Load(path string) (*AppConfig, error) {
	return nil, nil
}

// ==========================================
// PKG/TOOLS/STD/...
// Реализация инструментов (примеры)
// ==========================================

package std

import (
	"context"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// --- Tool: read_s3_image ---

type S3ReadImageTool struct {
	// Зависимости (Клиент S3, конфиг ресайза)
}

// Definition возвращает контракт для LLM (имя, описание, схема JSON).
func (t *S3ReadImageTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name: "read_s3_image_base64",
		// ... schema ...
	}
}

// Execute реализует логику: Скачать -> Ресайз -> Base64.
func (t *S3ReadImageTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// 1. Unmarshal argsJSON
	// 2. S3 Download
	// 3. Resize (если нужно)
	// 4. Return "data:image/jpeg;base64,..."
	return "", nil
}

// --- Tool: get_wb_parent_categories ---

type WbParentCategoriesTool struct {
	// Зависимости (WB Client)
}

func (t *WbParentCategoriesTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// 1. Вызов метода API клиента
	// 2. Marshal result to JSON string
	return "", nil
}
```



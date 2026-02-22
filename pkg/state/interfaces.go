// Package state определяет интерфейсы репозиториев для domain-specific операций.
//
// Каждый интерфейс следует Interface Segregation Principle (ISP):
// - Маленькие, сфокусированные интерфейсы (1-7 методов)
// - Каждый интерфейс решает одну задачу
// - Клиенты зависят только от нужных им интерфейсов
//
// Соблюдение правил из dev_solid.md:
//   - ISP: Интерфейсы 1-7 методов, каждый с одной ответственностью
//   - DIP: Бизнес-логика зависит от интерфейсов, не реализаций
package state

import (
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/todo"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// MessageRepository — интерфейс для работы с историей сообщений.
//
// Отвечает за хранение и извлечение истории диалога между пользователем и агентом.
// Использует llm.Message как стандартный формат сообщений.
type MessageRepository interface {
	// Append добавляет сообщение в историю.
	Append(msg llm.Message) error

	// GetHistory возвращает копию всей истории диалога.
	GetHistory() []llm.Message
}

// FileRepository — интерфейс для работы с файлами.
//
// Отвечает за хранение файлов и результатов их анализа (vision descriptions).
// Поддерживает группировку файлов по тегам (sketch, plm_data, marketing).
type FileRepository interface {
	// SetFiles сохраняет файлы для текущей сессии.
	SetFiles(files map[string][]*s3storage.FileMeta) error

	// GetFiles возвращает копию текущих файлов.
	GetFiles() map[string][]*s3storage.FileMeta

	// UpdateFileAnalysis сохраняет результат работы Vision модели.
	UpdateFileAnalysis(tag, filename, description string) error

	// SetCurrentArticle устанавливает текущий артикул и файлы.
	SetCurrentArticle(articleID string, files map[string][]*s3storage.FileMeta) error

	// GetCurrentArticleID возвращает ID текущего артикула.
	GetCurrentArticleID() string

	// GetCurrentArticle возвращает ID и файлы текущего артикула.
	GetCurrentArticle() (articleID string, files map[string][]*s3storage.FileMeta)
}

// TodoRepository — интерфейс для работы с задачами.
//
// Отвечает за планирование и отслеживание многошаговых задач агента.
// Делегирует основную логику todo.Manager.
type TodoRepository interface {
	// AddTask добавляет новую задачу в план.
	AddTask(description string, metadata ...map[string]any) (int, error)

	// CompleteTask отмечает задачу как выполненную.
	CompleteTask(id int) error

	// FailTask отмечает задачу как проваленную.
	FailTask(id int, reason string) error

	// GetTodoString возвращает строковое представление плана.
	GetTodoString() string

	// GetTodoStats возвращает статистику по задачам.
	GetTodoStats() (pending, done, failed int)

	// GetTodoManager возвращает todo.Manager для использования в tools.
	// ВНИМАНИЕ: Для backward compatibility. Предпочтительно использовать методы интерфейса.
	GetTodoManager() *todo.Manager

	// SetTodoManager устанавливает todo manager.
	SetTodoManager(manager *todo.Manager) error
}

// DictionaryRepository — интерфейс для справочников маркетплейсов.
//
// Отвечает за хранение и извлечение e-commerce справочников
// (цвета, страны, полы, сезоны, НДС для Wildberries).
type DictionaryRepository interface {
	// SetDictionaries устанавливает справочники маркетплейсов.
	SetDictionaries(dicts *wb.Dictionaries) error

	// GetDictionaries возвращает справочники маркетплейсов.
	GetDictionaries() *wb.Dictionaries
}

// StorageRepository — интерфейс для S3 клиента.
//
// Отвечает за управление S3-совместимым клиентом для загрузки данных.
// Является опциональной зависимостью — не все приложения используют S3.
type StorageRepository interface {
	// SetStorage устанавливает S3 клиент.
	// Если client == nil — очищает S3 из состояния.
	SetStorage(client *s3storage.Client) error

	// GetStorage возвращает S3 клиент.
	// Возвращает nil если S3 не установлен.
	GetStorage() *s3storage.Client

	// HasStorage проверяет наличие S3 клиента.
	HasStorage() bool
}

// ToolsRepository — интерфейс для реестра инструментов.
//
// Отвечает за хранение и извлечение реестра зарегистрированных инструментов.
// Rule 3: Все инструменты регистрируются через Registry.Register().
type ToolsRepository interface {
	// SetToolsRegistry устанавливает реестр инструментов.
	SetToolsRegistry(registry *tools.Registry) error

	// GetToolsRegistry возвращает реестр инструментов.
	GetToolsRegistry() *tools.Registry
}

// ContextBuilder — интерфейс для построения контекста LLM.
//
// Отвечает за сборку полного контекста для генеративного запроса:
// системный промпт, результаты анализа файлов, контекст плана, история диалога.
type ContextBuilder interface {
	// BuildAgentContext собирает полный контекст для генеративного запроса.
	BuildAgentContext(systemPrompt string) []llm.Message
}

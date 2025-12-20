/* 
Обновленный файл.
Теперь это не просто конфиг-контейнер, а потокобезопасное хранилище (Store).
Ключевые изменения:
sync.RWMutex — защита от паники при одновременной записи агентом и чтении UI.
ClassifiedFile расширен полями VisionDescription — это и есть ваша "Рабочая память" для результатов анализа.
Метод BuildAgentContext — реализует логику "сжатия" знаний перед отправкой в LLM.
*/

package app

import (
	"fmt"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/classifier"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Пакет app хранит глобальное состояние приложения (GlobalState).
// Он выступает "Single Source of Truth" для UI, Агента и системных утилит.

// FileMeta расширяет базовую классификацию файла результатами анализа.
// Это позволяет хранить "знание" о картинке, не гоняя саму картинку в LLM каждый раз.
type FileMeta struct {
	classifier.ClassifiedFile

	// VisionDescription хранит текстовое описание, полученное от Vision-модели.
	// Пример: "На эскизе изображено платье миди с V-образным вырезом..."
	VisionDescription string

	// Tags — теги, извлеченные или сгенерированные в процессе.
	Tags []string
}

// GlobalState хранит данные сессии, конфигурацию и историю.
// Доступ к полям, которые меняются в runtime (History, Files, IsProcessing),
// должен идти через методы с мьютексом.
type GlobalState struct {
	Config       *config.AppConfig
	S3           *s3storage.Client
	Dictionaries *wb.Dictionaries

	// mu защищает доступ к History, Files и IsProcessing
	mu sync.RWMutex

	// History — хронология общения (User <-> Agent).
	// Сюда НЕ попадают тяжелые base64, только текст и tool calls.
	History []llm.Message

	// Files — "Рабочая память" (Working Memory).
	// Хранит файлы текущего артикула и результаты их анализа.
	// Ключ: тег (например, "sketch", "plm_data").
	Files map[string][]*FileMeta

	// Данные текущей сессии
	CurrentArticleID string
	CurrentModel     string
	IsProcessing     bool
}

// NewState создает начальное состояние.
func NewState(cfg *config.AppConfig, s3Client *s3storage.Client) *GlobalState {
	return &GlobalState{
		Config:           cfg,
		S3:               s3Client,
		CurrentArticleID: "NONE",
		CurrentModel:     cfg.Models.DefaultVision,
		IsProcessing:     false,
		Files:            make(map[string][]*FileMeta),
		History:          make([]llm.Message, 0),
	}
}

// --- Thread-Safe Methods (Методы для работы с данными) ---

// AppendMessage безопасно добавляет сообщение в историю.
func (s *GlobalState) AppendMessage(msg llm.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.History = append(s.History, msg)
}

// GetHistory возвращает копию истории для рендера в UI или отправки в LLM.
// Возвращаем копию, чтобы избежать race condition при чтении слайса.
func (s *GlobalState) GetHistory() []llm.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dst := make([]llm.Message, len(s.History))
	copy(dst, s.History)
	return dst
}

// UpdateFileAnalysis сохраняет результат работы Vision модели в "память" файла.
// path — путь к файлу (ключ поиска), description — результат анализа.
func (s *GlobalState) UpdateFileAnalysis(tag string, filename string, description string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	files, ok := s.Files[tag]
	if !ok {
		return
	}

	for _, f := range files {
		if f.Filename == filename { // Предполагаем, что Filename есть в ClassifiedFile
			f.VisionDescription = description
			return
		}
	}
}

// SetProcessing меняет статус занятости (для спиннера в UI).
func (s *GlobalState) SetProcessing(busy bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.IsProcessing = busy
}

// BuildAgentContext собирает полный контекст для генеративного запроса (ReAct).
// Он объединяет:
// 1. Системный промпт.
// 2. "Рабочую память" (результаты анализа файлов).
// 3. Историю диалога.
func (s *GlobalState) BuildAgentContext(systemPrompt string) []llm.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 1. Формируем блок знаний из проанализированных файлов
	var visualContext string
	for tag, files := range s.Files {
		for _, f := range files {
			if f.VisionDescription != "" {
				visualContext += fmt.Sprintf("- Файл [%s] %s: %s\n", tag, f.Filename, f.VisionDescription)
			}
		}
	}

	knowledgeMsg := ""
	if visualContext != "" {
		knowledgeMsg = fmt.Sprintf("\nКОНТЕКСТ АРТИКУЛА (Результаты анализа файлов):\n%s", visualContext)
	}

	// 2. Собираем итоговый массив сообщений
	messages := make([]llm.Message, 0, len(s.History)+2)

	// Системное сообщение с инъекцией знаний
	messages = append(messages, llm.Message{
		Role:    llm.RoleSystem,
		Content: systemPrompt + knowledgeMsg,
	})

	// Добавляем историю переписки
	messages = append(messages, s.History...)

	return messages
}

/* 
Как это использовать (Пример логики)
Теперь в коде вашего агента (или в команде analyze) вы делаете так:

Vision этап (отдельно):

Берете файл, отправляете в LLM (без истории).

Получаете текст.

Вызываете state.UpdateFileAnalysis("sketch", "img1.jpg", "Платье красное...").

Генерация карточки (ReAct):

Вызываете state.BuildAgentContext("Ты менеджер WB...").

Этот метод сам склеит ("Платье красное...") в системный промпт.

Отправляете результат в LLM.

LLM "видит" описание картинки, но не тратит токены на vision.
*/

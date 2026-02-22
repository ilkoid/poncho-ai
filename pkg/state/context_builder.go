// Package state предоставляет реализацию ContextBuilder.
//
// ContextBuilder собирает полный контекст для генеративного запроса LLM,
// объединяя системный промпт, результаты анализа файлов, контекст плана и историю.
package state

import (
	"fmt"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/todo"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// ContextBuilderImpl реализует ContextBuilder.
//
// Использует Store для доступа ко всем данным:
// - История сообщений (KeyHistory)
// - Файлы с результатами анализа (KeyFiles)
// - План задач (KeyTodo)
type ContextBuilderImpl struct {
	store *Store
}

// NewContextBuilder создаёт новый построитель контекста.
func NewContextBuilder(store *Store) *ContextBuilderImpl {
	return &ContextBuilderImpl{store: store}
}

// BuildAgentContext собирает полный контекст для генеративного запроса (ReAct).
//
// Объединяет:
// 1. Системный промпт
// 2. "Рабочую память" (результаты анализа файлов из VisionDescription)
// 3. Контекст плана (Todo Manager)
// 4. Историю диалога
//
// Возвращаемый массив сообщений готов для передачи в LLM.
//
// "Working Memory" паттерн: Результаты анализа vision моделей хранятся в
// FileMeta.VisionDescription и инжектируются в контекст без повторной
// отправки изображений (экономия токенов).
//
// Thread-safe: читает Files и History под защитой мьютекса Store.
//
// Rule 5: Thread-safe доступ к данным.
// Rule 7: Возвращает корректный массив даже при пустых данных.
func (b *ContextBuilderImpl) BuildAgentContext(systemPrompt string) []llm.Message {
	// 1. Формируем блок знаний из проанализированных файлов
	visualContext := b.buildVisualContext()

	knowledgeMsg := ""
	if visualContext != "" {
		knowledgeMsg = fmt.Sprintf("\nКОНТЕКСТ АРТИКУЛА (Результаты анализа файлов):\n%s", visualContext)
	}

	// 2. Формируем контекст плана
	todoContext := b.buildTodoContext()

	// 3. Получаем историю
	history := b.getHistory()

	// 4. Собираем итоговый массив сообщений
	messages := make([]llm.Message, 0, len(history)+3)

	// Системное сообщение с инъекцией знаний
	messages = append(messages, llm.Message{
		Role:    llm.RoleSystem,
		Content: systemPrompt + knowledgeMsg,
	})

	// Добавляем контекст плана
	if todoContext != "" && !strings.Contains(todoContext, "Нет активных задач") {
		messages = append(messages, llm.Message{
			Role:    llm.RoleSystem,
			Content: todoContext,
		})
	}

	// Добавляем историю переписки
	messages = append(messages, history...)

	return messages
}

// buildVisualContext формирует контекст из проанализированных файлов.
//
// Thread-safe: использует Store.Get.
func (b *ContextBuilderImpl) buildVisualContext() string {
	files, ok := b.store.Get(KeyFiles)
	if !ok {
		return ""
	}

	filesMap, ok := files.(map[string][]*s3storage.FileMeta)
	if !ok {
		utils.Error("BuildAgentContext: invalid files type", "key", KeyFiles)
		return ""
	}

	var visualContext string
	for tag, filesList := range filesMap {
		for _, f := range filesList {
			if f.VisionDescription != "" {
				visualContext += fmt.Sprintf("- Файл [%s] %s: %s\n", tag, f.Filename, f.VisionDescription)
			}
		}
	}
	return visualContext
}

// buildTodoContext формирует контекст из плана задач.
//
// Thread-safe: использует Store.Get.
func (b *ContextBuilderImpl) buildTodoContext() string {
	todoVal, ok := b.store.Get(KeyTodo)
	if !ok {
		return ""
	}

	manager, ok := todoVal.(*todo.Manager)
	if !ok {
		utils.Error("BuildAgentContext: invalid todo type", "key", KeyTodo)
		return ""
	}

	return manager.String()
}

// getHistory возвращает историю диалога.
//
// Thread-safe: использует Store.Get.
func (b *ContextBuilderImpl) getHistory() []llm.Message {
	historyVal, ok := b.store.Get(KeyHistory)
	if !ok {
		return []llm.Message{}
	}

	history, ok := historyVal.([]llm.Message)
	if !ok {
		utils.Error("BuildAgentContext: invalid history type", "key", KeyHistory)
		return []llm.Message{}
	}

	return history
}

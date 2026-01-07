// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"

	"github.com/ilkoid/poncho-ai/pkg/llm"
)

// Agent представляет интерфейс AI-агента для обработки запросов пользователя.
//
// Этот интерфейс находится в пакете chain, чтобы избежать циклических импортов:
//   - pkg/agent → pkg/app → pkg/chain → (закольцовывалось на pkg/agent)
//   - Теперь: pkg/agent → pkg/app → pkg/chain (без цикла)
//
// ReActCycle реализует этот интерфейс, что позволяет использовать его
// как Agent для простых сценариев (Run(query)) и как Chain для сложных
// (Execute(input) с полным контролем).
type Agent interface {
	// Run выполняет обработку запроса пользователя.
	//
	// Принимает:
	//   - ctx: контекст для отмены операции
	//   - query: текстовый запрос пользователя
	//
	// Возвращает:
	//   - string: финальный ответ агента
	//   - error: ошибка если не удалось выполнить запрос
	Run(ctx context.Context, query string) (string, error)

	// GetHistory возвращает копию истории диалога агента.
	//
	// Включает все сообщения: пользовательские, ассистента и результаты tools.
	GetHistory() []llm.Message
}

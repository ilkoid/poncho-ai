// Интерфейс Провайдера через который работает всё приложение.

package llm

import "context"

// Provider — контракт для любого AI-сервиса
type Provider interface {
	// Chat отправляет запрос и возвращает текстовый ответ (или JSON строку)
	Chat(ctx context.Context, req ChatRequest) (string, error)
}

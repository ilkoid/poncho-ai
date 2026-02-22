// Package state предоставляет реализацию MessageRepository.
//
// MessageRepo инкапсулирует работу с историей сообщений,
// используя Store как thread-safe хранилище.
package state

import (
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// MessageRepo реализует MessageRepository.
//
// Использует Store для thread-safe хранения истории сообщений.
// Все методы делегируют операции в Store с соответствующими преобразованиями типов.
type MessageRepo struct {
	store *Store
}

// NewMessageRepo создаёт новый репозиторий сообщений.
func NewMessageRepo(store *Store) *MessageRepo {
	return &MessageRepo{store: store}
}

// Append добавляет сообщение в историю.
//
// Thread-safe: делегирует в Store.Update.
func (r *MessageRepo) Append(msg llm.Message) error {
	return r.store.Update(KeyHistory, func(val any) any {
		if val == nil {
			return []llm.Message{msg}
		}
		history, ok := val.([]llm.Message)
		if !ok {
			utils.Error("Append: type assertion failed", "key", KeyHistory)
			return []llm.Message{msg}
		}
		return append(history, msg)
	})
}

// GetHistory возвращает копию всей истории диалога.
//
// Возвращает копию слайса для избежания race condition при изменении.
// Thread-safe: делегирует в Store.Get.
func (r *MessageRepo) GetHistory() []llm.Message {
	val, ok := r.store.Get(KeyHistory)
	if !ok {
		return []llm.Message{}
	}

	history, ok := val.([]llm.Message)
	if !ok {
		utils.Error("GetHistory: type assertion failed", "key", KeyHistory)
		return []llm.Message{}
	}

	// Возвращаем копию для thread-safety
	dst := make([]llm.Message, len(history))
	copy(dst, history)
	return dst
}

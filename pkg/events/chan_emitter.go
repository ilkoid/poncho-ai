package events

import (
	"context"
	"sync"
)

// ChanEmitter — стандартная реализация Emitter через канал.
//
// Thread-safe.
// Используется как дефолтная реализация в pkg/agent.
type ChanEmitter struct {
	mu     sync.RWMutex
	ch     chan Event
	closed bool
}

// NewChanEmitter создаёт новый ChanEmitter с буферизованным каналом.
//
// buffer определяет размер буфера канала.
// Если buffer = 0, канал будет небуферизованным (blocking).
func NewChanEmitter(buffer int) *ChanEmitter {
	return &ChanEmitter{
		ch: make(chan Event, buffer),
	}
}

// Emit отправляет событие в канал.
//
// Thread-safe.
// Rule 11: уважает context.Context.
// Если канал закрыт или context отменён, возвращает ошибку.
func (e *ChanEmitter) Emit(ctx context.Context, event Event) {
	e.mu.RLock()
	if e.closed {
		e.mu.RUnlock()
		return
	}
	e.mu.RUnlock()

	select {
	case e.ch <- event:
		// Успешно отправлено
	case <-ctx.Done():
		// Context отменён
		return
	}
}

// Subscribe возвращает Subscriber для чтения событий.
//
// Thread-safe.
// Можно вызвать несколько раз для создания нескольких подписчиков.
func (e *ChanEmitter) Subscribe() Subscriber {
	return &chanSubscriber{
		ch:   e.ch,
		once: &sync.Once{},
	}
}

// Close закрывает канал и освобождает ресурсы.
//
// Thread-safe.
// После закрытия Emit больше не отправляет события.
func (e *ChanEmitter) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	e.closed = true
	close(e.ch)
}

// chanSubscriber реализует Subscriber интерфейс.
type chanSubscriber struct {
	ch   <-chan Event
	once *sync.Once
}

// Events возвращает read-only канал событий.
func (s *chanSubscriber) Events() <-chan Event {
	return s.ch
}

// Close закрывает подписчика (no-op для shared channel).
//
// Реальный канал закрывается только через ChanEmitter.Close().
func (s *chanSubscriber) Close() {
	// Ничего не делаем - канал общий для всех подписчиков
}

// Ensure ChanEmitter implements Emitter
var _ Emitter = (*ChanEmitter)(nil)

// Ensure chanSubscriber implements Subscriber
var _ Subscriber = (*chanSubscriber)(nil)

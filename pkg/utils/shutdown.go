// Package utils предоставляет вспомогательные функции для graceful shutdown.
//
// Graceful Shutdown — корректное завершение приложения при получении сигнала:
//   - SIGINT (Ctrl+C)
//   - SIGTERM (kill)
//
// Использование:
//   ctx, cancel := context.WithCancel(context.Background())
//   defer utils.SetupGracefulShutdown(cancel)()
//
// Функция гарантирует что:
//   - Контекст будет отменён при получении сигнала
//   - Логи будут сохранены (defer utils.Close())
//   - Ресурсы будут освобождены
package utils

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// SetupGracefulShutdown устанавливает обработчик сигналов для graceful shutdown.
//
// Возвращает функцию которую следует вызвать через defer для освобождения ресурсов.
//
// Правильное использование:
//   ctx, cancel := context.WithCancel(context.Background())
//   defer SetupGracefulShutdown(cancel)()
//
// Или для более детального контроля:
//   ctx, cancel := context.WithCancel(context.Background())
//   shutdown := SetupGracefulShutdown(cancel)
//   // ... код приложения ...
//   shutdown() // Явный вызов в конце
//
// Sigterm Handler:
// При получении SIGINT (Ctrl+C) или SIGTERM:
//   1. Логируется сообщение "Received signal, shutting down gracefully..."
//   2. Вызывается cancel() для отмены контекста
//   3. Все операции должны проверить ctx.Err() и завершиться
//   4. При выходе из main() сработает defer shutdown() → Close()
//
// Rule 11: Уважает context.Context для распространения отмены.
func SetupGracefulShutdown(cancel context.CancelFunc) func() {
	// Канал для OS сигналов
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Запускаем goroutine для обработки сигналов
	go func() {
		sig := <-sigChan
		Info("Received signal, shutting down gracefully", "signal", sig.String())
		cancel()
	}()

	// Возвращаем функцию очистки
	return func() {
		// Закрываем логи (это всегда безопасно вызвать)
		Close()
	}
}

// SetupGracefulShutdownWithContext создаёт контекст и настраивает graceful shutdown.
//
// Удобная обёртка для типичного случая использования:
//   ctx, shutdown := SetupGracefulShutdownWithContext()
//   defer shutdown()
//
// Rule 11: Уважает context.Context для распространения отмены.
func SetupGracefulShutdownWithContext() (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	shutdown := SetupGracefulShutdown(cancel)
	return ctx, shutdown
}

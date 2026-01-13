// Simple TUI - минимальный пример использования Poncho AI с готовым TUI
//
// Запуск:
//   go run main.go
//
// Этот пример показывает как использовать Poncho AI с готовым TUI интерфейсом
// без написания собственного Bubble Tea кода.
package main

import (
	"context"
	"log"

	"github.com/ilkoid/poncho-ai/pkg/agent"
	"github.com/ilkoid/poncho-ai/pkg/tui"
)

func main() {
	// 1. Rule 11: Создаём родительский контекст для инициализации
	ctx := context.Background()

	// 2. Создаём AI агент (загружает config.yaml автоматически)
	//
	// ConfigPath можно опустить - агент найдёт config.yaml автоматически:
	//   - Текущая директория
	//   - Директория исполняемого файла
	//   - Родительская директория
	client, err := agent.New(ctx, agent.Config{
		ConfigPath: "../../config.yaml", // Путь к config.yaml
	})
	if err != nil {
		log.Fatalf("Failed to create agent: %v", err)
	}

	// 3. Запускаем готовый TUI (одна строка!)
	//
	// TUI предоставляет:
	//   - Чат-подобный интерфейс
	//   - История сообщений
	//   - Отображение "Thinking..." во время работы агента
	//   - Обработку ошибок
	//   - Ctrl+C для выхода
	//
	// Rule 11: передаём контекст для распространения отмены
	if err := tui.Run(ctx, client); err != nil {
		log.Fatalf("TUI error: %v", err)
	}
}

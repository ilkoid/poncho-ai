package main

import (
	"fmt"
	"log"
	"os"

	"github.com/ilkoid/poncho-ai/internal/app"
	"github.com/ilkoid/poncho-ai/internal/ui" // Импортируй наш новый пакет
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/tools/std"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	// 1. Загрузка конфига
	cfg, err := config.Load("config.yaml")
	if err != nil {
		// Если конфиг не загрузился, паникуем сразу, TUI даже не нужен
		panic(err)
	}

	// 2. Инициализация S3 клиента
	s3Client, err := s3storage.New(cfg.S3)
	if err != nil {
		log.Fatalf("Critical error connecting to S3: %v", err)
	}
	fmt.Printf(" [OK] S3 Connected: %s (Bucket: %s)\n", cfg.S3.Endpoint, cfg.S3.Bucket)

	// 2. Инициализируем состояние
	state := app.NewState(cfg, s3Client)

	// 3. Создаем и регистрируем инструменты планировщика
	plannerTools := std.NewPlannerTools(state.Todo)
	for _, tool := range plannerTools {
		state.ToolsRegistry.Register(tool)
	}

	// 4. Настраиваем реестр команд и регистрируем Todo команды
	app.SetupTodoCommands(state.CommandRegistry, state)

	// 5. Создаем программу Bubble Tea
	p := tea.NewProgram(
		ui.InitialModel(state),
		tea.WithAltScreen(),       // Использовать альтернативный буфер (полный экран)
		tea.WithMouseCellMotion(), // Включить мышь (скролл)
	)

	// 3. Запускаем
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error running program: %v\n", err)
		os.Exit(1)
	}
}

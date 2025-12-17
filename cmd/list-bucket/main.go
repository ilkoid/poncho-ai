package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// --- Стили ---
var (
	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFDF5")).
			Background(lipgloss.Color("#25A065")). // Зеленый
			Padding(0, 1)

	itemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("205")) // Розовый
)

// --- Сообщения (Messages) ---
type errMsg error
type contentMsg []s3storage.StoredObject

// --- Модель ---
type model struct {
	s3Client *s3storage.Client
	spinner  spinner.Model
	viewport viewport.Model
	
	loading  bool
	err      error
	ready    bool
}

// Инициализация модели
func initialModel(s3 *s3storage.Client) model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	return model{
		s3Client: s3,
		spinner:  s,
		loading:  true, // Сразу начинаем загрузку
	}
}

// Init запускает спиннер и команду загрузки
func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		fetchBucketContents(m.s3Client),
	)
}

// Update - обработка событий
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	
	// Нажатие клавиш
	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

	// Ошибка
	case errMsg:
		m.err = msg
		m.loading = false
		return m, nil

	// Данные загружены
	case contentMsg:
		m.loading = false
		content := formatFileList(msg)
		m.viewport.SetContent(content)
		return m, nil

	// Ресайз окна
	case tea.WindowSizeMsg:
		headerHeight := 2
		verticalMarginHeight := 2

		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-headerHeight-verticalMarginHeight)
			m.viewport.YPosition = headerHeight
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - headerHeight - verticalMarginHeight
		}
	}

	// Обновляем компоненты
	if m.loading {
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
	} else {
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

// View - отрисовка
func (m model) View() string {
	if m.err != nil {
		return fmt.Sprintf("\n❌ Error: %v\n\nPress 'q' to quit.", m.err)
	}

	header := titleStyle.Render("📦 S3 Bucket Inspector")

	if m.loading {
		return fmt.Sprintf("\n %s Connecting to S3 and fetching objects...\n\n", m.spinner.View())
	}

	return fmt.Sprintf("%s\n%s\n\n(Press 'q' to quit, arrows to scroll)", header, m.viewport.View())
}

// --- Бизнес-логика (Commands) ---

func fetchBucketContents(client *s3storage.Client) tea.Cmd {
	return func() tea.Msg {
		// Таймаут 10 секунд
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Пустой префикс = корневая папка (все файлы)
		files, err := client.ListFiles(ctx, "")
		if err != nil {
			return errMsg(err)
		}
		return contentMsg(files)
	}
}

// Форматирование списка в строку для вьюпорта
func formatFileList(files []s3storage.StoredObject) string {
	if len(files) == 0 {
		return "Bucket is empty."
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Total Objects: %d\n\n", len(files)))

	for _, f := range files {
		size := fmt.Sprintf("%.2f KB", float64(f.Size)/1024)
		line := fmt.Sprintf("%s  %-10s  %s\n", 
			itemStyle.Render("•"), 
			size, 
			f.Key,
		)
		b.WriteString(line)
	}
	return b.String()
}

// --- Main ---

func main() {
	// 1. Грузим конфиг (используем наш готовый пакет)
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("Config Error: %v", err)
	}

	// 2. Инициализируем S3
	s3Client, err := s3storage.New(cfg.S3)
	if err != nil {
		log.Fatalf("S3 Init Error: %v", err)
	}

	// 3. Запускаем
	p := tea.NewProgram(
		initialModel(s3Client),
		tea.WithAltScreen(),
	)

	// // 3. Запускаем
	// p := tea.NewProgram(
	// 	initialModel(s3Client),
	// 	tea.WithAltScreen(),
	// 	tea.WithMouseCellMotion(),
	// )


	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
		os.Exit(1)
	}

	// В конце main()
	// if m.selectedFile != "" {
	// 	fmt.Println(m.selectedFile) // Останется в терминале, можно выделить мышкой
	// }

}


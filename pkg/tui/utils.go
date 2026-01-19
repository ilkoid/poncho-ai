// Package tui предоставляет reusable утилиты для TUI компонентов.
package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// debugLogFile - файл для debug логирования (без mutex для простоты)
var debugLogFile *os.File

// closeDebugLog закрывает debug лог (без mutex для простоты)
func closeDebugLog() {
	if debugLogFile != nil {
		fmt.Fprintf(debugLogFile, "[%s] === TUI Debug Log Ended ===\n", time.Now().Format("15:04:05.000"))
		debugLogFile.Close()
		debugLogFile = nil
	}
}

// clearLogs удаляет все лог-файлы в текущей директории и поддиректориях.
//
// Удаляет следующие типы логов:
//   - poncho_log_*.md (Markdown логи от DebugManager, дампы экрана)
//   - poncho-*.log (старые лог-файлы)
//   - debug_*.json (JSON логи от pkg/debug)
//   - tui_debug.log (TUI debug лог)
//   - callback_debug.log (Callback debug лог)
//
// Returns количество удалённых файлов и ошибку (если есть).
func clearLogs() (int, error) {
	deleted := 0
	patterns := []string{
		"poncho_log_*.md",
		"poncho-*.log",
		"debug_*.json",
		"tui_debug.log",
		"callback_debug.log",
	}

	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return deleted, fmt.Errorf("glob pattern %s failed: %w", pattern, err)
		}

		for _, match := range matches {
			// Удаляем файл
			if err := os.Remove(match); err == nil {
				deleted++
			}
			// Игнорируем ошибки удаления (файл может быть уже удалён)
		}
	}

	// Также проверяем поддиректорию debug_logs/
	debugLogsDir := "./debug_logs"
	if entries, err := os.ReadDir(debugLogsDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			// Проверяем паттерны debug_*.json
			if strings.HasPrefix(name, "debug_") && strings.HasSuffix(name, ".json") {
				fullPath := filepath.Join(debugLogsDir, name)
				if err := os.Remove(fullPath); err == nil {
					deleted++
				}
			}
		}
	}

	return deleted, nil
}

// stripANSICodes удаляет ANSI escape коды из строки.
func stripANSICodes(s string) string {
	// Простая реализация - убираем ESC последовательности
	// Более сложная версия может использовать регулярные выражения
	result := strings.Builder{}
	i := 0
	for i < len(s) {
		if s[i] == 0x1B { // ESC символ
			// Пропускаем до конца последовательности (до буквы/цифры)
			i++
			for i < len(s) && (s[i] < '@' || s[i] > '~') {
				i++
			}
			if i < len(s) {
				i++ // пропускаем последний символ последовательности
			}
		} else {
			result.WriteByte(s[i])
			i++
		}
	}
	return result.String()
}

// truncate укорачивает строку до указанной длины (по символам, не байтам).
// Корректно обрабатывает Unicode (включая русский текст).
func truncate(s string, maxLen int) string {
	// Конвертируем в руны для корректной работы с Unicode
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

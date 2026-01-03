// Package debug предоставляет инструменты для записи и анализа выполнения AI-агента.
package debug

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Recorder записывает трейс выполнения агента и сохраняет в JSON файл.
//
// Потокобезопасен — может использоваться из разных горутин.
type Recorder struct {
	mu sync.Mutex

	// config — конфигурация рекордера
	config RecorderConfig

	// log — накапливаемый трейс выполнения
	log DebugLog

	// currentIteration — текущая итерация (заполняется по мере выполнения)
	currentIteration *Iteration

	// visitedTools — множество уникальных инструментов
	visitedTools map[string]struct{}

	// errors — список ошибок выполнения
	errors []string
}

// RecorderConfig конфигурация для создания Recorder.
type RecorderConfig struct {
	// LogsDir — директория для сохранения логов
	LogsDir string

	// IncludeToolArgs — включать аргументы инструментов в лог
	IncludeToolArgs bool

	// IncludeToolResults — включать результаты инструментов в лог
	IncludeToolResults bool

	// MaxResultSize — максимальный размер результата (превышение обрезается)
	// 0 означает без ограничений
	MaxResultSize int
}

// NewRecorder создает новый Recorder с заданной конфигурацией.
//
// Если LogsDir не существует, пытается создать её.
func NewRecorder(cfg RecorderConfig) (*Recorder, error) {
	// Создаём директорию для логов если не существует
	if cfg.LogsDir != "" {
		if err := os.MkdirAll(cfg.LogsDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create logs directory: %w", err)
		}
	}

	// Генерируем RunID на основе времени
	runID := fmt.Sprintf("debug_%s", time.Now().Format("20060102_150405"))

	return &Recorder{
		config:      cfg,
		log: DebugLog{
			RunID:     runID,
			Timestamp: time.Now(),
		},
		visitedTools: make(map[string]struct{}),
		errors:       make([]string, 0),
	}, nil
}

// Start начинает запись новой сессии с пользовательским запросом.
func (r *Recorder) Start(userQuery string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.log.UserQuery = userQuery
	r.log.Timestamp = time.Now()
}

// StartIteration начинает запись новой итерации.
func (r *Recorder) StartIteration(num int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.currentIteration = &Iteration{
		Number:   num,
		Duration: 0,
	}
}

// RecordLLMRequest записывает информацию о запросе к LLM.
func (r *Recorder) RecordLLMRequest(req LLMRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.currentIteration != nil {
		r.currentIteration.LLMRequest = req
	}
}

// RecordLLMResponse записывает ответ от LLM.
func (r *Recorder) RecordLLMResponse(resp LLMResponse) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.currentIteration != nil {
		r.currentIteration.LLMResponse = resp

		// Записываем ошибку в общий список если есть
		if resp.Error != "" {
			r.errors = append(r.errors, fmt.Sprintf("LLM error: %s", resp.Error))
		}
	}
}

// RecordToolExecution записывает выполнение инструмента.
func (r *Recorder) RecordToolExecution(exec ToolExecution) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.currentIteration == nil {
		return
	}

	// Применяем конфигурацию включения/обрезки данных
	if !r.config.IncludeToolArgs {
		exec.Args = ""
	}
	if !r.config.IncludeToolResults {
		exec.Result = ""
	} else if r.config.MaxResultSize > 0 && len(exec.Result) > r.config.MaxResultSize {
		exec.Result = exec.Result[:r.config.MaxResultSize] + "... (truncated)"
		exec.ResultTruncated = true
	}

	// Добавляем в текущую итерацию
	r.currentIteration.ToolsExecuted = append(r.currentIteration.ToolsExecuted, exec)

	// Регистрируем уникальный инструмент
	r.visitedTools[exec.Name] = struct{}{}

	// Записываем ошибку если есть
	if !exec.Success && exec.Error != "" {
		r.errors = append(r.errors, fmt.Sprintf("Tool %s: %s", exec.Name, exec.Error))
	}
}

// EndIteration завершает текущую итерацию.
func (r *Recorder) EndIteration() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.currentIteration != nil {
		r.log.Iterations = append(r.log.Iterations, *r.currentIteration)
		r.currentIteration = nil
	}
}

// Finalize завершает запись и сохраняет лог в файл.
//
// Возвращает путь к сохраненному файлу или ошибку.
func (r *Recorder) Finalize(finalResult string, duration time.Duration) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Заполняем финальные данные
	r.log.FinalResult = finalResult
	r.log.Duration = duration.Milliseconds()

	// Формируем summary
	r.buildSummary(duration)

	// Сериализуем в JSON
	data, err := json.MarshalIndent(r.log, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal debug log: %w", err)
	}

	// Определяем путь к файлу
	filePath := r.getFilePath()

	// Сохраняем в файл
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write debug log: %w", err)
	}

	return filePath, nil
}

// buildSummary формирует агрегированную статистику.
func (r *Recorder) buildSummary(totalDuration time.Duration) {
	summary := Summary{
		Errors:      r.errors,
		VisitedTools: make([]string, 0, len(r.visitedTools)),
	}

	// Собираем уникальные инструменты
	for tool := range r.visitedTools {
		summary.VisitedTools = append(summary.VisitedTools, tool)
	}

	// Анализируем итерации
	for _, iter := range r.log.Iterations {
		summary.TotalLLMCalls++

		// LLM duration
		summary.TotalLLMDuration += iter.LLMResponse.Duration

		// Tool statistics
		for _, tool := range iter.ToolsExecuted {
			summary.TotalToolsExecuted++
			summary.TotalToolDuration += tool.Duration
		}
	}

	r.log.Summary = summary
}

// getFilePath возвращает путь к файлу для сохранения.
func (r *Recorder) getFilePath() string {
	if r.config.LogsDir != "" {
		return filepath.Join(r.config.LogsDir, r.log.RunID+".json")
	}
	return r.log.RunID + ".json"
}

// Helper функция для обрезки строки с сохранением суффикса.
func truncateString(s string, maxSize int) string {
	if maxSize <= 0 || len(s) <= maxSize {
		return s
	}

	// Обрезаем и добавляем индикатор обрезки
	if len(s) > maxSize {
		return s[:maxSize] + "... (truncated)"
	}
	return s
}

// Helper функция для очистки строки от лишних пробелов и переносов.
func cleanString(s string) string {
	// Удаляем лишние пробелы и переносы
	cleaned := strings.TrimSpace(s)
	// Заменяем множественные пробелы на одиночные
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	return cleaned
}

// GetRunID возвращает идентификатор текущей сессии.
func (r *Recorder) GetRunID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.log.RunID
}

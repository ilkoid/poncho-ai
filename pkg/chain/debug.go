// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/debug"
	"github.com/ilkoid/poncho-ai/pkg/llm"
)

// ChainDebugRecorder — обёртка над debug.Recorder для Chain Pattern.
//
// Предоставляет удобные методы для записи LLM вызовов и tool execution.
type ChainDebugRecorder struct {
	recorder *debug.Recorder
	enabled  bool
	logsDir  string
}

// NewChainDebugRecorder создаёт новый ChainDebugRecorder.
//
// Rule 10: Godoc на public API.
func NewChainDebugRecorder(cfg DebugConfig) (*ChainDebugRecorder, error) {
	if !cfg.Enabled {
		return &ChainDebugRecorder{enabled: false}, nil
	}

	recorderCfg := debug.RecorderConfig{
		LogsDir:            cfg.LogsDir,
		IncludeToolArgs:    cfg.IncludeToolArgs,
		IncludeToolResults: cfg.IncludeToolResults,
		MaxResultSize:      cfg.MaxResultSize,
	}

	recorder, err := debug.NewRecorder(recorderCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create debug recorder: %w", err)
	}

	return &ChainDebugRecorder{
		recorder: recorder,
		enabled:  true,
		logsDir:  cfg.LogsDir,
	}, nil
}

// Enabled возвращает true если debug логирование включено.
func (r *ChainDebugRecorder) Enabled() bool {
	return r.enabled
}

// Start начинает запись новой цепочки.
//
// Должен вызываться в начале Chain.Execute().
func (r *ChainDebugRecorder) Start(input ChainInput) {
	if !r.enabled {
		return
	}
	r.recorder.Start(input.UserQuery)
}

// Finalize завершает запись цепочки.
//
// Должен вызываться в конце Chain.Execute() перед возвратом результата.
// Возвращает путь к сохраненному файлу лога.
func (r *ChainDebugRecorder) Finalize(result string, duration time.Duration) (string, error) {
	if !r.enabled {
		return "", nil
	}
	return r.recorder.Finalize(result, duration)
}

// StartIteration начинает запись новой итерации ReAct цикла.
//
// Принимает номер итерации (начиная с 1).
func (r *ChainDebugRecorder) StartIteration(iterationNum int) {
	if !r.enabled {
		return
	}
	r.recorder.StartIteration(iterationNum)
}

// EndIteration завершает запись текущей итерации.
//
// Должен вызываться после StartIteration и всех Record* вызовов.
func (r *ChainDebugRecorder) EndIteration() {
	if !r.enabled {
		return
	}
	r.recorder.EndIteration()
}

// RecordLLMRequest записывает LLM запрос.
func (r *ChainDebugRecorder) RecordLLMRequest(model string, temperature float64, maxTokens int, systemPromptUsed string, messagesCount int) {
	if !r.enabled {
		return
	}
	r.recorder.RecordLLMRequest(debug.LLMRequest{
		Model:            model,
		Temperature:      temperature,
		MaxTokens:        maxTokens,
		SystemPromptUsed: systemPromptUsed,
		MessagesCount:    messagesCount,
	})
}

// RecordLLMResponse записывает LLM ответ.
func (r *ChainDebugRecorder) RecordLLMResponse(content string, toolCalls []llm.ToolCall, duration int64) {
	if !r.enabled {
		return
	}

	// Конвертируем tool calls в debug format
	debugToolCalls := make([]debug.ToolCallInfo, len(toolCalls))
	for i, tc := range toolCalls {
		debugToolCalls[i] = debug.ToolCallInfo{
			ID:   tc.ID,
			Name: tc.Name,
			Args: tc.Args,
		}
	}

	r.recorder.RecordLLMResponse(debug.LLMResponse{
		Content:    content,
		ToolCalls:  debugToolCalls,
		Duration:   duration,
	})
}

// RecordToolExecution записывает выполнение инструмента.
func (r *ChainDebugRecorder) RecordToolExecution(toolName, argsJSON, result string, duration int64, success bool) {
	if !r.enabled {
		return
	}

	r.recorder.RecordToolExecution(debug.ToolExecution{
		Name:     toolName,
		Args:     argsJSON,
		Result:   result,
		Duration: duration,
		Success:  success,
	})
}

// GetLogPath возвращает путь к файлу лога.
//
// Возвращает пустую строку если debug отключён или log ещё не сохранён.
func (r *ChainDebugRecorder) GetLogPath() string {
	if !r.enabled {
		return ""
	}
	runID := r.recorder.GetRunID()
	if runID == "" {
		return ""
	}
	if r.logsDir != "" {
		return filepath.Join(r.logsDir, runID+".json")
	}
	return runID + ".json"
}

// GetRunID возвращает ID текущего запуска.
func (r *ChainDebugRecorder) GetRunID() string {
	if !r.enabled {
		return ""
	}
	return r.recorder.GetRunID()
}

// AttachToChain — helper для присоединения debug recorder к Chain.
//
// Используется в CLI утилитах и тестах.
//
// Rule 7: Возвращает ошибку если Chain не поддерживает debug.
func AttachToChain(c Chain, recorder *ChainDebugRecorder) error {
	// Проверяем что Chain поддерживает debug
	if dc, ok := c.(DebuggableChain); ok {
		dc.AttachDebug(recorder)
		return nil
	}
	return fmt.Errorf("chain does not support debug recording")
}

// DebuggableChain — интерфейс для Chain с поддержкой debug.
//
// ReActChain и другие реализации должны реализовывать этот интерфейс.
type DebuggableChain interface {
	Chain

	// AttachDebug присоединяет debug recorder к Chain.
	AttachDebug(recorder *ChainDebugRecorder)
}

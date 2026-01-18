// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
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
	recorder   *debug.Recorder
	enabled    bool
	logsDir    string
	startTime  time.Time // PHASE 4: для отслеживания длительности выполнения
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

// RecordLLMRequestFull записывает полный LLM запрос с историей сообщений и инструментами.
//
// Используется для отладки потери контекста в диалогах.
func (r *ChainDebugRecorder) RecordLLMRequestFull(
	model string,
	temperature float64,
	maxTokens int,
	format string,
	systemPromptUsed string,
	messages []llm.Message,
	toolDefs []debug.ToolDef,
	rawRequest []byte,
	thinking string,
	parallelToolCalls *bool,
) {
	if !r.enabled {
		return
	}

	// Конвертируем сообщения в debug format
	debugMessages := make([]debug.MessageEntry, len(messages))
	for i, msg := range messages {
		debugMessages[i] = debug.MessageEntry{
			Role:      string(msg.Role),
			Content:   msg.Content,
			ToolCallID: msg.ToolCallID,
			Images:    msg.Images,
		}
		if len(msg.ToolCalls) > 0 {
			debugMessages[i].ToolCalls = make([]debug.ToolCallInfo, len(msg.ToolCalls))
			for j, tc := range msg.ToolCalls {
				debugMessages[i].ToolCalls[j] = debug.ToolCallInfo{
					ID:   tc.ID,
					Name: tc.Name,
					Args: tc.Args,
				}
			}
		}
	}

	r.recorder.RecordLLMRequest(debug.LLMRequest{
		Model:             model,
		Temperature:       temperature,
		MaxTokens:         maxTokens,
		Format:            format,
		SystemPromptUsed:  systemPromptUsed,
		MessagesCount:     len(messages),
		Messages:          debugMessages,
		Tools:             toolDefs,
		RawRequest:        rawRequest,
		Thinking:          thinking,
		ParallelToolCalls: parallelToolCalls,
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

// PHASE 4 REFACTOR: ExecutionObserver implementation
//
// ChainDebugRecorder теперь реализует ExecutionObserver для отслеживания
// жизненного цикла выполнения ReAct цикла.

// OnStart вызывается в начале выполнения Execute().
func (r *ChainDebugRecorder) OnStart(ctx context.Context, exec *ReActExecution) {
	if !r.enabled {
		return
	}
	r.startTime = time.Now()
	r.Start(*exec.chainCtx.Input)
}

// OnIterationStart вызывается в начале каждой итерации.
func (r *ChainDebugRecorder) OnIterationStart(iteration int) {
	if !r.enabled {
		return
	}
	r.StartIteration(iteration)
}

// OnIterationEnd вызывается в конце каждой итерации.
func (r *ChainDebugRecorder) OnIterationEnd(iteration int) {
	if !r.enabled {
		return
	}
	r.EndIteration()
}

// OnFinish вызывается в конце выполнения Execute().
func (r *ChainDebugRecorder) OnFinish(result ChainOutput, err error) {
	if !r.enabled {
		return
	}
	resultStr := result.Result
	if err != nil {
		resultStr = ""
	}
	r.Finalize(resultStr, time.Since(r.startTime))
}

// Ensure ChainDebugRecorder implements ExecutionObserver
var _ ExecutionObserver = (*ChainDebugRecorder)(nil)

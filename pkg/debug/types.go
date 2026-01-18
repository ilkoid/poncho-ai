// Package debug предоставляет инструменты для записи и анализа выполнения AI-агента.
//
// Пакет сохраняет детальные трейсы выполнения в JSON формате для последующего
// анализа, отладки и оптимизации работы агента.
package debug

import (
	"encoding/json"
	"time"
)

// DebugLog представляет полный трейс выполнения одной операции агента.
//
// Сохраняется в JSON файл и содержит всю информацию о выполнении:
// LLM вызовы, выполнения инструментов, временные метрики, ошибки.
type DebugLog struct {
	// RunID — уникальный идентификатор запуска (используется в имени файла)
	RunID string `json:"run_id"`

	// Timestamp — время начала выполнения
	Timestamp time.Time `json:"timestamp"`

	// UserQuery — исходный запрос пользователя
	UserQuery string `json:"user_query"`

	// Duration — общая длительность выполнения в миллисекундах
	Duration int64 `json:"duration_ms"`

	// Iterations — список итераций ReAct цикла
	Iterations []Iteration `json:"iterations"`

	// Summary — агрегированная статистика выполнения
	Summary Summary `json:"summary"`

	// FinalResult — финальный ответ агента
	FinalResult string `json:"final_result,omitempty"`

	// Error — ошибка если выполнение завершилось неудачно
	Error string `json:"error,omitempty"`
}

// Iteration представляет одну итерацию ReAct цикла.
type Iteration struct {
	// Number — номер итерации (начиная с 1)
	Number int `json:"iteration"`

	// Duration — длительность итерации в миллисекундах
	Duration int64 `json:"duration_ms"`

	// LLMRequest — информация о запросе к LLM
	LLMRequest LLMRequest `json:"llm_request,omitempty"`

	// LLMResponse — ответ от LLM
	LLMResponse LLMResponse `json:"llm_response,omitempty"`

	// ToolsExecuted — список выполненных инструментов на этой итерации
	ToolsExecuted []ToolExecution `json:"tools_executed,omitempty"`

	// IsFinal — true если это финальная итерация (без tool calls)
	IsFinal bool `json:"is_final,omitempty"`
}

// LLMRequest содержит информацию о запросе к LLM.
type LLMRequest struct {
	// Model — использованная модель
	Model string `json:"model"`

	// Temperature — параметр температуры
	Temperature float64 `json:"temperature,omitempty"`

	// MaxTokens — максимальное количество токенов
	MaxTokens int `json:"max_tokens,omitempty"`

	// Format — формат ответа (например, "json_object")
	Format string `json:"format,omitempty"`

	// SystemPromptUsed — какой системный промпт был использован
	SystemPromptUsed string `json:"system_prompt_used,omitempty"`

	// MessagesCount — количество сообщений в запросе
	MessagesCount int `json:"messages_count"`

	// Messages — полная история сообщений (для отладки потери контекста)
	Messages []MessageEntry `json:"messages,omitempty"`

	// Tools — список доступных инструментов
	Tools []ToolDef `json:"tools,omitempty"`

	// RawRequest — сырой JSON запроса (полный API call)
	RawRequest json.RawMessage `json:"raw_request,omitempty"`

	// Thinking — параметр thinking для Zai GLM
	Thinking string `json:"thinking,omitempty"`

	// ParallelToolCalls — параметр parallel_tool_calls
	ParallelToolCalls *bool `json:"parallel_tool_calls,omitempty"`
}

// LLMResponse содержит ответ от LLM.
type LLMResponse struct {
	// Content — текстовый ответ
	Content string `json:"content,omitempty"`

	// ToolCalls — список вызовов инструментов
	ToolCalls []ToolCallInfo `json:"tool_calls,omitempty"`

	// Duration — длительность генерации в миллисекундах
	Duration int64 `json:"duration_ms"`

	// Error — ошибка если произошла
	Error string `json:"error,omitempty"`
}

// ToolCallInfo описывает вызов инструмента от LLM.
type ToolCallInfo struct {
	// ID — уникальный идентификатор вызова
	ID string `json:"id"`

	// Name — имя инструмента
	Name string `json:"name"`

	// Args — аргументы в JSON формате
	Args string `json:"args"`
}

// ToolExecution описывает выполнение одного инструмента.
type ToolExecution struct {
	// Name — имя инструмента
	Name string `json:"name"`

	// Args — аргументы (может быть обрезано по maxSize)
	Args string `json:"args,omitempty"`

	// Result — результат выполнения (может быть обрезан по maxSize)
	Result string `json:"result,omitempty"`

	// ResultTruncated — true если результат был обрезан
	ResultTruncated bool `json:"result_truncated,omitempty"`

	// Duration — длительность выполнения в миллисекундах
	Duration int64 `json:"duration_ms"`

	// Success — true если выполнение прошло успешно
	Success bool `json:"success"`

	// Error — описание ошибки если неуспешно
	Error string `json:"error,omitempty"`

	// PostPromptActivated — какой post-prompt был активирован
	PostPromptActivated string `json:"post_prompt_activated,omitempty"`
}

// Summary содержит агрегированную статистику выполнения.
type Summary struct {
	// TotalLLMCalls — общее количество вызовов LLM
	TotalLLMCalls int `json:"total_llm_calls"`

	// TotalToolsExecuted — общее количество выполненных инструментов
	TotalToolsExecuted int `json:"total_tools_executed"`

	// TotalLLMDuration — общее время всех LLM вызовов в миллисекундах
	TotalLLMDuration int64 `json:"total_llm_duration_ms"`

	// TotalToolDuration — общее время выполнения инструментов в миллисекундах
	TotalToolDuration int64 `json:"total_tool_duration_ms"`

	// Errors — список всех ошибок выполнения
	Errors []string `json:"errors,omitempty"`

	// VisitedTools — список уникальных инструментов, которые были вызваны
	VisitedTools []string `json:"visited_tools,omitempty"`
}

// MessageEntry представляет одно сообщение в истории对话 для полного логирования.
type MessageEntry struct {
	Role      string   `json:"role"`
	Content   string   `json:"content"`
	ToolCalls []ToolCallInfo `json:"tool_calls,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Images    []string `json:"images,omitempty"`
}

// ToolDef представляет определение инструмента для логирования.
type ToolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

# Streaming Output Support for Poncho AI - Implementation Plan

## Executive Summary

Add streaming output support to Poncho AI's LLM library with:
- **Provider-agnostic abstraction** (not Zai-specific)
- **Opt-out design** (streaming enabled by default)
- **Thinking-mode-only event granularity** (only reasoning_content triggers events)
- **Full stack support** (LLM → Chain → Agent → UI)
- **Zero breaking changes** (backward compatible)

## Requirements Confirmed

| Requirement | Decision |
|-------------|----------|
| Default mode | Opt-out (streaming enabled by default) |
| Interface design | Separate StreamingProvider interface |
| Scope | Full stack (all layers support streaming) |
| Provider support | Generic first (provider-agnostic) |
| Event granularity | Thinking only (reasoning_content events only) |

---

## Phase 1: Core Abstractions (`pkg/llm/`)

### 1.1 New Types: `pkg/llm/streaming.go`

Create new file defining streaming interfaces and types:

```go
package llm

import "context"

// StreamingProvider — интерфейс для LLM провайдеров с поддержкой стриминга.
// Отдельный интерфейс от Provider для обратной совместимости.
type StreamingProvider interface {
    Provider // Встраиваем базовый интерфейс

    // GenerateStream выполняет запрос к API с потоковой передачей ответа.
    GenerateStream(
        ctx context.Context,
        messages []Message,
        callback func(StreamChunk),
        opts ...any,
    ) (Message, error)
}

// StreamChunk представляет одну порцию данных из потокового ответа.
type StreamChunk struct {
    Type             ChunkType
    Content          string
    ReasoningContent string
    Delta            string
    Done             bool
    Error            error
}

// ChunkType определяет тип стримингового чанка.
type ChunkType string

const (
    ChunkThinking ChunkType = "thinking"
    ChunkContent  ChunkType = "content"
    ChunkError    ChunkType = "error"
    ChunkDone     ChunkType = "done"
)

// IsStreamingMode проверяет, включен ли стриминг в опциях.
func IsStreamingMode(opts ...any) bool {
    // Default: true (opt-out)
}
```

### 1.2 Stream Options: `pkg/llm/streaming_options.go`

```go
package llm

type StreamOptions struct {
    Enabled      bool // Default: true (opt-out)
    ThinkingOnly bool // Default: true
}

type StreamOption func(*StreamOptions)

func WithStream(enabled bool) StreamOption
func WithThinkingOnly(thinkingOnly bool) StreamOption
```

---

## Phase 2: OpenAI Client Streaming (`pkg/llm/openai/`)

### 2.1 Modify: `client.go`

**Changes to existing file:**

1. **Update `Generate()` method** - detect streaming mode and route appropriately

2. **Add `generateStandardStream()` method:**
   - Set `"stream": true` in request body
   - Parse SSE (Server-Sent Events) response
   - Call callback for each chunk
   - Accumulate final message

3. **Add `generateWithThinkingStream()` method:**
   - Set `"stream": true` + `"thinking": {"type": c.thinking}`
   - Parse SSE with `reasoning_content` field
   - **Key**: Send ONLY reasoning_content chunks (user requirement)
   - Accumulate final message

4. **Add SSE parser helper:**
   ```go
   func parseSSEChunk(line string) (map[string]interface{}, error)
   ```

---

## Phase 3: Event System (`pkg/events/`)

### 3.1 Modify: `events.go`

**Add to existing file:**

```go
const (
    // ... existing constants ...

    // EventThinkingChunk отправляется для каждой порции reasoning_content.
    EventThinkingChunk EventType = "thinking_chunk"
)

// ThinkingChunkData содержит данные для EventThinkingChunk.
type ThinkingChunkData struct {
    Chunk       string // Инкрементальные данные
    Accumulated string // Накопленные данные
}
```

---

## Phase 4: ReActCycle Integration (`pkg/chain/`)

### 4.1 Modify: `react.go`

**Add to `ReActCycle` struct:**
```go
streamingEnabled bool // Global flag from config
```

### 4.2 Modify: `llm_step.go`

**Update `LLMInvocationStep`:**

1. **Add emitter reference:**
```go
type LLMInvocationStep struct {
    // ... existing fields ...
    emitter events.Emitter
}
```

2. **Update `Execute()` method:**
```go
// Detect streaming capability
if streamingProvider, ok := provider.(llm.StreamingProvider); ok && c.streamingEnabled {
    response, err = s.invokeStreamingLLM(ctx, streamingProvider, messages, generateOpts)
} else {
    response, err = provider.Generate(ctx, messages, generateOpts...)
}
```

3. **Add `invokeStreamingLLM()` helper:**
```go
func (s *LLMInvocationStep) invokeStreamingLLM(
    ctx context.Context,
    provider llm.StreamingProvider,
    messages []llm.Message,
    opts []any,
) (llm.Message, error) {
    callback := func(chunk llm.StreamChunk) {
        switch chunk.Type {
        case llm.ChunkThinking:
            s.emitThinkingChunk(ctx, chunk)
        case llm.ChunkError:
            s.emitError(ctx, chunk.Error)
        }
    }
    return provider.GenerateStream(ctx, messages, callback, opts...)
}
```

---

## Phase 5: Agent API (`pkg/agent/`)

### 5.1 Modify: `client.go`

**No breaking changes** - existing `Run()` remains synchronous but internally uses streaming if available.

**Optional:** Add explicit `RunStream()` method:
```go
func (c *Client) RunStream(ctx context.Context, query string, enableStreaming bool) (string, error)
```

---

## Phase 6: UI Integration (`pkg/tui/`)

### 6.1 Modify: `model.go`

**Update `handleAgentEvent()` method:**

```go
case events.EventThinkingChunk:
    if chunkData, ok := event.Data.(events.ThinkingChunkData); ok {
        m.appendThinkingChunk(chunkData.Chunk)
    }
```

**Add helper methods:**
```go
func (m *Model) appendThinkingChunk(chunk string)
func thinkingStyle(str string) string
func thinkingContentStyle(str string) string
```

---

## Phase 7: Configuration (`config.yaml`)

### 7.1 Global Streaming Setting

```yaml
app:
  streaming:
    enabled: true        # Default: true (opt-out)
    thinking_only: true  # Send only reasoning_content events
```

### 7.2 Per-Model Override

```yaml
models:
  definitions:
    glm-4.6:
      streaming:
        enabled: true
```

---

## Phase 8: Testing

### 8.1 Unit Tests

File: `pkg/llm/openai/client_test.go`
- `TestGenerateStream_WithThinking`
- `TestGenerateStream_ContextCancellation`
- `TestGenerateStream_BackwardCompatibility`

### 8.2 Integration Test Utility

File: `cmd/streaming-test/main.go`
- CLI utility for testing streaming functionality
- Subscribes to events and displays chunks

---

## Critical Files to Modify

| File | Purpose |
|------|---------|
| `pkg/llm/streaming.go` | NEW: Core streaming interfaces |
| `pkg/llm/streaming_options.go` | NEW: Stream options |
| `pkg/llm/openai/client.go` | MODIFY: Add streaming methods |
| `pkg/events/events.go` | MODIFY: Add EventThinkingChunk |
| `pkg/chain/llm_step.go` | MODIFY: Integrate streaming |
| `pkg/chain/react.go` | MODIFY: Add streamingEnabled flag |
| `pkg/agent/client.go` | MODIFY: Read streaming config |
| `pkg/tui/model.go` | MODIFY: Display thinking chunks |
| `config.yaml` | MODIFY: Add streaming settings |
| `cmd/streaming-test/main.go` | NEW: Test utility |

---

## Implementation Order

1. **Week 1**: Core types (`streaming.go`, `streaming_options.go`)
2. **Week 2**: OpenAI client streaming methods
3. **Week 3**: Event system extensions
4. **Week 4**: ReActCycle integration
5. **Week 5**: Agent API and configuration
6. **Week 6**: UI integration
7. **Week 7**: Testing and documentation

---

## Dev Manifest Compliance

| Rule | Status |
|------|--------|
| Rule 0: Code Reuse | ✅ Uses existing event system |
| Rule 1: Tool Interface | ✅ No changes to Tool interface |
| Rule 2: Configuration | ✅ YAML with ENV support |
| Rule 3: Registry | ✅ No changes to tools registry |
| Rule 4: LLM Abstraction | ✅ Works through Provider interface |
| Rule 5: State | ✅ Thread-safe operations |
| Rule 6: Package Structure | ✅ pkg/ reusable, internal/ app-specific |
| Rule 7: Error Handling | ✅ No panic in business logic |
| Rule 8: Extensibility | ✅ New interface, no breaking changes |
| Rule 9: Testing | ✅ CLI utilities in cmd/ |
| Rule 10: Documentation | ✅ Godoc on public APIs |
| Rule 11: Context Propagation | ✅ All layers respect context.Context |
| Rule 12: Security | ✅ No hardcoded secrets |
| Rule 13: Resource Localization | ✅ Autonomous cmd/ apps |

---

## Verification

### Test Commands

```bash
# Test streaming with Zai GLM
cd cmd/streaming-test && go run main.go

# Test backward compatibility (sync mode)
# Set streaming.enabled: false in config.yaml
go run cmd/poncho/main.go

# Test context cancellation
# Send SIGINT during streaming query
```

### Success Criteria

- ✅ Streaming enabled by default (opt-out via config)
- ✅ Separate StreamingProvider interface
- ✅ Full stack support (LLM → Chain → Agent → UI)
- ✅ Generic abstraction (provider-agnostic)
- ✅ Thinking-only events (reasoning_content chunks only)
- ✅ Backward compatible (no breaking changes)
- ✅ Thread-safe (Rule 5)
- ✅ Context-aware (Rule 11)

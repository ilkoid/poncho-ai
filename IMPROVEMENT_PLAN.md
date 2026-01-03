# Poncho AI Improvement Plan
## Based on Eino Framework Analysis

> **Reference**: `docs/eino_comparison.md`
> **Goal**: Add high-value features while preserving Poncho AI philosophy ("Raw In, String Out", YAML-driven, simple)

---

## Priority Overview

| Feature | Complexity | Value | Priority |
|---------|-----------|-------|----------|
| JSON Debug Logs | Low | High | Phase 1 |
| BranchStrategy Pattern | Medium | High | Phase 1 |
| Callback/AOP System | Medium | High | Phase 2 |
| RequestContext (scoped state) | Medium | Medium | Phase 2 |
| Lambda Tools (inline) | Low | Medium | Phase 3 |
| HTTP Debug Server | Medium | High | Phase 3 |

---

## Phase 1: Quick Wins (1-2 weeks)

### 1.1 JSON Debug Logs

**Goal**: Save detailed execution traces for post-analysis

**Implementation**:

1. **Create `pkg/debug/` package**
   - `debug.go` - DebugLog struct with JSON serialization
   - `recorder.go` - Recorder that captures execution events

2. **Add configuration to `config.yaml`**:
   ```yaml
   app:
     debug:
       enabled: true
       save_logs: true
       logs_dir: "./debug_logs"
       include_tool_args: true
       include_tool_results: true
   ```

3. **Integrate into Orchestrator** (`internal/agent/orchestrator.go`):
   - Create `Recorder` at start of `Run()`
   - Capture each iteration: messages, tool calls, results, duration
   - Save to JSON file on completion: `debug_20241230_143022.json`

4. **Files to create**:
   - `pkg/debug/recorder.go`
   - `pkg/debug/types.go`

5. **Files to modify**:
   - `pkg/config/config.go` - Add debug config struct
   - `internal/agent/orchestrator.go` - Integrate recorder

---

### 1.2 BranchStrategy Pattern

**Goal**: Extract branching logic from Orchestrator for flexibility

**Implementation**:

1. **Create `pkg/agent/strategy.go`**:
   ```go
   type BranchStrategy interface {
       ShouldContinue(response llm.Message) bool
       NextStep(response llm.Message) string
   }

   type ReActStrategy struct{}
   type StreamingStrategy struct{}
   ```

2. **Modify Orchestrator** (`internal/agent/orchestrator.go`):
   - Add `strategy BranchStrategy` field
   - Replace hardcoded branching with `strategy.ShouldContinue()`
   - Extract tool execution logic into `executeToolsStep()`

3. **Files to create**:
   - `pkg/agent/strategy.go`

4. **Files to modify**:
   - `internal/agent/orchestrator.go:162-301` - Main loop refactoring

---

## Phase 2: Architecture Improvements (2-4 weeks)

### 2.1 Callback/AOP System

**Goal**: Enable cross-cutting concerns (logging, metrics, tracing) without modifying business logic

**Implementation**:

1. **Create `pkg/callbacks/` package**:
   ```go
   type CallbackHandler interface {
       OnAgentStart(ctx context.Context, query string) context.Context
       OnAgentEnd(ctx context.Context, result string, duration time.Duration)
       OnToolStart(ctx context.Context, tool string, args string) context.Context
       OnToolEnd(ctx context.Context, tool string, result string, duration time.Duration)
       OnToolError(ctx context.Context, tool string, err error)
       OnLLMStart(ctx context.Context, messages []llm.Message) context.Context
       OnLLMEnd(ctx context.Context, response llm.Message, duration time.Duration)
   }
   ```

2. **Add configuration to `config.yaml`**:
   ```yaml
   callbacks:
     enabled: true
     handlers:
       - type: "logging"
       - type: "metrics"
         config:
           export_prometheus: true
   ```

3. **Create built-in handlers** (`pkg/callbacks/handlers.go`):
   - `LoggingCallbackHandler` - Structured logging
   - `MetricsCallbackHandler` - Token usage, tool counts, durations

4. **Integrate into Orchestrator**:
   - Add `handlers []CallbackHandler` field
   - Call appropriate hooks at each event point
   - Pass context through handlers

5. **Files to create**:
   - `pkg/callbacks/handler.go`
   - `pkg/callbacks/handlers.go` - Built-in implementations
   - `pkg/callbacks/registry.go`

6. **Files to modify**:
   - `pkg/config/config.go` - Add callbacks config
   - `internal/agent/orchestrator.go` - Integrate handlers

---

### 2.2 RequestContext (Scoped State)

**Goal**: Isolate per-request state from GlobalState

**Implementation**:

1. **Create `pkg/agent/context.go`**:
   ```go
   type RequestContext struct {
       ID          string
       StartTime   time.Time
       TokenCount  int
       VisitedTools []string
       Cache      map[string]interface{}
       DebugLog   *debug.DebugLog
   }

   func (rc *RequestContext) RecordToolCall(tool string)
   func (rc *RequestContext) GetTokenCount() int
   ```

2. **Modify Orchestrator**:
   - Create `RequestContext` at start of `Run()`
   - Pass via `context.Context` using custom key
   - Store request-specific data instead of using GlobalState

3. **Keep GlobalState for**:
   - Configuration (read-only after init)
   - Shared clients (S3, WB)
   - Registries (Tools, Commands)
   - Conversation history (persisted across requests)

4. **Files to create**:
   - `pkg/agent/context.go`

5. **Files to modify**:
   - `internal/agent/orchestrator.go` - Use RequestContext
   - `internal/app/state.go` - Clarify what's global vs request-scoped

---

## Phase 3: Advanced Features (4-8 weeks)

### 3.1 Lambda Tools (Inline)

**Goal**: Enable quick prototyping without creating new files

**Implementation**:

1. **Create `pkg/tools/lambda.go`**:
   ```go
   type LambdaTool struct {
       name        string
       description string
       fn          func(ctx context.Context, argsJSON string) (string, error)
   }
   ```

2. **Add registration helper**:
   ```go
   registry.RegisterLambda("uppercase", func(ctx context.Context, args string) (string, error) {
       return strings.ToUpper(args), nil
   })
   ```

3. **Files to create**:
   - `pkg/tools/lambda.go`

---

### 3.2 HTTP Debug Server

**Goal**: Real-time debugging via HTTP interface

**Implementation**:

1. **Create `pkg/debug/server.go`**:
   ```go
   type DebugServer struct {
       addr    string
       logs    *RingBuffer
       recorder *Recorder
   }

   func (s *DebugServer) Start()
   func (s *DebugServer) handleRun(w http.ResponseWriter, r *http.Request)
   func (s *DebugServer) handleLogs(w http.ResponseWriter, r *http.Request)
   ```

2. **Add configuration**:
   ```yaml
   debug:
     http_server:
       enabled: true
       addr: ":52538"
   ```

3. **Endpoints**:
   - `GET /debug/logs` - List recent executions
   - `GET /debug/logs/{id}` - Get specific execution
   - `POST /debug/run` - Run with mock input

4. **Files to create**:
   - `pkg/debug/server.go`

5. **Files to modify**:
   - `pkg/config/config.go` - Add debug server config
   - `internal/app/state.go` - Start debug server if enabled

---

## Implementation Order

### Week 1-2: Phase 1
1. JSON Debug Logs (3 days)
2. BranchStrategy Pattern (2 days)

### Week 3-4: Phase 2.1
3. Callback/AOP System (5 days)

### Week 5-6: Phase 2.2
4. RequestContext (5 days)

### Week 7-8: Phase 3
5. Lambda Tools (2 days)
6. HTTP Debug Server (3 days)

---

## Critical Files Reference

| File | Purpose |
|------|---------|
| `internal/agent/orchestrator.go` | Main ReAct loop - **core modifications** |
| `internal/app/state.go` | GlobalState - clarify scope |
| `pkg/config/config.go` | Configuration structures |
| `pkg/tools/registry.go` | Tool registration system |
| `pkg/utils/simplelogger.go` | Current logging - may enhance |

---

## Design Principles

1. **Rule 0 (Reuse)**: Use existing patterns (post-prompts, options pattern)
2. **Rule 1 (Tool Interface)**: Never modify `Tool` interface
3. **Rule 2 (YAML Config)**: All new features configurable via YAML
4. **Rule 5 (State)**: Use RequestContext for per-request, GlobalState for shared
5. **Rule 7 (No Panics)**: All errors returned up the stack

---

## Testing Strategy

Per Rule 9: **No tests initially** - create CLI utilities for verification:

1. `cmd/debug-test/main.go` - Test JSON debug logs
2. `cmd/callbacks-test/main.go` - Test callback handlers
3. `cmd/strategy-test/main.go` - Test branch strategies

---

## Success Criteria

- [ ] JSON debug logs capture full execution trace
- [ ] BranchStrategy allows custom execution patterns
- [ ] Callbacks enable AOP without modifying business logic
- [ ] RequestContext isolates per-request state
- [ ] Lambda tools work for prototyping
- [ ] Debug server provides real-time inspection
- [ ] All features respect existing 11 Immutable Rules
- [ ] All configuration via YAML with ENV support

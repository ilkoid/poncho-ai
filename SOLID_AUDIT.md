# SOLID Audit Report: Poncho AI `pkg/` Directory

**Audit Date**: 2026-01-27
**Scope**: Reusable library code in `pkg/` directory only
**Methodology**: Systematic analysis of 5 SOLID principles

---

## Executive Summary

| Principle | Critical | Major | Minor | Total |
|-----------|----------|-------|-------|-------|
| **SRP** | 1 | 3 | 1 | 5 |
| **OCP** | 0 | 3 | 1 | 4 |
| **LSP** | 0 | 1 | 1 | 2 |
| **ISP** | 0 | 1 | 1 | 2 |
| **DIP** | 1 | 2 | 0 | 3 |
| **TOTAL** | **2** | **10** | **4** | **16** |

---

## Single Responsibility Principle (SRP)

### 🔴 CRITICAL: `pkg/app/components.go:147-357` - `Initialize()` Function

**Violation**: God function with 7 distinct responsibilities

```go
func Initialize(parentCtx context.Context, cfg *config.AppConfig, maxIters int, systemPrompt string) (*Components, error) {
    // 1. S3 client initialization (lines 150-160)
    // 2. WB client initialization (lines 164-213)
    // 3. Dictionaries loading (lines 194-209)
    // 4. CoreState creation (lines 215-235)
    // 5. ModelRegistry setup (lines 237-268)
    // 6. Tools registration (lines 271-346)
    // 7. ReActCycle configuration (lines 307-346)
}
```

**Impact**: 210+ lines, impossible to test in isolation, difficult to reuse

**Recommendation**: Extract into builder pattern:
```go
type ComponentBuilder struct {
    cfg *config.AppConfig
    ctx context.Context
}

func (b *ComponentBuilder) WithS3() (*ComponentBuilder, error)
func (b *ComponentBuilder) WithWB() (*ComponentBuilder, error)
func (b *ComponentBuilder) WithModelRegistry() (*ComponentBuilder, error)
func (b *ComponentBuilder) Build() (*Components, error)
```

---

### 🟠 MAJOR: `pkg/state/core.go:31-676` - `CoreState` Implements 6 Repository Interfaces

**Violation**: Single struct handles 6 domain concerns

```go
type CoreState struct {
    Config *config.AppConfig
    mu     sync.RWMutex
    store  map[string]any
}

// Implements ALL of these:
// - MessageRepository    (chat history)
// - FileRepository       (S3 files)
// - TodoRepository       (task management)
// - DictionaryRepository (WB dictionaries)
// - StorageRepository    (S3 client)
// - ToolsRepository      (tool registry)
```

**Impact**:
- Changes to one domain affect all others
- Violates separation of concerns
- Difficult to mock specific repositories

**Recommendation**: Split into focused repositories:
```go
// pkg/state/repositories.go
type MessageRepository struct { store *UnifiedStore }
type FileRepository struct { store *UnifiedStore }
type TodoRepository struct { store *UnifiedStore }
// etc.

// CoreState becomes a facade:
type CoreState struct {
    Messages  *MessageRepository
    Files     *FileRepository
    Todos     *TodoRepository
}
```

---

### 🟠 MAJOR: `pkg/chain/executor.go:74-441` - `ReActExecutor` Mixes Concerns

**Violation**: Orchestration + event emission + interruption handling + debug recording

```go
type ReActExecutor struct {
    observers         []ExecutionObserver  // lifecycle hooks
    iterationObserver *EmitterIterationObserver  // event emission
}

func (e *ReActExecutor) Execute(ctx, exec) (ChainOutput, error) {
    // Lines 162-176: Message appending
    // Lines 180-228: LLM invocation + event emission
    // Lines 230-241: Signal checking
    // Lines 243-262: Tool execution
    // Lines 263-307: Interruption handling (duplicated!)
    // Lines 316-372: Interruption handling (AGAIN!)
    // Lines 389-428: Result formatting
}
```

**Impact**:
- Duplicated interruption logic (lines 263-307 and 316-372)
- 268-line method
- Difficult to test individual concerns

**Recommendation**: Extract interruption handler:
```go
type InterruptionHandler struct {
    promptsDir string
    promptPath string
}

func (h *InterruptionHandler) HandleInterruption(ctx, exec, message) error
```

---

### 🟠 MAJOR: `pkg/app/components.go:479-643` - Tool Setup Functions

**Violation**: `setupWBTools()` hardcodes 24 tool instantiations

```go
func setupWBTools(registry, cfg, wbClient) error {
    // Lines 505-541: 16 hardcoded WB tool registrations
    // Each follows identical pattern:
    if toolCfg, exists := getToolCfg("search_wb_products"); exists && isEnabled("search_wb_products") {
        if err := register("search_wb_products", std.NewWbProductSearchTool(wbClient, toolCfg, cfg.WB)); err != nil {
            return err
        }
    }
    // ... repeated 15 more times
}
```

**Impact**: Adding new tool requires modifying this function

**Recommendation**: Use registration strategy pattern:
```go
type ToolFactory interface {
    CreateTool(dependencies) (Tool, error)
    Name() string
}

type WBToolFactory struct {
    toolName string
    cfg      config.ToolConfig
}

func (f *WBToolFactory) Register(registry, dependencies) error {
    if !f.isEnabled() {
        return nil
    }
    tool := f.create(dependencies)
    return registry.Register(tool)
}
```

---

### 🟡 MINOR: `pkg/agent/agent.go:44-507` - `Client` Multiple Responsibilities

**Violation**: Client handles execution + events + config + registries + presets

```go
type Client struct {
    reactCycle    *chain.ReActCycle
    modelRegistry *models.Registry
    toolsRegistry *tools.Registry
    state         *state.CoreState
    config        *config.AppConfig
    wbClient      *wb.Client
    emitter       events.Emitter  // Multiple concerns
}
```

**Impact**: Moderate - facade pattern is acceptable here

**Recommendation**: Consider splitting into `AgentExecutor` + `AgentConfig`

---

## Open/Closed Principle (OCP)

### 🟠 MAJOR: `pkg/models/registry.go:122-129` - Provider Factory Switch

**Violation**: Switch statement on provider type

```go
func CreateProvider(modelDef config.ModelDef) (llm.Provider, error) {
    switch modelDef.Provider {
    case "zai", "openai", "deepseek", "openrouter":
        return openai.NewClient(modelDef), nil
    default:
        return nil, fmt.Errorf("unknown provider type: %s", modelDef.Provider)
    }
}
```

**Impact**: Adding new provider (e.g., "anthropic") requires modifying this function

**Recommendation**: Registry pattern:
```go
type ProviderFactory interface {
    Create(config.ModelDef) (llm.Provider, error)
    SupportedProviders() []string
}

var providerFactories = map[string]ProviderFactory{
    "openai": &OpenAIFactory{},
    "anthropic": &AnthropicFactory{},
}

func RegisterProviderFactory(name string, factory ProviderFactory) {
    providerFactories[name] = factory
}
```

---

### 🟠 MAJOR: `pkg/app/components.go:479-885` - Hardcoded Tool Registration

**Violation**: 6 `setup*Tools()` functions with hardcoded tool names

```go
func setupWBTools()     // 16 hardcoded tools
func setupS3Tools()     // 5 hardcoded tools
func setupVisionTools() // 1 hardcoded tool
func setupLLMTools()    // 2 hardcoded tools
func setupTodoTools()   // 5 hardcoded tools
func setupDictionaryTools() // 5 hardcoded tools
```

**Impact**: Adding new tool category requires new function + modifications

**Recommendation**: Convention-based registration:
```go
type ToolRegistrar interface {
    Category() string
    Register(*tools.Registry, *ToolContext) error
}

// Auto-discover via reflection or explicit registration
var toolRegistrars = []ToolRegistrar{
    &WBToolRegistrar{},
    &S3ToolRegistrar{},
}
```

---

### 🟠 MAJOR: `pkg/chain/react.go:196-200` - Direct Step Instantiation

**Violation**: Concrete LLMInvocationStep creation in template

```go
func NewReActCycle(config ReActCycleConfig) *ReActCycle {
    cycle := &ReActCycle{...}
    cycle.llmStep = &LLMInvocationStep{  // Concrete type!
        systemPrompt: config.SystemPrompt,
    }
    return cycle
}
```

**Impact**: Cannot swap step implementations without modifying template

**Recommendation**: Factory injection:
```go
type StepFactory interface {
    CreateLLMStep(prompt string) LLMStep
    CreateToolStep(registry *tools.Registry) ToolStep
}

func NewReActCycle(config ReActCycleConfig, factory StepFactory) *ReActCycle
```

---

### 🟡 MINOR: `pkg/llm/streaming.go:107-116` - Flag-Based Switching

**Violation**: Opt-out pattern for streaming

```go
func IsStreamingMode(opts ...any) bool {
    for _, opt := range opts {
        if streamOpt, ok := opt.(StreamOption); ok {
            return streamOpt.Enabled  // Flag-based behavior
        }
    }
    return true  // Default
}
```

**Impact**: Minor - options pattern is acceptable

**Recommendation**: Consider explicit mode enum:
```go
type StreamingMode int
const (
    StreamingDisabled StreamingMode = iota
    StreamingEnabled
)
```

---

## Liskov Substitution Principle (LSP)

### 🟠 MAJOR: `pkg/llm/streaming.go:21-51` - `StreamingProvider` Interface Extension

**Violation**: No guarantee `Generate()` and `GenerateStream()` are equivalent

```go
type StreamingProvider interface {
    Provider  // Has Generate(ctx, messages, opts) (Message, error)
    GenerateStream(ctx, messages, callback, opts) (Message, error)
}
```

**Problem**:
- `Generate()` might use caching
- `GenerateStream()` might bypass cache
- No documented contract about equivalence

**Impact**: Substituting `StreamingProvider` for `Provider` may change behavior

**Recommendation**: Document contract or use composition:
```go
// Option 1: Document contract
// "GenerateStream MUST return equivalent results to Generate()"

// Option 2: Composition
type StreamingAdapter struct {
    provider Provider
}

func (s *StreamingAdapter) GenerateStream(...) {
    // Calls provider.Generate() internally
}
```

---

### 🟡 MINOR: `pkg/state/core.go:115-174` - Generic Type Assertion

**Violation**: Runtime type assertion may fail

```go
func GetType[T any](s *CoreState, key Key) (T, bool) {
    val, ok := s.store[string(key)]
    if !ok {
        return zero, false
    }
    typed, ok := val.(T)  // Could fail if wrong type stored
    if !ok {
        return zero, false
    }
    return typed, true
}
```

**Impact**: Caller expects type T, but may get zero value

**Recommendation**: Consider typed keys:
```go
type TypedKey[T any] struct {
    key string
}

func (k TypedKey[T]) Get(s *CoreState) (T, error) {
    // Compile-time type safety
}
```

---

## Interface Segregation Principle (ISP)

### 🟠 MAJOR: `pkg/chain/executor.go:114-119` - `ExecutionObserver` Interface

**Violation**: 4 methods, implementers may not need all

```go
type ExecutionObserver interface {
    OnStart(ctx, exec)
    OnIterationStart(iteration int)
    OnIterationEnd(iteration int)
    OnFinish(result, error)
}
```

**Problem**:
- `EmitterObserver` only needs `OnFinish`
- `ChainDebugRecorder` needs all 4
- Forcing single-method observers to implement empty methods

**Impact**: Unnecessary coupling between lifecycle events

**Recommendation**: Split into focused interfaces:
```go
type LifecycleObserver interface {
    OnStart(ctx, exec)
    OnFinish(result, error)
}

type IterationObserver interface {
    OnIterationStart(iteration int)
    OnIterationEnd(iteration int)
}

// Compose for full lifecycle:
type FullExecutionObserver interface {
    LifecycleObserver
    IterationObserver
}
```

---

### 🟡 MINOR: `pkg/events/events.go:193-199` - `Emitter` Interface Context Coupling

**Violation**: Context parameter in all emissions

```go
type Emitter interface {
    Emit(ctx context.Context, event Event)
}
```

**Impact**: Minor - context is appropriate for cancellation

**Recommendation**: Consider separating:
```go
type SyncEmitter interface {
    Emit(event Event) error
}

type AsyncEmitter interface {
    EmitAsync(ctx context.Context, event Event) error
}
```

---

## Dependency Inversion Principle (DIP)

### 🔴 CRITICAL: `pkg/app/components.go:122-147` - High-Level Depends on Low-Level

**Violation**: Direct filesystem access in initialization

```go
func InitializeConfig(finder ConfigPathFinder) (*config.AppConfig, string, error) {
    cfgPath := finder.FindConfigPath()  // Abstract - OK

    cfg, err := config.Load(cfgPath)  // Direct filesystem dependency!
    if err != nil {
        return nil, "", fmt.Errorf("failed to load config from %s: %w", cfgPath, err)
    }
    return cfg, cfgPath, nil
}
```

**Problem**: `InitializeConfig` depends on `config.Load()` which reads files

**Impact**: Cannot test without real files or complex mocking

**Recommendation**: Inject config loader:
```go
type ConfigLoader interface {
    Load(path string) (*config.AppConfig, error)
}

func InitializeConfig(finder ConfigPathFinder, loader ConfigLoader) (*config.AppConfig, string, error) {
    cfgPath := finder.FindConfigPath()
    return loader.Load(cfgPath), cfgPath, nil
}
```

---

### 🟠 MAJOR: `pkg/models/registry.go:122-129` - Concrete Provider Creation

**Violation**: Factory creates concrete types

```go
func CreateProvider(modelDef config.ModelDef) (llm.Provider, error) {
    switch modelDef.Provider {
    case "zai", "openai", "deepseek", "openrouter":
        return openai.NewClient(modelDef), nil  // Concrete type!
    default:
        return nil, fmt.Errorf("unknown provider type: %s", modelDef.Provider)
    }
}
```

**Impact**: Adding new provider requires modifying factory

**Recommendation**: See OCP recommendation - use provider factory registry

---

### 🟠 MAJOR: `pkg/chain/react.go:183-200` - Dependencies via Setters

**Violation**: Optional dependencies set after construction

```go
func NewReActCycle(config ReActCycleConfig) *ReActCycle {
    cycle := &ReActCycle{
        config:     config,
        promptsDir: config.PromptsDir,
    }
    // Dependencies added later:
    // cycle.SetModelRegistry(...)
    // cycle.SetRegistry(...)
    // cycle.SetState(...)
    return cycle
}
```

**Impact**: Incomplete state between construction and dependency injection

**Recommendation**: Constructor injection:
```go
func NewReActCycle(
    config ReActCycleConfig,
    registry *models.Registry,
    tools *tools.Registry,
    state *state.CoreState,
) *ReActCycle
```

---

## Architectural Insights

### ★ Insight ─────────────────────────────────────
**Key Findings:**

1. **Repository Pattern vs God Object**: `CoreState` implements 6 repository interfaces. While unified storage is elegant, it violates SRP. Consider the "Facade over Repositories" pattern instead.

2. **Factory Switch Smell**: The `CreateProvider()` switch in `models/registry.go` is a classic OCP violation. Provider registration via map[string]Factory would be more extensible.

3. **Observer Interface Segregation**: `ExecutionObserver` with 4 methods forces implementers like `EmitterObserver` to provide empty methods. Split into `LifecycleObserver` + `IterationObserver`.
─────────────────────────────────────────────────

---

## Recommended Refactoring Priority

### Phase 1: Critical (Do First)
1. Split `pkg/app/components.go:Initialize()` into builder pattern
2. Add `ConfigLoader` interface for dependency injection

### Phase 2: Major (High Impact)
3. Split `CoreState` into focused repositories
4. Replace `CreateProvider()` switch with factory registry
5. Split `ExecutionObserver` into segregated interfaces
6. Extract interruption handler from `ReActExecutor`

### Phase 3: Minor (Polish)
7. Document `StreamingProvider` contract
8. Use constructor injection in `ReActCycle`
9. Extract tool registration strategy pattern

---

## Conclusion

The Poncho AI `pkg/` codebase demonstrates **good architectural intent** with patterns like Repository, Registry, and Observer. However, **SOLID adherence is moderate** with:

- **2 Critical violations** requiring immediate attention
- **10 Major violations** impacting maintainability
- **4 Minor violations** acceptable for now

The most impactful improvements would be:
1. Breaking down the `Initialize()` god function
2. Segregating `CoreState` repositories
3. Implementing provider factory registration

These changes would significantly improve testability, extensibility, and maintainability while preserving the framework's reusability goals.

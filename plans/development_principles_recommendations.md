# Additional Development Principles for Poncho AI

## Executive Summary

This document provides recommendations for additional development principles to complement the existing 11 Immutable Rules in [`dev_manifest.md`](../dev_manifest.md). These recommendations are based on analysis of the Poncho AI codebase and Go best practices for AI/LLM frameworks.

---

## Current State Analysis

### Strengths of Existing Principles

The current 11 rules provide an excellent foundation:
- **Rule 1 (Tool Interface)**: "Raw In, String Out" is perfect for LLM tools
- **Rule 3 (Registry)**: Enables modular, composable architecture
- **Rule 4 (LLM Abstraction)**: Future-proof against provider changes
- **Rule 7 (Error Handling)**: Critical for AI agent resilience
- **Rule 11 (Resource Localization)**: Enables autonomous deployment

### Identified Gaps

While the existing rules cover core architecture, several areas need additional guidance:
1. **Context and cancellation** patterns for long-running operations
2. **Resource lifecycle** management beyond basic cleanup
3. **Observability** beyond debug logs
4. **Security** practices for API keys and sensitive data
5. **Performance** optimization patterns
6. **Structured error** types and handling
7. **Concurrency** patterns for parallel tool execution
8. **Testing** beyond CLI utilities
9. **API stability** and versioning
10. **Documentation** standards beyond godoc

---

## Recommended Additional Principles

### Principle 12: Context Propagation and Cancellation

**Principle**: All long-running operations must accept and properly handle `context.Context`. Context must be propagated through all layers.

**Rationale**: AI agent operations can be long-running (LLM calls, batch processing, API requests). Proper context handling enables:
- Graceful shutdown on timeout
- Cancellation of in-flight operations
- Request-scoped values (tracing, user ID)

**Implementation Guidelines**:

```go
// ✅ GOOD: Context propagation
func (t *MyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    // Pass context to all downstream calls
    result, err := t.client.DoSomething(ctx, ...)
    if err != nil {
        return "", fmt.Errorf("operation failed: %w", err)
    }
    return result, nil
}

// ❌ BAD: Ignoring context
func (t *MyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    // Using context.Background() - loses cancellation
    result, err := t.client.DoSomething(context.Background(), ...)
    return result, err
}
```

**Specific Requirements**:
- All tool `Execute()` methods must respect context cancellation
- LLM provider calls must pass context through
- HTTP clients must use context for requests
- Background goroutines must inherit parent context
- Use `select` statements for context checks in loops

**Example Pattern**:
```go
func (t *BatchTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    for i, item := range items {
        select {
        case <-ctx.Done():
            return "", ctx.Err() // Graceful cancellation
        default:
            // Process item
            if err := t.processItem(ctx, item); err != nil {
                return "", err
            }
        }
    }
}
```

---

### Principle 13: Resource Lifecycle Management

**Principle**: All resources (files, connections, goroutines) must be explicitly managed with proper cleanup. Use `defer` for cleanup, implement `io.Closer` for complex resources.

**Rationale**: Go's garbage collector handles memory, but external resources need explicit cleanup. Poor resource management leads to:
- File descriptor leaks
- Connection pool exhaustion
- Goroutine leaks
- Memory leaks (via references)

**Implementation Guidelines**:

```go
// ✅ GOOD: Proper cleanup with defer
func (c *Client) ProcessFile(path string) error {
    file, err := os.Open(path)
    if err != nil {
        return err
    }
    defer file.Close() // Always executed

    // Process file
    return nil
}

// ✅ GOOD: Implement io.Closer for complex resources
type ResourceManager struct {
    conn net.Conn
    file *os.File
}

func (rm *ResourceManager) Close() error {
    var errs []error
    
    if rm.conn != nil {
        if err := rm.conn.Close(); err != nil {
            errs = append(errs, err)
        }
    }
    
    if rm.file != nil {
        if err := rm.file.Close(); err != nil {
            errs = append(errs, err)
        }
    }
    
    if len(errs) > 0 {
        return fmt.Errorf("multiple errors: %v", errs)
    }
    return nil
}

// Usage
func process() error {
    rm := NewResourceManager()
    defer rm.Close() // Cleanup all resources
    
    // Use resources
    return nil
}
```

**Specific Requirements**:
- Always `defer` file.Close() after successful os.Open()
- Always `defer` response.Body.Close() after http requests
- Implement `io.Closer` for resources with multiple components
- Use `sync.Pool` for frequently allocated objects
- Cancel contexts for background goroutines
- Use `context.WithCancel()` for goroutines you control

**Goroutine Management Pattern**:
```go
// ✅ GOOD: Proper goroutine lifecycle
func (s *Service) Start(ctx context.Context) error {
    ctx, cancel := context.WithCancel(ctx)
    defer cancel() // Ensure cleanup
    
    done := make(chan error)
    
    go func() {
        defer close(done)
        done <- s.runWorker(ctx)
    }()
    
    select {
    case err := <-done:
        return err
    case <-ctx.Done():
        return ctx.Err()
    }
}
```

---

### Principle 14: Observability and Structured Logging

**Principle**: Use structured logging with consistent fields. Log at appropriate levels (Debug, Info, Warn, Error). Include request IDs, timing, and context.

**Rationale**: Debug logs ([`pkg/debug/`](../pkg/debug/) provide detailed traces, but production needs:
- Real-time monitoring
- Alerting on errors
- Performance metrics
- Request tracing across components

**Implementation Guidelines**:

```go
// ✅ GOOD: Structured logging with context
func (t *MyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    requestID := getRequestID(ctx)
    
    utils.Info("Tool execution started",
        "tool", "my_tool",
        "request_id", requestID,
        "args_length", len(argsJSON),
    )
    
    start := time.Now()
    result, err := t.doWork(ctx, argsJSON)
    
    if err != nil {
        utils.Error("Tool execution failed",
            "tool", "my_tool",
            "request_id", requestID,
            "error", err,
            "duration_ms", time.Since(start).Milliseconds(),
        )
        return "", err
    }
    
    utils.Info("Tool execution completed",
        "tool", "my_tool",
        "request_id", requestID,
        "duration_ms", time.Since(start).Milliseconds(),
        "result_size", len(result),
    )
    
    return result, nil
}

// ❌ BAD: Unstructured logging
func (t *MyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    log.Printf("Starting tool...") // No context, no structure
    result, err := t.doWork(ctx, argsJSON)
    if err != nil {
        log.Printf("Error: %v", err) // No request ID, no timing
        return "", err
    }
    log.Printf("Done") // No metrics
    return result, nil
}
```

**Log Levels**:
- **Debug**: Detailed execution traces (disabled in production)
- **Info**: Normal operations, request lifecycle
- **Warn**: Recoverable errors, retries
- **Error**: Failures requiring attention

**Required Fields**:
- `request_id` or `trace_id` for correlation
- `tool` or `component` name
- `duration_ms` for operations
- `error` for error logs
- Context-specific fields (user_id, article_id, etc.)

**Metrics Collection**:
```go
// Add metrics to critical operations
func (t *MyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    start := time.Now()
    defer func() {
        metrics.RecordDuration("tool.my_tool.duration", time.Since(start))
    }()
    
    result, err := t.doWork(ctx, argsJSON)
    if err != nil {
        metrics.Increment("tool.my_tool.errors")
        return "", err
    }
    
    metrics.Increment("tool.my_tool.success")
    return result, nil
}
```

---

### Principle 15: Security and Secrets Management

**Principle**: Never hardcode secrets. Use environment variables or secret management. Log redaction for sensitive data. Validate all inputs.

**Rationale**: AI frameworks often handle API keys, tokens, and user data. Security breaches can:
- Expose API keys and credentials
- Leak user data
- Enable unauthorized access
- Violate compliance (GDPR, etc.)

**Implementation Guidelines**:

```go
// ✅ GOOD: Environment variables for secrets
type Config struct {
    APIKey string `yaml:"api_key"` // Loaded from ${API_KEY}
}

func LoadConfig() (*Config, error) {
    // ExpandEnv replaces ${VAR} with environment values
    data, err := os.ReadFile("config.yaml")
    if err != nil {
        return nil, err
    }
    
    expanded := os.ExpandEnv(string(data))
    // Parse YAML...
}

// ✅ GOOD: Secret validation
func (c *Config) Validate() error {
    if c.APIKey == "" || c.APIKey == "${API_KEY}" {
        return errors.New("API_KEY environment variable not set")
    }
    
    if len(c.APIKey) < 32 {
        return errors.New("API_KEY appears invalid (too short)")
    }
    
    return nil
}

// ✅ GOOD: Log redaction
func (t *MyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    // Parse args
    var args struct {
        Token string `json:"token"`
        Data  string `json:"data"`
    }
    json.Unmarshal([]byte(argsJSON), &args)
    
    // Log with redaction
    utils.Info("Processing request",
        "token", redactToken(args.Token), // "abc...xyz"
        "data_length", len(args.Data),
    )
    
    return t.process(ctx, args)
}

func redactToken(token string) string {
    if len(token) <= 8 {
        return "***"
    }
    return token[:4] + "..." + token[len(token)-4:]
}
```

**Specific Requirements**:
- All secrets in `config.yaml` must use `${VAR}` syntax
- Never commit secrets to git (add to `.gitignore`)
- Validate secrets are not default/template values
- Redact sensitive data in logs (tokens, passwords, PII)
- Use HTTPS for all external API calls
- Implement rate limiting to prevent abuse
- Validate and sanitize all user inputs

**Input Validation Pattern**:
```go
func (t *MyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    var args struct {
        ArticleID string `json:"article_id"`
    }
    
    if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
        return "", fmt.Errorf("invalid JSON: %w", err)
    }
    
    // Validate
    if args.ArticleID == "" {
        return "", errors.New("article_id is required")
    }
    
    if len(args.ArticleID) > 100 {
        return "", errors.New("article_id too long (max 100 chars)")
    }
    
    // Sanitize (prevent injection)
    args.ArticleID = strings.TrimSpace(args.ArticleID)
    
    return t.process(ctx, args.ArticleID)
}
```

---

### Principle 16: Performance Optimization Patterns

**Principle**: Optimize for common cases. Use caching for expensive operations. Pool frequently allocated objects. Profile before optimizing.

**Rationale**: AI agents often make repeated calls to same APIs or process similar data. Optimization improves:
- Response time (user experience)
- API quota usage (cost reduction)
- Resource utilization (scalability)

**Implementation Guidelines**:

```go
// ✅ GOOD: Caching for expensive operations
type CachedWBClient struct {
    client    *wb.Client
    cache     *lru.Cache[string, []Category]
    cacheTTL time.Duration
    mu        sync.RWMutex
}

func (c *CachedWBClient) GetParentCategories(ctx context.Context) ([]Category, error) {
    cacheKey := "parent_categories"
    
    // Check cache
    c.mu.RLock()
    if cached, ok := c.cache.Get(cacheKey); ok {
        c.mu.RUnlock()
        utils.Debug("Cache hit", "key", cacheKey)
        return cached, nil
    }
    c.mu.RUnlock()
    
    // Cache miss - fetch from API
    categories, err := c.client.GetParentCategories(ctx)
    if err != nil {
        return nil, err
    }
    
    // Update cache
    c.mu.Lock()
    c.cache.Add(cacheKey, categories)
    c.mu.Unlock()
    
    utils.Debug("Cache miss - fetched from API", "key", cacheKey)
    return categories, nil
}

// ✅ GOOD: Object pooling for frequent allocations
var jsonBufferPool = sync.Pool{
    New: func() interface{} {
        b := make([]byte, 0, 1024)
        return &b
    },
}

func marshalJSON(v interface{}) (string, error) {
    bufPtr := jsonBufferPool.Get().(*[]byte)
    defer func() {
        *bufPtr = (*bufPtr)[:0] // Reset
        jsonBufferPool.Put(bufPtr)
    }()
    
    buf := *bufPtr
    data, err := json.Marshal(v)
    if err != nil {
        return "", err
    }
    
    return string(data), nil
}
```

**Optimization Checklist**:
- [ ] Profile with `go test -cpuprofile` before optimizing
- [ ] Cache expensive API calls (dictionaries, categories)
- [ ] Use connection pooling for HTTP clients
- [ ] Pool frequently allocated objects (byte buffers, JSON encoders)
- [ ] Batch operations when possible (S3 downloads, API calls)
- [ ] Use streaming for large responses (avoid loading all in memory)
- [ ] Implement rate limiting to avoid API throttling
- [ ] Compress large payloads

**Caching Strategy**:
```go
// Cache configuration
type CacheConfig struct {
    Enabled bool          `yaml:"enabled"`
    TTL     time.Duration `yaml:"ttl"`
    MaxSize int           `yaml:"max_size"` // Number of entries
}

// Cache decorator pattern
func WithCache(client *wb.Client, cfg CacheConfig) *wb.Client {
    if !cfg.Enabled {
        return client
    }
    
    return &CachedWBClient{
        client:    client,
        cache:     lru.New[string, interface{}](cfg.MaxSize),
        cacheTTL: cfg.TTL,
    }
}
```

---

### Principle 17: Structured Error Types

**Principle**: Use typed errors with context. Implement error wrapping with `fmt.Errorf("%w")`. Define error types for common failure modes.

**Rationale**: Beyond Rule 7 ("no panic"), structured errors enable:
- Programmatic error handling (retry logic, fallbacks)
- Better error messages for users
- Debugging with stack traces
- Error classification (transient vs permanent)

**Implementation Guidelines**:

```go
// ✅ GOOD: Typed errors
import (
    "errors"
    "fmt"
)

// Define error types
var (
    ErrToolNotFound    = errors.New("tool not found")
    ErrInvalidArgs    = errors.New("invalid arguments")
    ErrRateLimit      = errors.New("rate limit exceeded")
    ErrUnauthorized   = errors.New("unauthorized access")
)

// Error with context
func (t *MyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    var args struct {
        ArticleID string `json:"article_id"`
    }
    
    if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
        // Wrap error with context
        return "", fmt.Errorf("failed to parse arguments: %w", err)
    }
    
    if args.ArticleID == "" {
        // Return typed error
        return "", ErrInvalidArgs
    }
    
    result, err := t.client.GetArticle(ctx, args.ArticleID)
    if err != nil {
        // Wrap with tool context
        return "", fmt.Errorf("get article failed: %w", err)
    }
    
    return result, nil
}

// ✅ GOOD: Error checking with types
func handleToolError(err error) string {
    if errors.Is(err, ErrInvalidArgs) {
        return "Invalid arguments provided. Please check your input."
    }
    
    if errors.Is(err, ErrRateLimit) {
        return "Rate limit exceeded. Please try again later."
    }
    
    if errors.Is(err, context.DeadlineExceeded) {
        return "Request timed out. Please try again."
    }
    
    // Unknown error
    return fmt.Sprintf("An error occurred: %v", err)
}
```

**Error Categories**:
```go
// Transient errors - can be retried
var (
    ErrTimeout      = errors.New("operation timeout")
    ErrRateLimit    = errors.New("rate limit exceeded")
    ErrNetworkError = errors.New("network error")
)

// Permanent errors - should not be retried
var (
    ErrUnauthorized = errors.New("unauthorized")
    ErrNotFound    = errors.New("not found")
    ErrInvalidArgs = errors.New("invalid arguments")
)

// Business logic errors - expected conditions
var (
    ErrInsufficientData = errors.New("insufficient data")
    ErrConflict        = errors.New("conflict")
)
```

**Error Wrapping Pattern**:
```go
// Wrap errors at each layer with context
func (s *Service) ProcessArticle(ctx context.Context, id string) error {
    // Layer 1: Service
    article, err := s.repo.GetArticle(ctx, id)
    if err != nil {
        return fmt.Errorf("get article %s: %w", id, err)
    }
    
    // Layer 2: Business logic
    if err := s.validateArticle(article); err != nil {
        return fmt.Errorf("validate article %s: %w", id, err)
    }
    
    // Layer 3: Persistence
    if err := s.repo.SaveArticle(ctx, article); err != nil {
        return fmt.Errorf("save article %s: %w", id, err)
    }
    
    return nil
}
```

---

### Principle 18: Concurrency Patterns

**Principle**: Use goroutines for parallel independent operations. Use sync.WaitGroup for coordination. Avoid data races with proper synchronization.

**Rationale**: AI agents can benefit from parallel execution:
- Multiple tools can run simultaneously
- Batch processing can be parallelized
- I/O-bound operations can overlap

**Implementation Guidelines**:

```go
// ✅ GOOD: Parallel tool execution with WaitGroup
func (o *Orchestrator) executeToolsParallel(ctx context.Context, toolCalls []ToolCall) ([]ToolResult, error) {
    var wg sync.WaitGroup
    results := make([]ToolResult, len(toolCalls))
    errChan := make(chan error, len(toolCalls))
    
    for i, tc := range toolCalls {
        wg.Add(1)
        go func(idx int, call ToolCall) {
            defer wg.Done()
            
            result, err := o.executeTool(ctx, call)
            results[idx] = ToolResult{
                Result: result,
                Error:  err,
            }
            
            if err != nil {
                errChan <- err
            }
        }(i, tc)
    }
    
    // Wait for all goroutines
    wg.Wait()
    close(errChan)
    
    // Check for errors
    var errs []error
    for err := range errChan {
        errs = append(errs, err)
    }
    
    if len(errs) > 0 {
        return results, fmt.Errorf("multiple errors: %v", errs)
    }
    
    return results, nil
}

// ✅ GOOD: Worker pool for batch processing
type WorkerPool struct {
    tasks   chan Task
    results chan Result
    workers int
    wg      sync.WaitGroup
}

func NewWorkerPool(workers int) *WorkerPool {
    return &WorkerPool{
        tasks:   make(chan Task, 100),
        results: make(chan Result, 100),
        workers: workers,
    }
}

func (p *WorkerPool) Start(ctx context.Context) {
    for i := 0; i < p.workers; i++ {
        p.wg.Add(1)
        go p.worker(ctx)
    }
}

func (p *WorkerPool) worker(ctx context.Context) {
    defer p.wg.Done()
    
    for {
        select {
        case <-ctx.Done():
            return
        case task, ok := <-p.tasks:
            if !ok {
                return
            }
            result := task.Execute(ctx)
            p.results <- result
        }
    }
}

func (p *WorkerPool) Submit(task Task) {
    p.tasks <- task
}

func (p *WorkerPool) Stop() {
    close(p.tasks)
    p.wg.Wait()
    close(p.results)
}
```

**Concurrency Best Practices**:
- Use `sync.Mutex` for protecting shared state (already done in [`GlobalState`](../internal/app/state.go))
- Use `sync.RWMutex` for read-heavy data
- Use channels for communication between goroutines
- Use `sync.WaitGroup` for waiting on multiple goroutines
- Use `context.WithCancel()` for goroutine cancellation
- Avoid global mutable state
- Use `go test -race` to detect data races

**Parallel Tool Execution Pattern**:
```go
// Execute independent tools in parallel
func (o *Orchestrator) executeIndependentTools(ctx context.Context, tools []Tool) (map[string]string, error) {
    results := make(map[string]string)
    var mu sync.Mutex
    var wg sync.WaitGroup
    errChan := make(chan error, len(tools))
    
    for _, tool := range tools {
        wg.Add(1)
        go func(t Tool) {
            defer wg.Done()
            
            result, err := t.Execute(ctx, "{}")
            if err != nil {
                errChan <- err
                return
            }
            
            mu.Lock()
            results[t.Definition().Name] = result
            mu.Unlock()
        }(tool)
    }
    
    wg.Wait()
    close(errChan)
    
    // Collect errors
    var errs []error
    for err := range errChan {
        errs = append(errs, err)
    }
    
    if len(errs) > 0 {
        return results, fmt.Errorf("some tools failed: %v", errs)
    }
    
    return results, nil
}
```

---

### Principle 19: Testing Strategy

**Principle**: Beyond Rule 9 (CLI utilities), add unit tests for pure functions, integration tests for components, and benchmarks for performance-critical code.

**Rationale**: CLI utilities are great for manual testing, but automated tests provide:
- Continuous integration protection
- Regression detection
- Documentation through examples
- Performance monitoring

**Implementation Guidelines**:

```go
// ✅ GOOD: Unit tests for pure functions
func TestCleanJsonBlock(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected string
    }{
        {
            name:     "removes markdown wrapper",
            input:    "```json\n{\"key\": \"value\"}\n```",
            expected: "{\"key\": \"value\"}",
        },
        {
            name:     "no wrapper",
            input:    "{\"key\": \"value\"}",
            expected: "{\"key\": \"value\"}",
        },
        {
            name:     "empty string",
            input:    "",
            expected: "",
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := CleanJsonBlock(tt.input)
            if result != tt.expected {
                t.Errorf("CleanJsonBlock() = %v, want %v", result, tt.expected)
            }
        })
    }
}

// ✅ GOOD: Table-driven tests
func TestToolExecution(t *testing.T) {
    mockClient := &MockWBClient{}
    tool := NewMyTool(mockClient)
    
    tests := []struct {
        name    string
        args    string
        want    string
        wantErr bool
    }{
        {
            name:    "valid arguments",
            args:    `{"article_id": "12345"}`,
            want:    `{"result": "success"}`,
            wantErr: false,
        },
        {
            name:    "invalid JSON",
            args:    `{"article_id": 12345}`, // Should be string
            want:    "",
            wantErr: true,
        },
        {
            name:    "missing required field",
            args:    `{}`,
            want:    "",
            wantErr: true,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            ctx := context.Background()
            result, err := tool.Execute(ctx, tt.args)
            
            if (err != nil) != tt.wantErr {
                t.Errorf("Execute() error = %v, wantErr %v", err, tt.wantErr)
            }
            
            if result != tt.want {
                t.Errorf("Execute() = %v, want %v", result, tt.want)
            }
        })
    }
}

// ✅ GOOD: Benchmark tests
func BenchmarkToolExecution(b *testing.B) {
    tool := NewMyTool(&MockWBClient{})
    ctx := context.Background()
    args := `{"article_id": "12345"}`
    
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _, _ = tool.Execute(ctx, args)
    }
}

// ✅ GOOD: Integration tests
func TestWBClientIntegration(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test in short mode")
    }
    
    apiKey := os.Getenv("WB_API_KEY_TEST")
    if apiKey == "" {
        t.Skip("WB_API_KEY_TEST not set")
    }
    
    client := wb.New(apiKey)
    ctx := context.Background()
    
    // Test real API call
    categories, err := client.GetParentCategories(ctx, "https://content-api.wildberries.ru", 100, 5)
    if err != nil {
        t.Fatalf("GetParentCategories failed: %v", err)
    }
    
    if len(categories) == 0 {
        t.Error("expected at least one category")
    }
}
```

**Testing Pyramid**:
```
        /\
       /  \
      / E2E \        (CLI utilities, manual testing)
     /--------\
    /  Integration \   (API clients, database)
   /--------------\
  /     Unit       \  (pure functions, algorithms)
 /------------------\
```

**Test Coverage Requirements**:
- Unit tests: >80% coverage for business logic
- Integration tests: Critical paths (LLM calls, API clients)
- E2E tests: CLI utilities (existing Rule 9 approach)
- Benchmark tests: Performance-critical code

**Mock Pattern**:
```go
// Mock for testing
type MockWBClient struct {
    GetParentCategoriesFunc func(ctx context.Context) ([]Category, error)
}

func (m *MockWBClient) GetParentCategories(ctx context.Context, endpoint string, rateLimit, burst int) ([]Category, error) {
    if m.GetParentCategoriesFunc != nil {
        return m.GetParentCategoriesFunc(ctx)
    }
    return []Category{{ID: 1, Name: "Test"}}, nil
}

// Usage in test
func TestMyTool(t *testing.T) {
    mock := &MockWBClient{
        GetParentCategoriesFunc: func(ctx context.Context) ([]Category, error) {
            return []Category{{ID: 1, Name: "Mock"}}, nil
        },
    }
    
    tool := NewMyTool(mock)
    result, err := tool.Execute(ctx, "{}")
    // Assertions...
}
```

---

### Principle 20: API Stability and Versioning

**Principle**: Public APIs must be stable. Use semantic versioning. Document breaking changes. Provide migration paths for deprecated APIs.

**Rationale**: Poncho AI is a framework used by multiple applications. API stability ensures:
- Applications don't break on updates
- Developers can upgrade safely
- Long-term maintenance is feasible

**Implementation Guidelines**:

```go
// ✅ GOOD: Versioned API
const (
    VersionMajor = 1
    VersionMinor = 0
    VersionPatch = 0
)

func Version() string {
    return fmt.Sprintf("%d.%d.%d", VersionMajor, VersionMinor, VersionPatch)
}

// ✅ GOOD: Stable public API
// Package agent provides AI agent orchestration.
//
// # Stability
//
// The public API of this package is considered stable. Breaking changes
// will increment the major version number.
//
// # Migration Guide
//
// For migration from v0.x to v1.0, see MIGRATION.md
package agent

// Agent represents an AI agent that can execute queries.
//
// This interface is stable and will not change without a major version bump.
type Agent interface {
    Run(ctx context.Context, query string) (string, error)
    ClearHistory()
    GetHistory() []llm.Message
}

// ✅ GOOD: Deprecated API with migration path
// Deprecated: Use New instead. This will be removed in v2.0.
//
// Migration: Replace NewOrchestrator with New(agent.Config{...})
func NewOrchestrator(llm llm.Provider, registry *tools.Registry) (*Orchestrator, error) {
    return New(agent.Config{
        LLM:      llm,
        Registry: registry,
        // Use defaults for other fields
    })
}

// ✅ GOOD: Additive changes only
// Before (v1.0):
type Config struct {
    LLM      llm.Provider
    Registry *tools.Registry
}

// After (v1.1 - additive change):
type Config struct {
    LLM      llm.Provider
    Registry *tools.Registry
    // New field with default value
    MaxIters int // Default: 10
}
```

**Versioning Rules**:
- **Major (X.0.0)**: Breaking changes, remove deprecated APIs
- **Minor (1.X.0)**: Additive changes, new features
- **Patch (1.0.X)**: Bug fixes, no API changes

**Deprecation Process**:
1. Mark as deprecated in godoc
2. Add deprecation notice in logs
3. Document migration path
4. Wait for at least one minor version
5. Remove in next major version

**Example Deprecation**:
```go
// Deprecated: Use Execute instead. Will be removed in v2.0.
//
// Migration:
//   Old: orchestrator.Run(ctx, query)
//   New: orchestrator.Execute(ctx, query)
func (o *Orchestrator) Run(ctx context.Context, query string) (string, error) {
    log.Println("Warning: Run() is deprecated, use Execute() instead")
    return o.Execute(ctx, query)
}
```

---

### Principle 21: Documentation Standards

**Principle**: Beyond Rule 10 (godoc), provide usage examples, architecture diagrams, and migration guides. Keep documentation in sync with code.

**Rationale**: Good documentation reduces:
- Onboarding time for new developers
- Support burden
- Misuse of APIs
- Knowledge loss

**Implementation Guidelines**:

```go
// ✅ GOOD: Comprehensive godoc
// Package tools provides a registry for AI agent tools.
//
// # Overview
//
// The tools package implements the "Raw In, String Out" pattern:
//   - Tools receive raw JSON from LLM
//   - Tools return strings to LLM
//   - Maximum flexibility, minimal dependencies
//
// # Usage
//
// Basic tool registration:
//
//   registry := tools.NewRegistry()
//   tool := &MyTool{}
//   if err := registry.Register(tool); err != nil {
//       log.Fatal(err)
//   }
//
// Tool execution:
//
//   tool, err := registry.Get("my_tool")
//   if err != nil {
//       return err
//   }
//   result, err := tool.Execute(ctx, argsJSON)
//
// # Thread Safety
//
// The Registry is safe for concurrent use. All operations are protected
// by sync.RWMutex.
//
// # Error Handling
//
// Register() returns an error if the tool definition is invalid.
// Execute() returns errors from the tool implementation.
//
// # Examples
//
// See pkg/tools/std/ for example implementations.
package tools

// Tool represents an AI agent tool.
//
// Tools must implement both Definition() and Execute() methods.
// The Definition() method returns metadata for the LLM.
// The Execute() method contains the business logic.
//
// # Example
//
//   type MyTool struct{}
//
//   func (t *MyTool) Definition() ToolDefinition {
//       return ToolDefinition{
//           Name:        "my_tool",
//           Description: "Does something useful",
//           Parameters: map[string]interface{}{
//               "type": "object",
//               "properties": map[string]interface{}{
//                   "input": map[string]interface{}{
//                       "type": "string",
//                   },
//               },
//               "required": []string{"input"},
//           },
//       }
//   }
//
//   func (t *MyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
//       var args struct {
//           Input string `json:"input"`
//       }
//       json.Unmarshal([]byte(argsJSON), &args)
//       return fmt.Sprintf("Processed: %s", args.Input), nil
//   }
type Tool interface {
    Definition() ToolDefinition
    Execute(ctx context.Context, argsJSON string) (string, error)
}
```

**Documentation Requirements**:
- **Package godoc**: Overview, usage examples, thread safety notes
- **Type godoc**: Purpose, examples, invariants
- **Function godoc**: Parameters, return values, errors, examples
- **Architecture docs**: High-level design decisions (like [`brief.md`](../brief.md))
- **Migration guides**: How to upgrade between versions
- **Examples**: Working code snippets in doc/examples/

**Documentation Structure**:
```
docs/
├── architecture/
│   ├── overview.md
│   ├── tool-system.md
│   └── chain-pattern.md
├── api/
│   ├── agent.md
│   ├── tools.md
│   └── llm.md
├── guides/
│   ├── creating-tools.md
│   ├── adding-llm-providers.md
│   └── deployment.md
└── migration/
    ├── v0-to-v1.md
    └── v1-to-v2.md
```

**Example Documentation**:
```markdown
# Creating a Custom Tool

## Overview

Tools are the primary way to extend Poncho AI functionality.
Each tool implements the `Tool` interface and is registered
with the `Registry`.

## Step 1: Define the Tool

Create a new file in `pkg/tools/std/` or your own package:

```go
package std

import (
    "context"
    "encoding/json"
    "fmt"
    "github.com/ilkoid/poncho-ai/pkg/tools"
)

type MyTool struct{}

func (t *MyTool) Definition() tools.ToolDefinition {
    return tools.ToolDefinition{
        Name:        "my_tool",
        Description: "Does something useful",
        Parameters: map[string]interface{}{
            "type": "object",
            "properties": map[string]interface{}{
                "input": map[string]interface{}{
                    "type": "string",
                },
            },
            "required": []string{"input"},
        },
    }
}

func (t *MyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    var args struct {
        Input string `json:"input"`
    }
    if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
        return "", fmt.Errorf("invalid arguments: %w", err)
    }
    
    result := fmt.Sprintf("Processed: %s", args.Input)
    return result, nil
}
```

## Step 2: Register the Tool

Add registration in `pkg/app/components.go`:

```go
if toolCfg, exists := getToolCfg("my_tool"); exists && toolCfg.Enabled {
    if err := register("my_tool", std.NewMyTool()); err != nil {
        return err
    }
}
```

## Step 3: Add Configuration

Add to `config.yaml`:

```yaml
tools:
  my_tool:
    enabled: true
    description: "Does something useful"
```

## Testing

Use the CLI utility for testing:

```bash
go run cmd/wb-tools-test/main.go
```

Or create a test utility in `cmd/` per Rule 9.
```

---

### Principle 22: Dependency Management

**Principle**: Use Go modules for dependency management. Pin versions for production. Regularly update dependencies. Review security advisories.

**Rationale**: Go projects accumulate dependencies over time. Proper management ensures:
- Reproducible builds
- Security updates
- Compatibility
- Minimal attack surface

**Implementation Guidelines**:

```bash
# ✅ GOOD: Explicit version pinning
require (
    github.com/charmbracelet/bubbletea v1.3.10
    gopkg.in/yaml.v3 v3.0.1
)

# ✅ GOOD: Regular updates
go get -u ./...
go mod tidy

# ✅ GOOD: Security scanning
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./...

# ✅ GOOD: Dependency review
go list -m all
go mod graph
```

**Dependency Management Checklist**:
- [ ] Use semantic versioning for dependencies
- [ ] Pin versions in `go.mod` (no `@latest` in require)
- [ ] Run `go mod tidy` after adding/removing dependencies
- [ ] Review `go.sum` changes
- [ ] Run security scans regularly
- [ ] Update dependencies monthly
- [ ] Review breaking changes before major updates
- [ ] Minimize indirect dependencies

**Dependency Update Process**:
```bash
# 1. Check for updates
go list -u -m all

# 2. Update specific package
go get github.com/pkg/errors@latest

# 3. Update all dependencies
go get -u ./...

# 4. Tidy modules
go mod tidy

# 5. Run tests
go test ./...

# 6. Security scan
govulncheck ./...
```

**Vendor Mode** (if needed):
```bash
# Vendor dependencies for reproducible builds
go mod vendor

# Use vendored dependencies
go build -mod=vendor
```

---

### Principle 23: Build and Deployment

**Principle**: Use consistent build processes. Support multiple platforms. Create reproducible builds. Automate with CI/CD.

**Rationale**: Poncho AI includes multiple CLI utilities. Consistent builds ensure:
- Cross-platform compatibility
- Reproducible deployments
- Easy distribution
- Automated testing

**Implementation Guidelines**:

```bash
# ✅ GOOD: Cross-platform builds
#!/bin/bash
# build.sh

VERSION=$(git describe --tags --always)
LDFLAGS="-X main.Version=${VERSION}"

# Build for multiple platforms
GOOS=linux GOARCH=amd64 go build -ldflags "$LDFLAGS" -o bin/poncho-linux-amd64 cmd/poncho/main.go
GOOS=darwin GOARCH=amd64 go build -ldflags "$LDFLAGS" -o bin/poncho-darwin-amd64 cmd/poncho/main.go
GOOS=windows GOARCH=amd64 go build -ldflags "$LDFLAGS" -o bin/poncho-windows-amd64.exe cmd/poncho/main.go

# ✅ GOOD: Reproducible builds
go build -ldflags="-buildid=" -trimpath" -o poncho cmd/poncho/main.go

# ✅ GOOD: Version embedding
// In main.go
var Version string // Set by -ldflags

func main() {
    if Version == "" {
        Version = "dev"
    }
    log.Printf("Poncho AI v%s", Version)
}
```

**Build Configuration**:
```yaml
# .github/workflows/build.yml
name: Build

on:
  push:
    tags:
      - 'v*'
  pull_request:

jobs:
  build:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        go-version: ['1.25']
        os: [linux, darwin, windows]
        arch: [amd64, arm64]
    
    steps:
      - uses: actions/checkout@v4
      
      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: ${{ matrix.go-version }}
      
      - name: Build
        run: |
          GOOS=${{ matrix.os }} GOARCH=${{ matrix.arch }} go build -v ./...
      
      - name: Test
        run: go test -v ./...
      
      - name: Upload artifacts
        uses: actions/upload-artifact@v4
        with:
          name: poncho-${{ matrix.os }}-${{ matrix.arch }}
          path: poncho
```

**Deployment Checklist**:
- [ ] Build for target platforms (Linux, macOS, Windows)
- [ ] Run tests before release
- [ ] Create git tag for version
- [ ] Generate release notes
- [ ] Upload binaries to release
- [ ] Update documentation
- [ ] Announce changes

**Release Process**:
```bash
# 1. Update version
vim go.mod  # Update version in comments

# 2. Commit changes
git add .
git commit -m "Release v1.0.0"

# 3. Create tag
git tag -a v1.0.0 -m "Release v1.0.0"

# 4. Push
git push origin main
git push origin v1.0.0

# 5. GitHub Actions will build and release
```

---

## Summary of Additional Principles

| # | Principle | Key Points |
|---|-----------|-------------|
| **12** | Context Propagation | Accept and propagate context, handle cancellation |
| **13** | Resource Lifecycle | Use defer, implement io.Closer, manage goroutines |
| **14** | Observability | Structured logging, metrics, request tracing |
| **15** | Security | Environment variables, log redaction, input validation |
| **16** | Performance | Caching, object pooling, profiling before optimizing |
| **17** | Structured Errors | Typed errors, error wrapping, error categories |
| **18** | Concurrency | Goroutines, WaitGroup, worker pools, race detection |
| **19** | Testing | Unit tests, integration tests, benchmarks, mocks |
| **20** | API Stability | Semantic versioning, deprecation process, migration guides |
| **21** | Documentation | Examples, architecture docs, migration guides |
| **22** | Dependency Management | Go modules, version pinning, security scanning |
| **23** | Build and Deployment | Cross-platform builds, CI/CD, reproducible builds |

---

## Integration with Existing Rules

These additional principles complement the existing 11 rules:

- **Principles 12-18** enhance **Rule 7** (Error Handling)
- **Principle 19** extends **Rule 9** (Testing)
- **Principle 20** strengthens **Rule 10** (Documentation)
- **Principles 15-16** support **Rule 2** (Configuration)
- **Principle 18** leverages **Rule 5** (Thread-safe State)

---

## Implementation Priority

### High Priority (Immediate)
1. **Principle 12** (Context Propagation) - Critical for production
2. **Principle 14** (Observability) - Essential for monitoring
3. **Principle 15** (Security) - Non-negotiable for production

### Medium Priority (Near-term)
4. **Principle 17** (Structured Errors) - Improves error handling
5. **Principle 19** (Testing) - Adds automated testing
6. **Principle 21** (Documentation) - Enhances developer experience

### Low Priority (Long-term)
7. **Principle 16** (Performance) - Optimize after profiling
8. **Principle 20** (API Stability) - Prepare for v2.0
9. **Principle 23** (Build and Deployment) - Automate CI/CD

---

## Conclusion

These 12 additional principles (Principles 12-23) provide comprehensive guidance for developing production-ready Go-based AI frameworks. They complement the existing 11 Immutable Rules and address gaps in:

- **Context and cancellation** patterns
- **Resource lifecycle** management
- **Observability** beyond debug logs
- **Security** practices
- **Performance** optimization
- **Structured error** handling
- **Concurrency** patterns
- **Automated testing** strategies
- **API stability** and versioning
- **Documentation** standards
- **Dependency** management
- **Build and deployment** automation

Following these principles will ensure Poncho AI remains maintainable, secure, performant, and production-ready as it evolves.

---

**Next Steps**:
1. Review these recommendations with the team
2. Prioritize principles based on current needs
3. Create implementation plans for high-priority principles
4. Update [`dev_manifest.md`](../dev_manifest.md) with approved principles
5. Create training materials for new principles
6. Implement principles incrementally with code reviews

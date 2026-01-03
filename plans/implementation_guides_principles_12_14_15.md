# Implementation Guides for High-Priority Principles

This document provides detailed implementation guides for the three high-priority principles added to [`dev_manifest.md`](../dev_manifest.md):
- **Principle 12**: Context Propagation and Cancellation
- **Principle 14**: Observability and Structured Logging
- **Principle 15**: Security and Secrets Management

---

## Principle 12: Context Propagation and Cancellation

### Overview

Context propagation is critical for production systems where operations can be long-running (LLM calls, batch processing, API requests). Proper context handling enables:
- Graceful shutdown on timeout
- Cancellation of in-flight operations
- Request-scoped values (tracing, user ID)

### Implementation Checklist

- [ ] All `Tool.Execute()` methods accept and respect `context.Context`
- [ ] LLM provider calls pass context through all layers
- [ ] HTTP clients use context for requests
- [ ] Background goroutines inherit parent context
- [ ] Use `select` statements for context checks in loops
- [ ] Context is used for timeouts and cancellation

### Step-by-Step Implementation

#### Step 1: Update Tool Execute Methods

For each tool in [`pkg/tools/std/`](../pkg/tools/std/), ensure the `Execute` method properly handles context:

```go
// Before (current state):
func (t *MyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    // Context is accepted but not used
    result, err := t.client.DoSomething(argsJSON)
    return result, err
}

// After (proper implementation):
func (t *MyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    // 1. Check for cancellation before starting
    select {
    case <-ctx.Done():
        return "", ctx.Err()
    default:
        // Continue with execution
    }
    
    // 2. Pass context to all downstream calls
    result, err := t.client.DoSomething(ctx, argsJSON)
    if err != nil {
        return "", fmt.Errorf("operation failed: %w", err)
    }
    
    return result, nil
}
```

#### Step 2: Update LLM Provider Calls

Ensure [`pkg/llm/openai/client.go`](../pkg/llm/openai/client.go) passes context through:

```go
// Before:
func (c *Client) Generate(messages []Message, tools []ToolDefinition) (Message, error) {
    // No context - can't be cancelled
    req := c.buildRequest(messages, tools)
    resp, err := c.client.CreateChatCompletion(ctx.Background(), req)
    // ...
}

// After:
func (c *Client) Generate(ctx context.Context, messages []Message, tools []ToolDefinition) (Message, error) {
    // Context is passed through
    req := c.buildRequest(messages, tools)
    resp, err := c.client.CreateChatCompletion(ctx, req)
    // ...
}
```

#### Step 3: Update HTTP Client Calls

For HTTP clients in [`pkg/wb/client.go`](../pkg/wb/client.go):

```go
// Before:
func (c *Client) GetParentCategories(endpoint string) ([]Category, error) {
    // No context - no timeout control
    req, _ := http.NewRequest("GET", endpoint, nil)
    resp, err := c.httpClient.Do(req)
    // ...
}

// After:
func (c *Client) GetParentCategories(ctx context.Context, endpoint string, rateLimit, burst int) ([]Category, error) {
    // Context with timeout
    req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
    resp, err := c.httpClient.Do(req)
    // ...
}
```

#### Step 4: Update Orchestrator

Ensure [`internal/agent/orchestrator.go`](../internal/agent/orchestrator.go) propagates context:

```go
// Current implementation already does this correctly:
func (o *Orchestrator) Run(ctx context.Context, userQuery string) (string, error) {
    // Context is passed to chain.Execute
    input := chain.ChainInput{
        UserQuery: userQuery,
        State:     o.state,
        LLM:       o.llm,
        Registry:  o.registry,
    }
    
    output, err := o.chain.Execute(ctx, input)
    // ...
}
```

#### Step 5: Update Chain Pattern

Ensure [`pkg/chain/react.go`](../pkg/chain/react.go) respects context:

```go
// Check LLMInvocationStep
func (s *LLMInvocationStep) Execute(ctx context.Context, chainCtx *ChainContext) (NextAction, error) {
    // Pass context to LLM
    response, err := s.llm.Generate(ctx, messages, toolDefs...)
    // ...
}

// Check ToolExecutionStep
func (s *ToolExecutionStep) Execute(ctx context.Context, chainCtx *ChainContext) (NextAction, error) {
    // Pass context to tool execution
    for _, tc := range response.ToolCalls {
        result, err := tool.Execute(ctx, cleanArgs)
        // ...
    }
}
```

### Testing Context Propagation

Create tests to verify context cancellation works:

```go
// pkg/tools/std/my_tool_test.go
func TestMyTool_ContextCancellation(t *testing.T) {
    tool := NewMyTool(&MockClient{})
    
    // Create cancellable context
    ctx, cancel := context.WithCancel(context.Background())
    
    // Start execution in goroutine
    resultChan := make(chan string, 1)
    errChan := make(chan error, 1)
    
    go func() {
        result, err := tool.Execute(ctx, "{}")
        resultChan <- result
        errChan <- err
    }()
    
    // Cancel immediately
    cancel()
    
    // Should return error due to cancellation
    select {
    case result := <-resultChan:
        t.Errorf("Expected cancellation error, got result: %v", result)
    case err := <-errChan:
        if err == context.Canceled {
            // Expected
            return
        }
        t.Errorf("Expected context.Canceled, got: %v", err)
    case <-time.After(100 * time.Millisecond):
        t.Error("Timeout waiting for cancellation")
    }
}
```

### Common Patterns

#### Pattern 1: Context with Timeout

```go
func (t *MyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    // Create context with timeout
    ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()
    
    result, err := t.client.DoSomething(ctx, argsJSON)
    return result, err
}
```

#### Pattern 2: Context in Loops

```go
func (t *BatchTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    items := parseItems(argsJSON)
    
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

#### Pattern 3: Context for Goroutines

```go
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

## Principle 14: Observability and Structured Logging

### Overview

Beyond debug logs ([`pkg/debug/`](../pkg/debug/)), production needs real-time monitoring, alerting, and performance metrics. Structured logging with consistent fields enables:
- Request tracing across components
- Performance monitoring
- Error aggregation and alerting
- Debugging in production

### Implementation Checklist

- [ ] Use structured logging with key-value pairs
- [ ] Log at appropriate levels (Debug, Info, Warn, Error)
- [ ] Include request IDs for correlation
- [ ] Include timing information (duration_ms)
- [ ] Add metrics collection for critical operations
- [ ] Use consistent field names across codebase

### Step-by-Step Implementation

#### Step 1: Update Tool Logging

For tools in [`pkg/tools/std/`](../pkg/tools/std/):

```go
// Before (unstructured logging):
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

// After (structured logging):
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
```

#### Step 2: Add Request ID Propagation

Create context helper for request IDs:

```go
// pkg/context/context.go (new file)
package context

import "context"

type requestIDKey struct{}

// WithRequestID adds request ID to context
func WithRequestID(ctx context.Context, requestID string) context.Context {
    return context.WithValue(ctx, requestIDKey{}, requestID)
}

// GetRequestID retrieves request ID from context
func GetRequestID(ctx context.Context) string {
    if id, ok := ctx.Value(requestIDKey{}).(string); ok {
        return id
    }
    return ""
}

// Usage in tools:
func (t *MyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    requestID := context.GetRequestID(ctx)
    if requestID == "" {
        requestID = generateRequestID()
    }
    
    utils.Info("Tool execution started",
        "request_id", requestID,
        "tool", "my_tool",
    )
    // ...
}
```

#### Step 3: Update Orchestrator Logging

Enhance [`internal/agent/orchestrator.go`](../internal/agent/orchestrator.go):

```go
// Current:
func (o *Orchestrator) Run(ctx context.Context, userQuery string) (string, error) {
    utils.Info("=== Orchestrator.Run STARTED ===", "query", userQuery)
    // ...
    utils.Info("=== Orchestrator.Run COMPLETED ===",
        "iterations", output.Iterations,
        "duration_ms", output.Duration.Milliseconds(),
    )
}

// Enhanced with request ID:
func (o *Orchestrator) Run(ctx context.Context, userQuery string) (string, error) {
    requestID := context.GetRequestID(ctx)
    if requestID == "" {
        requestID = generateRequestID()
    }
    
    utils.Info("=== Orchestrator.Run STARTED ===",
        "request_id", requestID,
        "query", userQuery,
    )
    
    // ...
    
    utils.Info("=== Orchestrator.Run COMPLETED ===",
        "request_id", requestID,
        "iterations", output.Iterations,
        "duration_ms", output.Duration.Milliseconds(),
    )
}
```

#### Step 4: Add Metrics Collection

Create metrics package:

```go
// pkg/metrics/metrics.go (new file)
package metrics

import "sync"
import "time"

var (
    mu       sync.RWMutex
    counters = make(map[string]int64)
    timers   = make(map[string][]time.Duration)
)

// Increment increments a counter
func Increment(name string) {
    mu.Lock()
    defer mu.Unlock()
    counters[name]++
}

// RecordDuration records a duration
func RecordDuration(name string, duration time.Duration) {
    mu.Lock()
    defer mu.Unlock()
    timers[name] = append(timers[name], duration)
}

// GetCounters returns all counters
func GetCounters() map[string]int64 {
    mu.RLock()
    defer mu.RUnlock()
    result := make(map[string]int64)
    for k, v := range counters {
        result[k] = v
    }
    return result
}

// Usage in tools:
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

### Log Levels Guide

| Level | When to Use | Examples |
|--------|---------------|----------|
| **Debug** | Detailed execution traces, loop iterations, internal state | "Tool args parsed", "Cache miss", "Iteration 3 started" |
| **Info** | Normal operations, request lifecycle, significant events | "Tool execution started", "Request completed", "User logged in" |
| **Warn** | Recoverable errors, retries, deprecated usage | "API rate limit reached, retrying", "Using deprecated API" |
| **Error** | Failures requiring attention, unrecoverable errors | "Tool execution failed", "Database connection lost", "Authentication failed" |

### Required Log Fields

For production logs, always include:
- `request_id` or `trace_id` - Correlation across components
- `tool` or `component` - Which part of system
- `duration_ms` - Operation timing
- `error` - Error details (in Error logs)
- Context-specific fields: `user_id`, `article_id`, `model`, etc.

---

## Principle 15: Security and Secrets Management

### Overview

AI frameworks handle API keys, tokens, and user data. Security breaches can expose credentials and leak sensitive information. This principle ensures:
- Secrets are never hardcoded
- Secrets are validated before use
- Sensitive data is redacted in logs
- All inputs are validated and sanitized

### Implementation Checklist

- [ ] All secrets in `config.yaml` use `${VAR}` syntax
- [ ] Secrets are never committed to git (in `.gitignore`)
- [ ] Secrets are validated (not default/template values)
- [ ] Sensitive data is redacted in logs
- [ ] All user inputs are validated
- [ ] All user inputs are sanitized (prevent injection)
- [ ] HTTPS is used for all external API calls
- [ ] Rate limiting is implemented to prevent abuse

### Step-by-Step Implementation

#### Step 1: Update Configuration Validation

Enhance [`pkg/config/config.go`](../pkg/config/config.go):

```go
// Add validation methods
func (c *AppConfig) ValidateSecrets() error {
    var errs []error
    
    // Check API keys
    if c.Models.Definitions != nil {
        for name, model := range c.Models.Definitions {
            if err := validateSecret(model.APIKey, name+"_API_KEY"); err != nil {
                errs = append(errs, err)
            }
        }
    }
    
    // Check S3 credentials
    if err := validateSecret(c.S3.AccessKey, "S3_ACCESS_KEY"); err != nil {
        errs = append(errs, err)
    }
    if err := validateSecret(c.S3.SecretKey, "S3_SECRET_KEY"); err != nil {
        errs = append(errs, err)
    }
    
    // Check WB API key
    if err := validateSecret(c.WB.APIKey, "WB_API_KEY"); err != nil {
        errs = append(errs, err)
    }
    
    if len(errs) > 0 {
        return fmt.Errorf("secret validation failed: %v", errs)
    }
    
    return nil
}

func validateSecret(value, name string) error {
    if value == "" {
        return fmt.Errorf("%s environment variable not set", name)
    }
    
    // Check for template value (not expanded)
    if strings.Contains(value, "${") || strings.Contains(value, "$") {
        return fmt.Errorf("%s appears to be a template value (not expanded)", name)
    }
    
    // Validate length/format
    if len(value) < 32 {
        return fmt.Errorf("%s appears invalid (too short, min 32 chars)", name)
    }
    
    return nil
}

// Update Load function to validate secrets
func Load(path string) (*AppConfig, error) {
    // ... existing code ...
    
    // Add secret validation
    if err := cfg.validate(); err != nil {
        return nil, fmt.Errorf("config validation failed: %w", err)
    }
    
    if err := cfg.ValidateSecrets(); err != nil {
        return nil, fmt.Errorf("secret validation failed: %w", err)
    }
    
    return &cfg, nil
}
```

#### Step 2: Add Log Redaction Helpers

Create redaction utilities:

```go
// pkg/redact/redact.go (new file)
package redact

import "strings"

// RedactToken masks sensitive tokens
func RedactToken(token string) string {
    if len(token) <= 8 {
        return "***"
    }
    return token[:4] + "..." + token[len(token)-4:]
}

// RedactEmail masks email addresses
func RedactEmail(email string) string {
    parts := strings.Split(email, "@")
    if len(parts) != 2 {
        return "***@***"
    }
    return parts[0][:2] + "***@" + parts[1]
}

// RedactAPIKey masks API keys
func RedactAPIKey(key string) string {
    if len(key) <= 8 {
        return "***"
    }
    return key[:4] + "..." + key[len(key)-4:]
}

// RedactJSON redacts sensitive fields in JSON
func RedactJSON(data []byte, sensitiveFields []string) []byte {
    var obj map[string]interface{}
    json.Unmarshal(data, &obj)
    
    for _, field := range sensitiveFields {
        if val, ok := obj[field]; ok {
            if str, ok := val.(string); ok {
                obj[field] = RedactAPIKey(str)
            }
        }
    }
    
    result, _ := json.Marshal(obj)
    return result
}
```

#### Step 3: Update Tool Input Validation

For tools in [`pkg/tools/std/`](../pkg/tools/std/):

```go
// Before (no validation):
func (t *MyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    var args struct {
        ArticleID string `json:"article_id"`
    }
    json.Unmarshal([]byte(argsJSON), &args)
    // No validation - vulnerable to injection
    
    return t.process(ctx, args.ArticleID)
}

// After (with validation):
func (t *MyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    var args struct {
        ArticleID string `json:"article_id"`
    }
    
    // Validate JSON
    if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
        return "", fmt.Errorf("invalid arguments JSON: %w", err)
    }
    
    // Validate required fields
    if args.ArticleID == "" {
        return "", errors.New("article_id is required")
    }
    
    // Validate format
    if len(args.ArticleID) > 100 {
        return "", errors.New("article_id too long (max 100 chars)")
    }
    
    // Sanitize input (prevent injection)
    args.ArticleID = strings.TrimSpace(args.ArticleID)
    args.ArticleID = strings.ReplaceAll(args.ArticleID, "\n", "")
    args.ArticleID = strings.ReplaceAll(args.ArticleID, "\r", "")
    
    return t.process(ctx, args.ArticleID)
}
```

#### Step 4: Update Tool Logging with Redaction

For tools handling sensitive data:

```go
// Before (logging raw data):
func (t *MyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    var args struct {
        Token string `json:"token"`
        Data  string `json:"data"`
    }
    json.Unmarshal([]byte(argsJSON), &args)
    
    // Logs sensitive data - BAD
    utils.Info("Processing request",
        "token", args.Token,
        "data", args.Data,
    )
    
    return t.process(ctx, args)
}

// After (logging redacted data):
func (t *MyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    var args struct {
        Token string `json:"token"`
        Data  string `json:"data"`
    }
    json.Unmarshal([]byte(argsJSON), &args)
    
    // Log with redaction
    utils.Info("Processing request",
        "token", redact.RedactToken(args.Token),
        "data_length", len(args.Data),
    )
    
    return t.process(ctx, args)
}
```

#### Step 5: Ensure HTTPS for External APIs

Check [`pkg/wb/client.go`](../pkg/wb/client.go) and [`pkg/llm/openai/client.go`](../pkg/llm/openai/client.go):

```go
// Before (insecure HTTP):
func (c *Client) New(apiKey string) *Client {
    baseURL := "http://api.example.com" // Insecure
    // ...
}

// After (secure HTTPS):
func (c *Client) New(apiKey string) *Client {
    baseURL := "https://api.example.com" // Secure
    if !strings.HasPrefix(baseURL, "https://") {
        log.Printf("Warning: Using insecure HTTP for API: %s", baseURL)
    }
    // ...
}
```

#### Step 6: Add Rate Limiting

Ensure rate limiting is implemented:

```go
// pkg/wb/client.go - verify rate limiting exists
type Client struct {
    apiKey        string
    httpClient    HTTPClient
    retryAttempts int
    
    mu       sync.RWMutex
    limiters map[string]*rate.Limiter // Already exists
}

// Ensure tools use rate limiting
func (t *WbParentCategoriesTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    // Rate limiting is already handled by client
    cats, err := t.client.GetParentCategories(ctx, t.endpoint, t.rateLimit, t.burst)
    // ...
}
```

### Security Checklist

Before deploying to production, verify:

- [ ] All secrets use `${VAR}` syntax in `config.yaml`
- [ ] `.gitignore` includes `config.yaml` and files with secrets
- [ ] No secrets in code (search for hardcoded keys, tokens, passwords)
- [ ] All API calls use HTTPS
- [ ] Rate limiting is implemented
- [ ] Input validation is in place for all tools
- [ ] Log redaction is implemented for sensitive data
- [ ] Secret validation is performed at startup
- [ ] Environment variables are documented in README

### Example Secure Configuration

```yaml
# config.yaml - SECURE EXAMPLE
models:
  default_reasoning: "glm-4.6"
  definitions:
    glm-4.6:
      provider: "zai"
      model_name: "glm-4.6"
      api_key: "${ZAI_API_KEY}"  # ✅ Environment variable
      base_url: "https://api.z.ai/api/paas/v4"  # ✅ HTTPS
      max_tokens: 2000
      temperature: 0.5

s3:
  endpoint: "storage.yandexcloud.net"  # ✅ HTTPS
  region: "ru-central1"
  bucket: "plm-ai"
  access_key: "${S3_ACCESS_KEY}"  # ✅ Environment variable
  secret_key: "${S3_SECRET_KEY}"  # ✅ Environment variable

wb:
  api_key: "${WB_API_KEY}"  # ✅ Environment variable
  base_url: "https://content-api.wildberries.ru"  # ✅ HTTPS
  rate_limit: 100  # ✅ Rate limiting
  burst_limit: 5
```

---

## Integration with Existing Rules

These principles complement existing rules:

- **Principle 12** enhances **Rule 7** (Error Handling) by adding context for graceful failures
- **Principle 14** extends **Rule 9** (Testing) by adding observability for production
- **Principle 15** strengthens **Rule 2** (Configuration) by securing secrets

---

## Next Steps

1. Review implementation guides with team
2. Prioritize which principle to implement first
3. Create pull requests for each principle
4. Update existing code following the guides
5. Add tests for new functionality
6. Monitor production logs to verify improvements

---

## Additional Resources

- Go Context Package: https://pkg.go.dev/context
- Go Log/slog: https://pkg.go.dev/log/slog
- Security Best Practices: https://github.com/OWASP/Go-SCP
- Rate Limiting: https://pkg.go.dev/golang.org/x/time/rate

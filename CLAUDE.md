# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Poncho AI is a Go-based LLM-agnostic, tool-centric framework designed for building AI agents. It provides a structured approach to agent development, handling routine tasks like prompt engineering, JSON validation, and conversation history, while allowing developers to focus on implementing business logic through isolated tools.

## Architecture

The project follows a modular architecture with clear separation of concerns:

- **cmd/** - Entry points for different applications (TUI, CLI utilities, examples)
- **internal/** - Application-specific logic (UI components, global state)
- **pkg/** - Reusable library packages (config, LLM abstraction, tools, S3 client, etc.)

### Core Components

1. **Tool System** (`pkg/tools/`)
   - All functionality is implemented as tools that conform to the `Tool` interface
   - Tools follow the "Raw In, String Out" principle - they receive raw JSON from LLM and return strings
   - The registry pattern allows dynamic tool registration and discovery

2. **LLM Abstraction** (`pkg/llm/`)
   - Provider-agnostic interface for AI models
   - OpenAI-compatible adapter covers 99% of modern APIs
   - Factory pattern for dynamic provider creation

3. **Configuration** (`pkg/config/`)
   - YAML-based configuration with ENV variable support (`${VAR}` syntax)
   - Centralized configuration for all components
   - Type-safe configuration structures

4. **TUI Framework** (Bubble Tea)
   - Model-View-Update architecture for UI components
   - Terminal-first approach for high performance and developer convenience

## Development Commands

### Building Applications

```bash
# Main TUI application
go run cmd/poncho/main.go

# Simple LLM utility
go run cmd/simple-llm-util/main.go

# Tool usage example (good for understanding the framework)
go run cmd/tool-usage-example/main.go

# S3 bucket inspector
go run cmd/list-bucket/main.go
```

### Testing

Currently no test files exist, but the framework is designed for testability:
- Tools accept context for dependency injection
- All HTTP clients are abstracted (no hardcoded dependencies)
- Registry pattern allows easy mocking

## Key Architectural Rules (from dev_manifest.md)

1. **Tool Interface Contract**: Never change the `Tool` interface - it must remain `Definition() ToolDefinition` and `Execute(ctx context.Context, argsJSON string) (string, error)`

2. **Configuration**: All settings must be in YAML with ENV variable support. No hardcoded values in code.

3. **Registry Usage**: All tools must be registered through `Registry.Register()`. No direct tool calls bypassing the registry.

4. **LLM Abstraction**: Work with AI models only through the `Provider` interface. No direct API calls in business logic.

5. **State Management**: Use `GlobalState` with thread-safe access. No global variables.

6. **Package Structure**:
   - `pkg/` - Library code ready for reuse
   - `internal/` - Internal application logic
   - `cmd/` - Entry points and orchestration only

7. **Error Handling**: Return errors up the call stack. No `panic()` in business logic. The framework must be resilient against LLM hallucinations.

8. **Extensibility**: Add new features only through:
   - New tools in `pkg/tools/std/` or custom packages
   - New LLM adapters in `pkg/llm/`
   - Configuration extensions without breaking changes

## Tool Development

When creating new tools:

1. Create a new file in `pkg/tools/std/` or a custom package
2. Implement the `Tool` interface
3. Register the tool in main.go using `registry.Register()`
4. Follow the pattern in existing tools like `wb_catalog.go`

Example tool structure:
```go
type MyTool struct {
    // Dependencies (e.g., HTTP clients)
}

func (t *MyTool) Definition() tools.ToolDefinition {
    return tools.ToolDefinition{
        Name:        "my_tool",
        Description: "What this tool does",
        Parameters: map[string]interface{}{
            "type": "object",
            "properties": map[string]interface{}{
                "param1": map[string]interface{}{
                    "type": "string",
                    "description": "Description of param1",
                },
            },
            "required": []string{"param1"},
        },
    }
}

func (t *MyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    var args struct {
        Param1 string `json:"param1"`
    }
    if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
        return "", fmt.Errorf("invalid arguments: %w", err)
    }

    // Business logic here

    result, err := json.Marshal(result)
    return string(result), err
}
```

## Configuration

The main configuration is in `config.yaml`. It supports:
- Multiple LLM model definitions
- S3 storage settings
- File classification rules
- Tool-specific configurations
- Environment variable substitution

## Working with Prompts

Prompts are stored in `prompts/` directory as YAML files with:
- Config section for model parameters
- Messages section using Go template syntax (`{{.Variable}}`)
- Support for system/user/assistant roles

Example prompt usage:
```go
promptFile, err := prompt.LoadFromFile("prompts/my_prompt.yaml")
if err != nil {
    return err
}

messages, err := promptFile.RenderMessages(data)
if err != nil {
    return err
}
```

## Common Patterns

1. **Agent Loop**: The typical agent execution follows:
   - Build context (history + system prompt with available tools)
   - Call LLM
   - Sanitize response (clean JSON from markdown)
   - Route to tool execution or return text response
   - Add result to conversation history

2. **Error Sanitization**: Always clean LLM responses before parsing:
   ```go
   func cleanJsonBlock(s string) string {
       s = strings.TrimSpace(s)
       s = strings.TrimPrefix(s, "```json")
       s = strings.TrimPrefix(s, "```")
       s = strings.TrimSuffix(s, "```")
       return strings.TrimSpace(s)
   }
   ```

3. **Context Management**: Always pass context to tool executions for timeout/cancellation support.

## Environment Variables Required

- `ZAI_API_KEY` - For ZAI AI provider
- `S3_ACCESS_KEY` - S3 storage access key
- `S3_SECRET_KEY` - S3 storage secret key
- `WB_API_KEY` - Wildberries API key

## Dependencies

Key external dependencies:
- `github.com/charmbracelet/bubbletea` - TUI framework
- `github.com/minio/minio-go/v7` - S3 compatible storage client
- `gopkg.in/yaml.v3` - YAML configuration parsing
- `golang.org/x/time/rate` - Rate limiting for API calls
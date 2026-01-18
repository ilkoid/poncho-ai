# 04. Interruption Mechanism Analysis

## Overview

The interruption mechanism allows users to send messages to the agent during execution. This document analyzes how it works across layers.

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        INTERRUPTION FLOW                                    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  USER LAYER                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  User types "todo: add test task" and presses Enter                 │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    ▼                                        │
│  TUI LAYER (pkg/tui/)                                                      │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  InterruptionModel.handleKeyPressWithInterruption()                 │   │
│  │    ├─ If agent NOT running: launch agent with query                 │   │
│  │    └─ If agent running: send to inputChan                          │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    ▼                                        │
│  CHANNEL LAYER                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  inputChan chan string (buffered, size=10)                          │   │
│  │    User input → channel → ReActCycle check                          │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    ▼                                        │
│  AGENT LAYER (pkg/chain/)                                                  │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  ReActExecutor.Execute()                                            │   │
│  │    For each iteration:                                              │   │
│  │      1. Execute LLM step                                            │   │
│  │      2. Execute Tool step                                           │   │
│  │      3. ⏸️ CHECK INTERRUPTION (between iterations)                   │   │
│  │      4. If input: process interruption                              │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    ▼                                        │
│  INTERRUPTION HANDLER                                                        │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  loadInterruptionPrompt() → YAML or fallback                        │   │
│  │  Add interruption message to history                                │   │
│  │  Emit EventUserInterruption → TUI receives via Subscriber           │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    ▼                                        │
│  TUI EVENT HANDLING                                                        │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  InterruptionModel.handleAgentEventWithInterruption()               │   │
│  │    Display interruption in viewport:                                │   │
│  │      "[🔔 INTERRUPTION at iteration 3]"                              │   │
│  │      "todo: add test task"                                          │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Key Components

### 1. User Input (TUI Layer)

**File**: [pkg/tui/model.go:1134-1267](../pkg/tui/model.go#L1134-L1267)

```go
func (m *InterruptionModel) handleKeyPressWithInterruption(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
    input := m.GetInput()

    // Clear input field
    m.base.textarea.Reset()

    if !m.base.isProcessing {
        // Agent not running - start new execution
        if m.onInput == nil {
            m.base.appendLog(m.base.systemStyle("❌ Error: onInput callback not set"))
            return m, nil
        }
        return m, m.onInput(input)
    }

    // Agent IS running - send interruption
    select {
    case m.inputChan <- input:
        m.base.appendLog(m.base.systemStyle(fmt.Sprintf("📤 Interrupting: %s", input)))
    default:
        m.base.appendLog(m.base.systemStyle("⚠️ Interruption channel full, try again"))
    }

    return m, nil
}
```

### 2. Interruption Check (Agent Layer)

**File**: [pkg/chain/executor.go:262-313](../pkg/chain/executor.go#L262-L313)

```go
// In ReActExecutor.Execute() iteration loop:

// Check for user interruption (between iterations)
select {
case msg := <-exec.UserInputChan:
    // User sent interruption message

    // Load interruption handler prompt
    interruptionPrompt := loadInterruptionPrompt(exec.Config.InterruptionPrompt)

    // Create interruption message
    interruptionMsg := llm.Message{
        Role:    llm.RoleUser,
        Content: fmt.Sprintf("USER INTERRUPTION: %s\n\n%s", msg, interruptionPrompt.content),
    }

    // Append to history
    exec.history = append(exec.history, interruptionMsg)

    // Set active post-prompt for next LLM call
    exec.SetActivePostPrompt(interruptionPrompt.postPrompt)

    // Emit event to TUI
    if exec.emitter != nil {
        exec.emitter.Emit(ctx, events.Event{
            Type: events.EventUserInterruption,
            Data: events.UserInterruptionData{
                Message:      msg,
                Iteration:   iteration,
                PromptSource: interruptionPrompt.source,
            },
            Timestamp: time.Now(),
        })
    }

default:
    // No interruption, continue to next iteration
}
```

### 3. Interruption Prompt Loader

**File**: [pkg/chain/interruption.go](../pkg/chain/interruption.go)

```go
func loadInterruptionPrompt(promptPath string) *interruptionPrompt {
    // Try to load from YAML file
    if promptPath != "" {
        if prompt, err := loadPromptFromYAML(promptPath); err == nil {
            return prompt
        }
    }

    // Fallback to default prompt
    return &interruptionPrompt{
        source:  "default",
        content: defaultInterruptionPrompt,
    }
}

var defaultInterruptionPrompt = `
You are an INTERRUPTION HANDLER for an AI agent.

The user has interrupted your execution with a message.
Handle their request appropriately:

Common interruption patterns:
- "todo: add <task>" → User wants to add a task
- "todo: complete <N>" → User wants to mark task as done
- "stop" → User wants to stop execution
- "What are you doing?" → User wants status update

Respond appropriately to the interruption, then continue your task.
`
```

### 4. Event Emission & Handling

**File**: [pkg/tui/model.go:1044-1131](../pkg/tui/model.go#L1044-L1131)

```go
func (m *InterruptionModel) handleAgentEventWithInterruption(event events.Event) tea.Cmd {
    switch event.Type {
    case events.EventUserInterruption:
        data := event.Data.(events.UserInterruptionData)

        // Display interruption in UI
        m.base.appendLog(
            fmt.Sprintf("\n[🔔 INTERRUPTION at iteration %d]\n%s\n",
                data.Iteration, data.Message),
            m.base.systemStyle,
        )

        m.base.status = fmt.Sprintf("⏸️ Interrupted: %s", data.Message)

        // Store debug path for Ctrl+L
        if data.DebugPath != "" {
            m.mu.Lock()
            m.lastDebugPath = data.DebugPath
            m.mu.Unlock()
        }

    // ... other event types
    }
    return nil
}
```

## Configuration

### YAML Configuration

**File**: [config.yaml](../config.yaml)

```yaml
chains:
  default:
    interruption_prompt: "prompts/interruption_handler.yaml"
    # If empty or file missing, uses default prompt from code
```

### Prompt File

**File**: [prompts/interruption_handler.yaml](../prompts/interruption_handler.yaml)

```yaml
version: "1.0"
description: "Handles user interruptions during ReAct cycle execution"

config:
  temperature: 0.3
  max_tokens: 1500

messages:
  - role: system
    content: |
      You are an INTERRUPTION HANDLER for an AI agent.

      ## TODO Operations (if user mentions "todo" or "plan"):
      - "todo: add <task>" → Call `plan_add_task` tool
      - "todo: complete <N>" → Call `plan_mark_done` tool
      - "todo: list" → Show current tasks

      ## Status Queries:
      - "What are you doing?" → Briefly describe current task
      - "status" → Show iteration number and current step

      ## Control:
      - "stop" → Set SignalNeedUserInput and ask what to do next
      - "continue" → Acknowledge and continue execution

      Respond concisely and continue your original task after handling.
```

## Usage Example

**File**: [cmd/interruption-test/main.go](../cmd/interruption-test/main.go)

```go
func main() {
    // Create agent
    client, _ := agent.New(ctx, agent.Config{ConfigPath: "config.yaml"})

    // Create emitter and subscribe
    emitter := events.NewChanEmitter(100)
    client.SetEmitter(emitter)
    sub := emitter.Subscribe()

    // Create interruption channel
    inputChan := make(chan string, 10)

    // Configure chain with interruption prompt
    chainCfg := tui.DefaultChainConfig()
    chainCfg.InterruptionPrompt = "./prompts/interruption_handler.yaml"

    // Create InterruptionModel
    model := tui.NewInterruptionModel(ctx, client, coreState, sub, inputChan, chainCfg)

    // Set callback for launching agent
    model.SetOnInput(createAgentLauncher(client, chainCfg, inputChan, true))

    // Run TUI
    p := tea.NewProgram(model, tea.WithAltScreen())
    p.Run()
}

func createAgentLauncher(client *agent.Client, chainCfg chain.ChainConfig,
    inputChan chan string, fullLLMLogging bool) func(query string) tea.Cmd {

    return func(query string) tea.Cmd {
        return func() tea.Msg {
            ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
            defer cancel()

            // Execute with interruption support
            output, err := client.Execute(ctx, chain.ChainInput{
                UserQuery:      query,
                State:          client.GetState(),
                Registry:       client.GetToolsRegistry(),
                Config:         chainCfg,
                UserInputChan:  inputChan,  // ← Interruption channel
                FullLLMLogging: fullLLMLogging,
            })

            if err != nil {
                return tui.EventMsg(events.Event{
                    Type: events.EventError,
                    Data: events.ErrorData{Err: err},
                })
            }

            return tui.EventMsg(events.Event{
                Type: events.EventDone,
                Data: events.MessageData{Content: output.Result},
            })
        }
    }
}
```

## Interruption Examples

### Example 1: Add Todo During Execution

```
User: Analyze product data for SKU123

Agent: [Iteration 1] Thinking...
       [Iteration 2] Calling get_wb_product_info...
       [Iteration 3] Analyzing...

User: todo: add verify SKU data

Agent: [🔔 INTERRUPTION at iteration 3]
       todo: add verify SKU data

       [Calls plan_add_task tool]
       Task added: "verify SKU data"

       [Continues original task]
       [Iteration 4] Completing analysis...
```

### Example 2: Status Query

```
User: Show me categories

Agent: [Iteration 1] Thinking...
       [Iteration 2] Calling get_wb_parent_categories...

User: What are you doing?

Agent: [🔔 INTERRUPTION at iteration 2]
       What are you doing?

       I'm currently fetching parent categories from Wildberries API.
       I'm on iteration 2 of the analysis.

       [Continues fetching categories]
```

### Example 3: Stop Execution

```
User: Download all images from S3

Agent: [Iteration 1] Thinking...
       [Iteration 2] Classifying files...
       [Iteration 3] Found 150 images...

User: stop

Agent: [🔔 INTERRUPTION at iteration 3]
       stop

       Execution stopped. What would you like me to do next?
```

## Architecture Issues

### Issue #1: Scattered Logic

Interruption logic is split across:
- `pkg/chain/interruption.go` — Prompt loading
- `pkg/chain/executor.go` — Interruption check
- `pkg/tui/model.go` — Event handling
- `cmd/*/main.go` — Channel creation and callback

### Issue #2: Tight Coupling

`InterruptionModel` requires:
- `*agent.Client` — Concrete type dependency
- `chain.ChainConfig` — Chain-specific configuration
- `inputChan chan string` — Channel management

### Issue #3: Business Logic in TUI

Interruption handling requires TUI to know about:
- Agent execution state
- Chain configuration
- Interruption prompts

This violates Port & Adapter separation.

---

## Summary

| Aspect | Current State | Should Be |
|--------|---------------|-----------|
| Prompt loading | `pkg/chain/interruption.go` | ✅ Correct location |
| Interruption check | `pkg/chain/executor.go` | ✅ Correct location |
| Channel creation | `cmd/*/main.go` | ✅ Correct location |
| Event handling | `pkg/tui/model.go` | ✅ Correct (via Subscriber) |
| Agent dependency | `pkg/tui` imports `pkg/agent` | ❌ Violation |

**Key insight**: The mechanism itself is well-designed, but the coupling between TUI and agent is the issue.

---

**Next**: [05-EVENT-FLOW.md](./05-EVENT-FLOW.md) — Event flow diagrams

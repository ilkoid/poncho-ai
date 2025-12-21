# Todo Commands Usage

This document explains how to use the Todo commands in the Poncho AI TUI interface.

## Available Commands

### Basic Todo Commands

- `todo` - Show current todo list
- `todo add <description>` - Add a new task
- `todo done <id>` - Mark task as completed
- `todo fail <id> <reason>` - Mark task as failed with reason
- `todo clear` - Clear all tasks
- `todo help` - Show help for todo commands

### Shortcuts

- `t` - Shortcut for `todo` command (e.g., `t add <description>`)

## Examples

```bash
# Add a new task
todo add "Analyze dress sketch"

# Mark task as completed
todo done 1

# Mark task as failed
todo fail 2 "File not found"

# Show current todo list
todo

# Clear all tasks
todo clear

# Using shortcuts
t add "Create product card"
t done 1
```

## Integration with TUI

The todo commands are integrated into the Poncho AI TUI interface. You can:

1. Type commands directly in the input field
2. See real-time updates to your todo list
3. Get feedback on command execution
4. View todo status in the UI (when implemented)

## Architecture

The todo system consists of:

- **pkg/todo/manager.go** - Core todo management logic
- **internal/app/commands.go** - Command registry and handlers
- **internal/app/state.go** - Global state integration
- **internal/ui/update.go** - UI integration

## Thread Safety

All todo operations are thread-safe and can be used concurrently with other system operations.

## Error Handling

Commands provide clear error messages for:
- Invalid task IDs
- Missing required arguments
- Task status conflicts (e.g., completing already completed tasks)
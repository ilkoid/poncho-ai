# Simple TUI Example

Минимальный пример использования Poncho AI с готовым TUI интерфейсом.

## Что это показывает

- Как создать AI агент в **2 строки**
- Как запустить готовый TUI в **1 строку**
- Никакого Bubble Tea кода писать не нужно!

## Запуск

```bash
# Из корня проекта:
cd examples/simple-tui
go mod tidy
go run main.go
```

## Использование

1. Запустите программу
2. Введите запрос к AI (например: `привет, как дела?`)
3. Нажмите Enter
4. Смотрите как агент думает и отвечает
5. Нажмите `Ctrl+C` для выхода

## Код

Весь код - это **15 строк**:

```go
package main

import (
    "log"
    "github.com/ilkoid/poncho-ai/pkg/agent"
    "github.com/ilkoid/poncho-ai/pkg/tui"
)

func main() {
    client, _ := agent.New(agent.Config{
        ConfigPath: "../../config.yaml",
    })

    if err := tui.Run(client); err != nil {
        log.Fatal(err)
    }
}
```

## Что дальше?

- **С опциями**: `tui.RunWithOpts(client, tui.WithTitle("My AI"))`
- **Свой UI**: используйте `events.Subscriber` для Web/CLI
- **Документация**: [pkg/agent](../../pkg/agent/) и [pkg/tui](../../pkg/tui/)

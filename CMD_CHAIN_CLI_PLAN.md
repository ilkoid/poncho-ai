# cmd/chain-cli/ — План CLI утилиты для тестирования Chain

## Цель

CLI утилита для тестирования Chain Pattern **без TUI интерфейса**.
Простая командная строка для верификации работы цепочек.

---

## Требования (из dev_manifest.md)

| Правило | Применение |
|---------|------------|
| **Rule 9** | CLI утилита вместо тестов |
| **Rule 11** | Автономность — config.yaml рядом с бинарником |
| **Rule 2** | YAML конфигурация с ENV поддержкой |
| **Rule 10** | Godoc комментарии |

---

## Спецификация CLI

### Использование

```bash
# Базовое использование
./chain-cli "найди все товары в категории Верхняя одежда"

# С указанием chain типа
./chain-cli -chain sequential "какой сегодня день?"

# С указанием config файла
./chain-cli -config /path/to/config.yaml "тестовый запрос"

# С выводом debug информации
./chain-cli -debug "show me trace"

# С указанием модели
./chain-cli -model glm-4.6 "расскажи про Go"

# Версия и помощь
./chain-cli -version
./chain-cli -help
```

### Флаги

| Флаг | Описание | Дефолт |
|------|----------|--------|
| `-chain` | Тип цепочки (`react`, `sequential`) | `react` |
| `-config` | Путь к config.yaml | `./config.yaml` |
| `-model` | Переопределить модель | из config |
| `-debug` | Включить debug логирование | из config |
| `-no-color` | Отключить цвета в выводе | false |
| `-json` | Вывод в JSON формате | false |
| `-version` | Показать версию | - |
| `-help`, `-h` | Показать справку | - |

---

## Архитектура утилиты

### Структура

```
cmd/chain-cli/
├── main.go                 # Точка входа, парсинг аргументов
├── config.go               # Загрузка конфигурации (Rule 11)
├── runner.go               # Выполнение Chain
└── output.go               # Форматирование вывода
```

### main.go — точка входа

```go
// cmd/chain-cli/main.go

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/chain"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/debug"
)

// Version — версия утилиты (заполняется при сборке)
var Version = "dev"

func main() {
	// 1. Парсим флаги
	chainType := flag.String("chain", "react", "Type of chain (react, sequential)")
	configPath := flag.String("config", findConfigPath(), "Path to config.yaml")
	modelName := flag.String("model", "", "Override model name")
	debugFlag := flag.Bool("debug", false, "Enable debug logging")
	noColor := flag.Bool("no-color", false, "Disable colors in output")
	jsonOutput := flag.Bool("json", false, "Output in JSON format")
	showVersion := flag.Bool("version", false, "Show version")
	flag.Parse()

	// 2. Обработка специальных флагов
	if *showVersion {
		fmt.Printf("chain-cli version %s\n", Version)
		os.Exit(0)
	}

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Error: query argument is required")
		fmt.Fprintln(os.Stderr, "Usage: chain-cli [flags] \"query\"")
		fmt.Fprintln(os.Stderr, "Run 'chain-cli -help' for more information")
		os.Exit(1)
	}

	userQuery := flag.Arg(0)

	// 3. Загружаем конфигурацию
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// 4. Создаём компоненты
	llmProvider := createLLMProvider(cfg, *modelName)
	registry := createToolsRegistry(cfg)
	state := createGlobalState(cfg)

	// 5. Загружаем chain из YAML
	chainConfig := cfg.Chains[*chainType]
	reactChain := chain.NewReActChain(chain.ReActChainConfig{
		SystemPrompt:     getSystemPrompt(cfg),
		ReasoningConfig:  getReasoningConfig(cfg),
		ChatConfig:       getChatConfig(cfg),
		ToolPostPrompts:  getToolPostPrompts(cfg),
		PromptsDir:       cfg.App.PromptsDir,
		MaxIterations:    chainConfig.MaxIterations,
	})

	// Настраиваем chain
	reactChain.SetLLM(llmProvider)
	reactChain.SetRegistry(registry)
	reactChain.SetState(state)

	// 6. Подключаем debug если включен
	if *debugFlag || chainConfig.Debug.Enabled {
		recorder, err := createDebugRecorder(chainConfig.Debug)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to create debug recorder: %v\n", err)
		} else {
			reactChain.AttachDebug(recorder)
		}
	}

	// 7. Выполняем chain
	input := chain.ChainInput{
		UserQuery: userQuery,
		State:     state,
		LLM:       llmProvider,
		Registry:  registry,
		Config:    chainConfig,
	}

	ctx, cancel := context.WithTimeout(context.Background(), chainConfig.Timeout)
	defer cancel()

	output, err := reactChain.Execute(ctx, input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// 8. Выводим результат
	if *jsonOutput {
		printJSON(output, *noColor)
	} else {
		printHuman(output, *noColor)
	}

	// 9. Debug лог уже сохранён
	if output.DebugPath != "" {
		fmt.Fprintf(os.Stderr, "Debug log: %s\n", output.DebugPath)
	}
}

// findConfigPath находит config.yaml (Rule 11)
// Сначала ищет рядом с бинарником, затем в текущей директории
func findConfigPath() string {
	// 1. Рядом с бинарником
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		configPath := filepath.Join(exeDir, "config.yaml")
		if _, err := os.Stat(configPath); err == nil {
			return configPath
		}
	}

	// 2. Текущая директория
	if _, err := os.Stat("config.yaml"); err == nil {
		return "config.yaml"
	}

	// 3. Fallback на проект
	return "/Users/ilkoid/dev/go/poncho-ai/config.yaml"
}
```

---

## output.go — форматирование вывода

### Human-readable формат

```
=== Chain Execution ===

Query: найди все товары в категории Верхняя одежда

Iteration 1:
  LLM: glm-4.6 (temp=0.5, tokens=2000)
  → Called: get_wb_parent_categories
  → Duration: 50ms

Iteration 2:
  LLM: glm-4.6 (temp=0.5, tokens=2000)
  → Called: get_wb_subjects
  → Duration: 40ms

Iteration 3:
  LLM: glm-4.6 (temp=0.5, tokens=2000)
  → Final answer
  → Duration: 60ms

=== Result ===
В категории Верхняя одежда найдено 1240 товаров в 12 подкатегориях.

=== Summary ===
Iterations: 3
Duration: 205ms
Debug log: ./debug_logs/debug_20250101_143022.json
```

### JSON формат

```json
{
  "query": "найди все товары в категории Верхняя одежда",
  "result": "В категории Верхняя одежда найдено 1240 товаров...",
  "iterations": 3,
  "duration_ms": 205,
  "debug_log": "./debug_logs/debug_20250101_143022.json",
  "success": true
}
```

---

## config.go — загрузка конфигурации (Rule 11)

```go
// cmd/chain-cli/config.go

package main

import (
	"os"
	"path/filepath"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

// findConfigPath находит config.yaml с учетом Rule 11.
// Приоритет:
// 1. Рядом с бинарником (exe_dir/config.yaml)
// 2. Текущая директория
// 3. Fallback на проект
func findConfigPath() string {
	// 1. Рядом с бинарником
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		configPath := filepath.Join(exeDir, "config.yaml")
		if _, err := os.Stat(configPath); err == nil {
			return configPath
		}
	}

	// 2. Текущая директория
	if _, err := os.Stat("config.yaml"); err == nil {
		return "config.yaml"
	}

	// 3. Fallback на проект
	return "/Users/ilkoid/dev/go/poncho-ai/config.yaml"
}
```

---

## runner.go — выполнение Chain

```go
// cmd/chain-cli/runner.go

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/chain"
)

// Runner выполняет Chain и возвращает результат.
type Runner struct {
	chain      chain.Chain
	debug      *debug.Recorder
	startTime  time.Time
}

// NewRunner создает новый Runner.
func NewRunner(c chain.Chain, debug *debug.Recorder) *Runner {
	return &Runner{
		chain: c,
		debug: debug,
	}
}

// Run выполняет цепочку с замером времени.
func (r *Runner) Run(ctx context.Context, input chain.ChainInput) (chain.ChainOutput, error) {
	r.startTime = time.Now()

	// Start debug если включен
	if r.debug != nil {
		r.debug.Start(input.UserQuery)
	}

	// Execute
	output, err := r.chain.Execute(ctx, input)
	if err != nil {
		// Finalize debug с ошибкой
		if r.debug != nil {
			r.debug.Finalize("", time.Since(r.startTime))
		}
		return chain.ChainOutput{}, err
	}

	// Finalize debug успешно
	if r.debug != nil {
		debugPath, _ := r.debug.Finalize(output.Result, time.Since(r.startTime))
		output.DebugPath = debugPath
	}

	return output, nil
}
```

---

## Примеры использования

### 1. Базовый запрос

```bash
$ ./chain-cli "покажи родительские категории"

=== Chain Execution ===

Query: покажи родительские категории

Iteration 1:
  LLM: glm-4.6 (temp=0.5, tokens=2000)
  → Called: get_wb_parent_categories
  → Duration: 350ms

Iteration 2:
  LLM: glm-4.6 (temp=0.5, tokens=2000)
  → Final answer
  → Duration: 180ms

=== Result ===
Вот список родительских категорий Wildberries:
...

=== Summary ===
Iterations: 2
Duration: 530ms
```

### 2. Debug режим

```bash
$ ./chain-cli -debug "найди товары"

=== Chain Execution ===
...

=== Summary ===
Iterations: 3
Duration: 1200ms
Debug log: ./debug_logs/debug_20250101_143022.json

$ cat ./debug_logs/debug_20250101_143022.json | jq .
{
  "run_id": "debug_20250101_143022",
  "user_query": "найди товары",
  ...
}
```

### 3. JSON вывод

```bash
$ ./chain-cli -json "привет" | jq .

{
  "query": "привет",
  "result": "Привет! Я AI-ассистент Poncho...",
  "iterations": 1,
  "duration_ms": 150,
  "debug_log": "",
  "success": true
}
```

---

## План реализации CLI утилиты

| Файл | Описание | Ожидаемый размер |
|------|----------|------------------|
| `main.go` | Парсинг аргументов, создание компонентов | ~150 строк |
| `config.go` | Поиск config.yaml (Rule 11) | ~30 строк |
| `runner.go` | Выполнение Chain с замером времени | ~60 строк |
| `output.go` | Форматирование вывода (human/JSON) | ~100 строк |

**Итого: ~340 строк**

---

## Зависимости от Chain реализации

CLI утилита требует:

| Компонент | Статус |
|-----------|--------|
| `chain.Chain` interface | ⏳ Будет в `pkg/chain/chain.go` |
| `chain.ReActChain` | ⏳ Будет в `pkg/chain/react.go` |
| `chain.ChainInput/Output` | ⏳ Будет в `pkg/chain/chain.go` |
| `debug.Recorder` | ✅ Уже реализован |
| `config.Load` | ✅ Уже реализован |

---

## Проверка перед запуском

```bash
# 1. Собрать CLI утилиту
go build -o chain-cli cmd/chain-cli/*.go

# 2. Проверить версию
./chain-cli -version
# Ожидается: chain-cli version dev

# 3. Проверить помощь
./chain-cli -help

# 4. Тестовый запуск (без реального LLM)
./chain-cli -config ./testdata/minimal-config.yaml "тест"

# 5. Полный тест
./chain-cli "какой сегодня день?"
```

---

## TestData для тестирования

```
cmd/chain-cli/testdata/
├── minimal-config.yaml     # Минимальная конфигурация
└── mock-chain.yaml          # Конфигурация с mock LLM
```

---

## Вопросы

1. **Нужны ли цвета в выводе?** (через `lipgloss` или без)
2. **Добавить ли verbose режим?** (подробный лог каждого шага)
3. **Поддерживать ли интерактивный режим?** (без TUI, просто readline)

---

## Решение

**Рекомендуемый план:**

1. ✅ Создать базовую структуру (`main.go`, `config.go`, `runner.go`, `output.go`)
2. ✅ Реализовать human-readable вывод
3. ✅ Добавить JSON вывод
4. ✅ Добавить colors (опционально)
5. ❌ НЕ делать интерактивный режим (оставить для TUI)

Начать реализацию CLI утилиты после базовой Chain?

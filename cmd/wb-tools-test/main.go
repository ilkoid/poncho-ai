// wb-tools-test — CLI утилита для тестирования Wildberries tools.
//
// Работает в интерактивном режиме: выбираешь tool → вводишь args → видишь результат.
// Соблюдает Правило 11: config.yaml ищется рядом с бинарником.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	appcomp "github.com/ilkoid/poncho-ai/pkg/app"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// ToolInfo описывает tool для меню
type ToolInfo struct {
	Name        string
	Description string
	NeedsArgs   bool
	ExampleArgs string
}

var menuTools = []ToolInfo{
	// Поиск товаров
	{"search_wb_products", "Поиск по артикулам (supplierArticle -> nmID)", true, `{"supplierArticles": ["ABC-123"]}`},

	// Категории и предметы
	{"get_wb_parent_categories", "Родительские категории WB", false, ""},
	{"get_wb_subjects", "Предметы по parentID", true, `{"parentID": 1234}`},
	{"get_wb_subjects_by_name", "Поиск предмета по имени", true, `{"name": "платье", "limit": 10}`},

	// Характеристики
	{"get_wb_characteristics", "Характеристики предмета", true, `{"subjectID": 105}`},
	{"get_wb_tnved", "Коды ТНВЭД для предмета", true, `{"subjectID": 105}`},
	{"get_wb_brands", "Бренды для предмета", true, `{"subjectID": 105}`},

	// Справочники
	{"get_wb_colors", "Поиск цветов (fuzzy search)", true, `{"search": "красный", "top": 10}`},
	{"get_wb_countries", "Справочник стран", false, ""},
	{"get_wb_genders", "Справочник полов", false, ""},
	{"get_wb_seasons", "Справочник сезонов", false, ""},
	{"get_wb_vat_rates", "Справочник НДС", false, ""},

	// Диагностика
	{"ping_wb_api", "Проверка доступности WB API", false, ""},
}

func main() {
	utils.Info("WB Tools Test Utility", "version", "1.0")

	// 1. Загружаем конфиг (ищет config.yaml рядом с бинарником)
	cfg, cfgPath, err := appcomp.InitializeConfig(&appcomp.DefaultConfigPathFinder{})
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	fmt.Printf("Config loaded from: %s\n", cfgPath)

	// 2. Валидируем WB API ключ
	if err := appcomp.ValidateWBKey(cfg.WB.APIKey); err != nil {
		log.Fatalf("WB API key validation failed: %v", err)
	}

	// 3. Инициализируем компоненты
	// Правило 11: передаём контекст для распространения отмены
	components, err := appcomp.Initialize(context.Background(), cfg, 10, "")
	if err != nil {
		log.Fatalf("Failed to initialize components: %v", err)
	}
	fmt.Printf("Components initialized successfully\n\n")

	// 4. Запускаем интерактивное меню
	runMenu(components)
}

// runMenu — главный цикл меню
func runMenu(c *appcomp.Components) {
	reader := bufio.NewReader(os.Stdin)

	for {
		printMenu()
		fmt.Print("Выбери инструмент (число или 'q' для выхода): ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		if input == "q" || input == "quit" || input == "exit" {
			fmt.Println("До свидания!")
			return
		}

		choice, err := strconv.Atoi(input)
		if err != nil || choice < 1 || choice > len(menuTools) {
			fmt.Println("Неверный выбор. Попробуй ещё раз.")
			continue
		}

		// Выполняем выбранный tool
		executeTool(c, reader, choice-1)
	}
}

// printMenu выводит меню инструментов
func printMenu() {
	fmt.Println("==========================================")
	fmt.Println("   WB Tools Test Utility")
	fmt.Println("==========================================")

	for i, tool := range menuTools {
		fmt.Printf("%2d. %-25s %s\n", i+1, tool.Name, tool.Description)
	}

	fmt.Println("\n  q. Выход")
	fmt.Println("==========================================")
}

// executeTool выполняет выбранный инструмент
func executeTool(c *appcomp.Components, reader *bufio.Reader, index int) {
	toolInfo := menuTools[index]
	registry := c.State.GetToolsRegistry()

	// Получаем tool из реестра
	tool, err := registry.Get(toolInfo.Name)
	if err != nil {
		fmt.Printf("Ошибка: tool '%s' не найден в реестре: %v\n\n", toolInfo.Name, err)
		return
	}

	fmt.Printf("\n--- Tool: %s ---\n", toolInfo.Name)
	fmt.Printf("Описание: %s\n", toolInfo.Description)

	// Получаем аргументы если нужны
	argsJSON := "{}"
	if toolInfo.NeedsArgs {
		fmt.Printf("Пример аргументов: %s\n", toolInfo.ExampleArgs)
		fmt.Print("Введи JSON аргументы (или Enter для '{}'): ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		if input != "" {
			// Проверяем что это валидный JSON
			if !json.Valid([]byte(input)) {
				fmt.Printf("Невалидный JSON, использую '{}'\n")
			} else {
				argsJSON = input
			}
		}
	}

	// Выводим что выполняем
	fmt.Printf("\nВыполняю: %s(%s)\n", toolInfo.Name, argsJSON)

	// Выполняем tool с таймаутом
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	result, err := tool.Execute(ctx, argsJSON)
	duration := time.Since(start)

	if err != nil {
		fmt.Printf("ОШИБКА: %v\n", err)
	} else {
		// Пытаемся форматировать JSON для читаемости
		var formatted interface{}
		if json.Unmarshal([]byte(result), &formatted) == nil {
			pretty, _ := json.MarshalIndent(formatted, "", "  ")
			fmt.Printf("\nРезультат (%dms, %d bytes):\n%s\n",
				duration.Milliseconds(), len(result), string(pretty))
		} else {
			// Не JSON - выводим как есть
			fmt.Printf("\nРезультат (%dms):\n%s\n", duration.Milliseconds(), result)
		}
	}

	fmt.Println("\nНажми Enter чтобы продолжить...")
	reader.ReadString('\n')
	fmt.Println()
}

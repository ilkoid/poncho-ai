// Package main provides WB Analytics Funnel Demo
//
// Это автономная утилита для верификации интеграции с WB Analytics API v3.
// Демонстрирует получение расширенных метрик воронки продаж:
// - просмотры → корзина → заказ → выкуп/отмена
// - финансовые метрики (суммы заказов, средний чек)
// - WB Club метрики (отдельная статистика для подписчиков)
// - остатки на складах WB и продавца
// - рейтинги товара и отзывов
// - время готовности к отправке
// - локализация карточки
//
// Usage:
//
//	cd examples/wb-funnel-demo
//	go run main.go                                    # Mock режим (demo_key)
//	WB_API_KEY=real_key go run main.go                # Реальный API
//	WB_API_KEY=real_key go run main.go --nmIds 123456 # Конкретные товары
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/tools/std"
	"github.com/ilkoid/poncho-ai/pkg/wb"
	"gopkg.in/yaml.v3"
)

// Config представляет структуру конфигурационного файла.
type Config struct {
	WB struct {
		APIKey string `yaml:"api_key"`
	} `yaml:"wb"`
	Tools struct {
		GetWbProductFunnel struct {
			Enabled     bool   `yaml:"enabled"`
			Description string `yaml:"description"`
			Endpoint    string `yaml:"endpoint"`
			RateLimit   int    `yaml:"rate_limit"`
			Burst       int    `yaml:"burst"`
		} `yaml:"get_wb_product_funnel"`
	} `yaml:"tools"`
}

// ToolArgs представляет аргументы для вызова инструмента.
type ToolArgs struct {
	NmIDs []int `json:"nmIDs"`
	Days  int    `json:"days"`
}

func main() {
	// Парсим аргументы командной строки
	nmIDs := []int{123456, 234567} // значения по умолчанию
	days := 7

	if len(os.Args) > 1 {
		for i, arg := range os.Args[1:] {
			if arg == "--nmIds" && i+2 < len(os.Args) {
				// Парсим список nmID через запятую
				nmIDs = parseNmIDs(os.Args[i+2])
			} else if arg == "--days" && i+2 < len(os.Args) {
				days, _ = strconv.Atoi(os.Args[i+2])
			}
		}
	}

	// Загружаем конфигурацию
	cfg, err := loadConfig("config.yaml")
	if err != nil {
		log.Fatalf("❌ Failed to load config: %v", err)
	}

	// Получаем API ключ из переменной окружения или из конфига
	apiKey := os.Getenv("WB_API_KEY")
	if apiKey == "" {
		apiKey = cfg.WB.APIKey
	}
	if apiKey == "" {
		apiKey = "demo_key" // fallback для тестирования
	}

	fmt.Println("=== WB Analytics Funnel Demo (API v3) ===")
	fmt.Printf("📦 Товары: %v\n", nmIDs)
	fmt.Printf("📅 Период: %d дней\n", days)
	fmt.Printf("🔑 API Key: %s\n\n", maskAPIKey(apiKey))

	// Создаём WB клиент
	client := wb.New(apiKey)

	// Создаём конфигурацию для WB инструмента
	wbCfg := config.WBConfig{
		APIKey:     apiKey,
		RateLimit:  cfg.Tools.GetWbProductFunnel.RateLimit,
		BurstLimit: cfg.Tools.GetWbProductFunnel.Burst,
	}

	// Создаём инструмент
	toolCfg := config.ToolConfig{
		Description: cfg.Tools.GetWbProductFunnel.Description,
	}

	tool := std.NewWbProductFunnelTool(client, toolCfg, wbCfg)

	// Формируем аргументы
	args := ToolArgs{
		NmIDs: nmIDs,
		Days:  days,
	}
	argsJSON, _ := json.Marshal(args)

	// Выполняем запрос
	fmt.Println("⏳ Выполняю запрос к WB API...")
	ctx := context.Background()

	result, err := tool.Execute(ctx, string(argsJSON))
	if err != nil {
		log.Fatalf("❌ Tool execution failed: %v", err)
	}

	// Парсим результат для красивого вывода
	var products []map[string]interface{}
	if err := json.Unmarshal([]byte(result), &products); err != nil {
		fmt.Printf("⚠️  Could not parse result, showing raw:\n%s\n", result)
		return
	}

	// Выводим результаты
	printResults(products)

	fmt.Println("\n✅ Готово!")
}

// loadConfig загружает конфигурацию из YAML файла.
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return &cfg, nil
}

// parseNmIDs парсит список nmID из строки через запятую.
func parseNmIDs(s string) []int {
	var result []int
	for _, part := range splitAndTrim(s, ",") {
		if id, err := strconv.Atoi(part); err == nil {
			result = append(result, id)
		}
	}
	return result
}

// splitAndTrim разбивает строку по разделителю и обрезает пробелы.
func splitAndTrim(s, sep string) []string {
	parts := make([]string, 0)
	for _, part := range splitString(s, sep) {
		trimmed := trimSpace(part)
		if trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return parts
}

// splitString разбивает строку по разделителю.
func splitString(s, sep string) []string {
	if s == "" {
		return []string{}
	}
	if sep == "" {
		return []string{s}
	}

	result := make([]string, 0)
	start := 0

	for i := 0; i <= len(s)-len(sep); i++ {
		if s[i:i+len(sep)] == sep {
			result = append(result, s[start:i])
			start = i + len(sep)
			i += len(sep) - 1
		}
	}
	result = append(result, s[start:])

	return result
}

// trimSpace обрезает пробелы с обеих сторон строки.
func trimSpace(s string) string {
	start := 0
	end := len(s)

	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}

	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}

	return s[start:end]
}

// maskAPIKey скрывает часть API ключа для безопасности.
func maskAPIKey(key string) string {
	if len(key) <= 8 {
		return "***"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// printResults выводит результаты в красивом формате.
func printResults(products []map[string]interface{}) {
	fmt.Println("\n📊 РЕЗУЛЬТАТЫ:")
	fmt.Println("=" + repeat("=", 70))

	for i, p := range products {
		product, ok := p["product"].(map[string]interface{})
		if !ok {
			continue
		}

		statistic, ok := p["statistic"].(map[string]interface{})
		if !ok {
			continue
		}

		selected, ok := statistic["selected"].(map[string]interface{})
		if !ok {
			continue
		}

		fmt.Printf("\n🛍️  ТОВАР #%d\n", i+1)
		fmt.Printf("  nmID:        %v\n", product["nmId"])
		fmt.Printf("  Название:    %v\n", product["title"])
		fmt.Printf("  Бренд:       %v\n", product["brandName"])

		// Основные метрики
		fmt.Printf("\n  📈 Воронка продаж:\n")
		fmt.Printf("    Просмотры:        %v\n", selected["openCount"])
		fmt.Printf("    В корзину:        %v\n", selected["cartCount"])
		fmt.Printf("    Заказы:           %v\n", selected["orderCount"])
		fmt.Printf("    Выкупы:           %v\n", selected["buyoutCount"])
		fmt.Printf("    Отмены:           %v\n", selected["cancelCount"])

		// Конверсии
		if conversions, ok := selected["conversions"].(map[string]interface{}); ok {
			fmt.Printf("\n  📊 Конверсии:\n")
			fmt.Printf("    В корзину:        %.1f%%\n", conversions["addToCartPercent"])
			fmt.Printf("    В заказ:          %.1f%%\n", conversions["cartToOrderPercent"])
			fmt.Printf("    Выкупаемость:     %.1f%%\n", conversions["buyoutPercent"])
		}

		// Финансы
		fmt.Printf("\n  💰 Финансы:\n")
		fmt.Printf("    Сумма заказов:     %v\n", selected["orderSum"])
		fmt.Printf("    Сумма выкупов:     %v\n", selected["buyoutSum"])
		fmt.Printf("    Средний чек:       %v\n", selected["avgPrice"])

		// WB Club
		if wbClub, ok := selected["wbClub"].(map[string]interface{}); ok {
			fmt.Printf("\n  🎯 WB Club:\n")
			fmt.Printf("    Заказы:           %v\n", wbClub["orderCount"])
			fmt.Printf("    Выкупаемость:     %.1f%%\n", wbClub["buyoutPercent"])
		}

		// Остатки
		if stocks, ok := product["stocks"].(map[string]interface{}); ok {
			fmt.Printf("\n  📦 Остатки:\n")
			fmt.Printf("    Склад WB:         %v\n", stocks["wb"])
			fmt.Printf("    Склад продавца:   %v\n", stocks["mp"])
		}

		// Рейтинги
		if rating, ok := product["productRating"].(float64); ok && rating > 0 {
			fmt.Printf("\n  ⭐ Рейтинги:\n")
			fmt.Printf("    Товара:           %.1f\n", rating)
			if feedback, ok := product["feedbackRating"].(float64); ok && feedback > 0 {
				fmt.Printf("    Отзывов:          %.1f\n", feedback)
			}
		}

		// Время готовности
		if timeToReady, ok := selected["timeToReady"].(map[string]interface{}); ok {
			fmt.Printf("\n  ⏱️  Время готовности:\n")
			fmt.Printf("    %v дней %v часов %v минут\n",
				timeToReady["days"], timeToReady["hours"], timeToReady["mins"])
		}
	}

	fmt.Println("\n" + repeat("=", 71))

	// Проверка на mock режим
	if len(products) > 0 {
		if _, ok := products[0]["mock"]; ok {
			fmt.Println("\n⚠️  Режим: MOCK DATA (используется demo_key)")
			fmt.Println("    Для реальных данных установите WB_API_KEY")
		}
	}
}

// repeat повторяет строку n раз.
func repeat(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}

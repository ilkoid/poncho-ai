// Package main provides WB Seller Products List Utility
//
// Утилита для получения списка товаров продавца с фильтрацией по предмету.
// Использует Content API (категория Promotion) - видит только товары продавца.
//
// Usage:
//
//	cd examples/wb-list-products
//	go run main.go                                    # Все товары
//	WB_API_KEY=real_key go run main.go               # Реальный API
//	WB_API_KEY=real_key go run main.go --search комбинезон  # Фильтр по предмету
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

func main() {
	// Парсим аргументы
	searchTerm := ""
	if len(os.Args) > 1 {
		for i, arg := range os.Args[1:] {
			if arg == "--search" && i+2 < len(os.Args) {
				searchTerm = os.Args[i+2]
			}
		}
	}

	// Получаем API ключ - пробуем несколько вариантов
	apiKey := os.Getenv("WB_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("WB_API_CONTENT_KEY") // Content API ключ
	}
	if apiKey == "" {
		apiKey = os.Getenv("WB_API_ANALYTICS_AND_PROMO_KEY") // Analytics API ключ
	}
	if apiKey == "" {
		apiKey = "demo_key"
	}

	fmt.Println("=== WB Seller Products List ===")
	fmt.Printf("🔑 API Key: %s\n", maskAPIKey(apiKey))
	if searchTerm != "" {
		fmt.Printf("🔍 Фильтр: %s\n\n", searchTerm)
	} else {
		fmt.Println("📋 Показываем все товары\n")
	}

	// Создаём клиент
	client := wb.New(apiKey)

	// Mock режим
	if client.IsDemoKey() {
		fmt.Println("⚠️  Режим: MOCK DATA (demo_key)")
		fmt.Println("    Установите WB_API_KEY для получения реальных товаров")
		fmt.Println("\n📊 MOCK РЕЗУЛЬТАТЫ:")
		printMockProducts(searchTerm)
		return
	}

	// Получаем все карточки используя существующий client.Post()
	ctx := context.Background()
	endpoint := "https://content-api.wildberries.ru"

	reqBody := map[string]interface{}{
		"settings": map[string]interface{}{
			"cursor": map[string]interface{}{
				"limit": 100,
			},
		},
	}

	var resp struct {
		Cards []struct {
			NmID        int    `json:"nmID"`
			SubjectID   int    `json:"subjectID"`
			SubjectName string `json:"subjectName"`
			VendorCode  string `json:"vendorCode"`
			Brand       string `json:"brand"`
			Title       string `json:"title"`
		} `json:"cards"`
		Error bool `json:"error"`
	}

	err := client.Post(ctx, "list_products", endpoint, 100, 5, "/content/v2/get/cards/list", reqBody, &resp)
	if err != nil {
		log.Fatalf("❌ Failed to get cards: %v", err)
	}

	// Фильтруем по предмету
	filtered := filterBySubject(resp.Cards, searchTerm)

	// Выводим результаты
	printProducts(filtered, searchTerm)

	// Если есть товары, предлагаем команды для теста
	if len(filtered) > 0 {
		printTestCommands(filtered)
	}
}

func filterBySubject(cards []struct {
	NmID        int    `json:"nmID"`
	SubjectID   int    `json:"subjectID"`
	SubjectName string `json:"subjectName"`
	VendorCode  string `json:"vendorCode"`
	Brand       string `json:"brand"`
	Title       string `json:"title"`
}, searchTerm string) []struct {
	NmID        int    `json:"nmID"`
	SubjectID   int    `json:"subjectID"`
	SubjectName string `json:"subjectName"`
	VendorCode  string `json:"vendorCode"`
	Brand       string `json:"brand"`
	Title       string `json:"title"`
} {
	if searchTerm == "" {
		return cards
	}

	var result []struct {
		NmID        int    `json:"nmID"`
		SubjectID   int    `json:"subjectID"`
		SubjectName string `json:"subjectName"`
		VendorCode  string `json:"vendorCode"`
		Brand       string `json:"brand"`
		Title       string `json:"title"`
	}
	searchLower := strings.ToLower(searchTerm)

	for _, card := range cards {
		if strings.Contains(strings.ToLower(card.SubjectName), searchLower) {
			result = append(result, card)
		}
	}

	return result
}

func printProducts(cards []struct {
	NmID        int    `json:"nmID"`
	SubjectID   int    `json:"subjectID"`
	SubjectName string `json:"subjectName"`
	VendorCode  string `json:"vendorCode"`
	Brand       string `json:"brand"`
	Title       string `json:"title"`
}, searchTerm string) {
	fmt.Printf("\n📊 НАЙДЕНО ТОВАРОВ: %d\n\n", len(cards))

	if len(cards) == 0 {
		if searchTerm != "" {
			fmt.Printf("❌ Товары с предметом '%s' не найдены\n", searchTerm)
			fmt.Println("   Попробуйте другой поиск или без фильтра")
		} else {
			fmt.Println("❌ У продавца нет товаров")
		}
		return
	}

	fmt.Println("=" + repeat("=", 80))

	for i, card := range cards {
		fmt.Printf("\n🛍️  ТОВАР #%d\n", i+1)
		fmt.Printf("  nmID:          %d\n", card.NmID)
		fmt.Printf("  Артикул:       %s\n", card.VendorCode)
		fmt.Printf("  Название:      %s\n", card.Title)
		fmt.Printf("  Бренд:         %s\n", card.Brand)
		fmt.Printf("  Предмет:       %s\n", card.SubjectName)
		fmt.Printf("  ID предмета:   %d\n", card.SubjectID)
	}

	fmt.Println("\n" + repeat("=", 81))
}

func printTestCommands(cards []struct {
	NmID        int    `json:"nmID"`
	SubjectID   int    `json:"subjectID"`
	SubjectName string `json:"subjectName"`
	VendorCode  string `json:"vendorCode"`
	Brand       string `json:"brand"`
	Title       string `json:"title"`
}) {
	// Берем первые 3 для примера
	count := 3
	if len(cards) < 3 {
		count = len(cards)
	}

	fmt.Println("\n🔧 КОМАНДЫ ДЛЯ ТЕСТА ВОРОНКИ:")
	fmt.Println("=" + repeat("=", 70))

	// Собираем nmID
	nmIDs := make([]string, count)
	for i := 0; i < count; i++ {
		nmIDs[i] = fmt.Sprintf("%d", cards[i].NmID)
	}

	nmIDsStr := strings.Join(nmIDs, ",")

	fmt.Printf("\n# Тест первых %d товаров:\n", count)
	fmt.Printf("cd ../wb-funnel-demo && WB_API_KEY=$WB_API_KEY go run main.go --nmIds %s --days 7\n", nmIDsStr)

	fmt.Printf("\n# Тест конкретного товара (%s):\n", cards[0].VendorCode)
	fmt.Printf("cd ../wb-funnel-demo && WB_API_KEY=$WB_API_KEY go run main.go --nmIds %d --days 7\n", cards[0].NmID)

	fmt.Println("\n" + repeat("=", 71))
}

func printMockProducts(searchTerm string) {
	mockCards := []struct {
		NmID        int
		VendorCode  string
		Title       string
		Brand       string
		SubjectName string
	}{
		{123456, "ART001", "Детский комбинезон зимний", "BabyBrand", "Комбинезоны"},
		{234567, "ART002", "Комбинезон демисезонный", "KidsWear", "Комбинезоны"},
		{345678, "ART003", "Платье летнее", "FashionStyle", "Платья"},
	}

	filtered := mockCards
	if searchTerm != "" {
		filtered = []struct {
			NmID        int
			VendorCode  string
			Title       string
			Brand       string
			SubjectName string
		}{}
		for _, card := range mockCards {
			if strings.Contains(strings.ToLower(card.SubjectName), strings.ToLower(searchTerm)) {
				filtered = append(filtered, card)
			}
		}
	}

	for i, card := range filtered {
		fmt.Printf("\n🛍️  ТОВАР #%d\n", i+1)
		fmt.Printf("  nmID:          %d\n", card.NmID)
		fmt.Printf("  Артикул:       %s\n", card.VendorCode)
		fmt.Printf("  Название:      %s\n", card.Title)
		fmt.Printf("  Бренд:         %s\n", card.Brand)
		fmt.Printf("  Предмет:       %s\n", card.SubjectName)
	}
}

func maskAPIKey(key string) string {
	if len(key) <= 8 {
		return "***"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

func repeat(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}

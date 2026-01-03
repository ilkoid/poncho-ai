// Package debug предоставляет утилиты для отладки и тестирования.
//
// Этот пакет содержит тестовые данные (fixtures) и вспомогательные функции
// для тестирования JSON Debug Logs без необходимости запускать полноценный orchestrator.
//
// Rule 6: Тестовые данные вынесены из entry point в отдельный пакет.
package debug

import (
	"encoding/json"
)

// GenerateCategoriesJSON генерирует тестовые данные родительских категорий Wildberries.
//
// Возвращает JSON-строку с примером структуры категорий, используемую
// для тестирования debug-recorder без реальных API вызовов.
//
// Rule 6: Тестовые данные отделены от бизнес-логики entry point.
func GenerateCategoriesJSON() string {
	categories := map[string]interface{}{
		"categories": []map[string]interface{}{
			{"id": 1541, "name": "Верхняя одежда", "parent_id": 0},
			{"id": 1542, "name": "Брюки", "parent_id": 0},
			{"id": 1543, "name": "Обувь", "parent_id": 0},
		},
	}
	data, _ := json.Marshal(categories)
	return string(data)
}

// GenerateSubjectsJSON генерирует тестовые данные подкатегорий (subjects) Wildberries.
//
// Возвращает JSON-строку с примером структуры подкатегорий, используемую
// для тестирования debug-recorder без реальных API вызовов.
//
// Rule 6: Тестовые данные отделены от бизнес-логики entry point.
func GenerateSubjectsJSON() string {
	subjects := map[string]interface{}{
		"subjects": []map[string]interface{}{
			{"id": 1001, "name": "Куртки", "parent_id": 1541, "count": 450},
			{"id": 1002, "name": "Пуховики", "parent_id": 1541, "count": 320},
			{"id": 1003, "name": "Пальто", "parent_id": 1541, "count": 280},
			{"id": 1004, "name": "Кардиганы", "parent_id": 1541, "count": 190},
		},
		"total": 1240,
	}
	data, _ := json.Marshal(subjects)
	return string(data)
}

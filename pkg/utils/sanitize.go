// Package utils предоставляет утилиты для санитайза и оптимизации данных.
package utils

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SanitizePLMJson убирает лишние поля из PLM JSON для экономии токенов.
//
// Удаляет:
//   - Блок "Ответственные" (служебная информация)
//   - Блок "Эскизы" (загружаются отдельно)
//   - Поля с "НомерСтроки" (технические)
//   - Поля со "Статус" (служебные)
//   - Поля с "ИдентификаторСтроки" (технические)
//   - **ВАЖНО**: Поля "Миниатюра", "Миниатюра_Файл" (огромные base64 данные)
//   - Поля "НавигационнаяСсылкаМиниатюра", "ФайлКартинки", "ВидЭскизаФайл" (ссылки на файлы)
//   - Пустые строки и пустые значения
//   - Технические поля: "ДобавленАвтоматически", "ДатаВыгрузкиВУТ", "ВыгружатьВУТ",
//     "АртикулПрототипа", "Прототип", "ВидПереноса", "СостояниеСогласования",
//     "СезоннаяКоллекцияПервогоПроизводства", "ТНВЭД", "НаименованиеПоКлассификатору",
//     "ПодлежитМаркировкеЧЗ", "ВнешнееОформление", "ИдентификаторСтрокиКонструкции"
//
// Сохраняет только полезную информацию для описания товара:
//   - Артикул, Наименование, Категория, Сезон
//   - Цвета, Материалы, РазмерныйРяд
//   - РекомендацииПоУходу, Комментарий
func SanitizePLMJson(jsonStr string) (string, error) {
	// Парсим JSON
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return "", fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Поля для полного удаления (целые блоки)
	blocksToRemove := []string{
		"Ответственные",
		"Эскизы",
		"ДополнительныеРеквизиты",
		"ДополнительныеРеквизитыКомплектующих",
		"ДополнительныеРеквизитыМаркетинг",
	}

	// Поля для удаления (на всех уровнях вложенности)
	// **ВАЖНО**: "Миниатюра_Файл" содержит огромный base64 массив - его нужно удалять в первую очередь
	fieldsToRemove := []string{
		"НомерСтроки",
		"ИдентификаторСтроки",
		"Статус",
		"Миниатюра",
		"Миниатюра_Файл",      // ВАЖНО: enormous base64 data
		"НавигационнаяСсылкаМиниатюра",
		"ФайлКартинки",
		"ВидЭскизаФайл",
		"ДобавленАвтоматически",
		"ДатаВыгрузкиВУТ",
		"ВыгружатьВУТ",
		"АртикулПрототипа",
		"Прототип",
		"ВидПереноса",
		"СостояниеСогласования",
		"СезоннаяКоллекцияПервогоПроизводства",
		"ТНВЭД",
		"НаименованиеПоКлассификатору",
		"ПодлежитМаркировкеЧЗ",
		"ВнешнееОформление",
		"ИдентификаторСтрокиКонструкции",
		"Код", // Технический код, не нужен для описания
	}

	// Удаляем целые блоки
	for _, block := range blocksToRemove {
		delete(data, block)
	}

	// Рекурсивно очищаем ВСЕ данные от ненужных полей и пустых значений
	// Это гарантирует удаление "Миниатюра_Файл" на любом уровне вложенности
	data = cleanData(data, fieldsToRemove)

	// Конвертируем обратно в JSON с отступами для читаемости
	result, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal sanitized JSON: %w", err)
	}

	return string(result), nil
}

// cleanData рекурсивно очищает данные от технических полей и пустых значений
func cleanData(data map[string]interface{}, fieldsToRemove []string) map[string]interface{} {
	result := make(map[string]interface{})

	for key, value := range data {
		// Пропускаем поля для удаления
		if shouldRemove(key, fieldsToRemove) {
			continue
		}

		// Рекурсивно обрабатываем вложенные map и слайсы
		switch v := value.(type) {
		case map[string]interface{}:
			cleaned := cleanData(v, fieldsToRemove)
			if len(cleaned) > 0 {
				result[key] = cleaned
			}
		case []interface{}:
			cleaned := cleanSlice(v, fieldsToRemove)
			if len(cleaned) > 0 {
				result[key] = cleaned
			}
		case string:
			// Пропускаем пустые строки
			if strings.TrimSpace(v) != "" {
				result[key] = v
			}
		case nil:
			// Пропускаем nil значения
		default:
			result[key] = v
		}
	}

	return result
}

// cleanSlice очищает слайсы от технических полей и пустых значений
func cleanSlice(slice []interface{}, fieldsToRemove []string) []interface{} {
	result := make([]interface{}, 0, len(slice))

	for _, item := range slice {
		switch v := item.(type) {
		case map[string]interface{}:
			cleaned := cleanData(v, fieldsToRemove)
			if len(cleaned) > 0 {
				result = append(result, cleaned)
			}
		case []interface{}:
			cleaned := cleanSlice(v, fieldsToRemove)
			if len(cleaned) > 0 {
				result = append(result, cleaned)
			}
		case string:
			// Пропускаем пустые строки
			if strings.TrimSpace(v) != "" {
				result = append(result, v)
			}
		case nil:
			// Пропускаем nil значения
		default:
			result = append(result, v)
		}
	}

	return result
}

// shouldRemove проверяет, нужно ли удалить поле
func shouldRemove(key string, fieldsToRemove []string) bool {
	for _, field := range fieldsToRemove {
		if key == field || strings.Contains(key, field) {
			return true
		}
	}
	return false
}

// removeEmptyValues удаляет пустые значения из map
func removeEmptyValues(m map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})

	for key, value := range m {
		switch v := value.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				result[key] = v
			}
		case nil:
			// Пропускаем
		case []interface{}:
			if len(v) > 0 {
				result[key] = v
			}
		case map[string]interface{}:
			if len(v) > 0 {
				result[key] = v
			}
		default:
			result[key] = v
		}
	}

	return result
}

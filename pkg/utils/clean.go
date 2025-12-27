// Package utils предоставляет вспомогательные функции для обработки данных.
//
// Включает утилиты для очистки ответов LLM от markdown-обёртки,
// санитизации JSON и других форматирующих операций.
package utils

import (
	"strings"
)

// CleanJsonBlock удаляет markdown-обёртку вокруг JSON.
//
// LLM часто возвращает JSON обёрнутым в markdown кодовые блоки:
//   ```json
//   {"key": "value"}
//   ```
//
// Эта функция очищает такие обёртки, возвращая чистый JSON.
//
// Примеры:
//   ```json {"a": 1} ``` → {"a": 1}
//   `{"a": 1}` → {"a": 1}
//   ``` {"a": 1} ``` → {"a": 1}
func CleanJsonBlock(s string) string {
	s = strings.TrimSpace(s)

	// Удаляем ```json в начале
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```JSON")
	s = strings.TrimPrefix(s, "```Json")

	// Удаляем ``` в начале
	s = strings.TrimPrefix(s, "```")

	// Удаляем ``` в конце
	s = strings.TrimSuffix(s, "```")

	return strings.TrimSpace(s)
}

// CleanMarkdownCode удаляет все markdown code blocks из текста.
//
// В отличие от CleanJsonBlock, эта функция работает с полным текстом,
// содержащим несколько code blocks, и удаляет их все, оставляя только
// обычный текст.
//
// Примеры:
//   "Пример:\n```json\n{"a": 1}\n```\nКонец" → "Пример:\nКонец"
func CleanMarkdownCode(s string) string {
	lines := strings.Split(s, "\n")
	var result []string

	inCodeBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Проверяем начало/конец code block
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			continue
		}

		// Добавляем строку только если не внутри code block
		if !inCodeBlock {
			result = append(result, line)
		}
	}

	return strings.Join(result, "\n")
}

// TrimCommonPrefixes удаляет общие префиксы из строк текста.
//
// Полезно для очистки цитат или numbered lists от лишних пробелов
// и символов, которые LLM может добавить при форматировании.
func TrimCommonPrefixes(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) == 0 {
		return s
	}

	// Находим общий префикс (пробелы, табы, маркировка, нумерация)
	var commonPrefix string
	for i := 0; i < len(lines[0]); i++ {
		c := lines[0][i]
		// Префикс может содержать: пробелы, табы, '-', '*', '.', цифры
		if c != ' ' && c != '\t' && c != '-' && c != '*' && c != '.' &&
			(c < '0' || c > '9') {
			break
		}

		isCommon := true
		for _, line := range lines {
			if i >= len(line) || line[i] != c {
				isCommon = false
				break
			}
		}

		if !isCommon {
			break
		}
		commonPrefix += string(c)
	}

	// Удаляем общий префикс из всех строк
	result := make([]string, len(lines))
	for i, line := range lines {
		if strings.HasPrefix(line, commonPrefix) {
			result[i] = strings.TrimPrefix(line, commonPrefix)
			// Дополнительно удаляем оставшиеся пробелы после префикса (например, "1. " → "1.")
			result[i] = strings.TrimLeft(result[i], " \t")
		} else {
			result[i] = line
		}
	}

	return strings.Join(result, "\n")
}

// SanitizeLLMOutput выполняет комплексную очистку вывода LLM.
//
// Применяет несколько шагов очистки:
// 1. Удаляет markdown code blocks
// 2. Удаляет лишние пробелы в начале/конце строк
// 3. Удаляет пустые строки
// 4. Нормализует переносы строк
//
// Используется как финальный шаг перед отображением ответа пользователю.
func SanitizeLLMOutput(s string) string {
	// 1. Удаляем markdown code blocks
	s = CleanMarkdownCode(s)

	// 2. Разбиваем на строки и обрезаем пробелы
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}

	// 3. Удаляем пустые строки (включая середину)
	var nonEmpty []string
	for _, line := range lines {
		if line != "" {
			nonEmpty = append(nonEmpty, line)
		}
	}

	// 4. Собираем результат
	return strings.Join(nonEmpty, "\n")
}

// ExtractJSON пытается извлечь JSON объект из строки.
//
// LLM часто возвращает JSON вместе с пояснительным текстом.
// Эта функция находит первый валидный JSON-объект в тексте.
//
// Возвращает пустую строку если JSON-объект не найден.
//
// ВНИМАНИЕ: Не валидирует JSON, только извлекает его по эвристикам.
// Для валидации используйте json.Unmarshal().
func ExtractJSON(s string) string {
	// Ищем первый { и последний }
	start := strings.Index(s, "{")
	if start == -1 {
		return ""
	}

	// Проверяем что это не массив (пропускаем [{)
	if start > 0 && s[start-1] == '[' {
		// Это элемент массива, не извлекаем
		return ""
	}

	// Ищем соответствующую закрывающую скобку
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}

	return s[start:]
}

// SplitChunks разбивает текст на части по разделителю.
//
// Полезно для обработки многострочных ответов LLM,
// где разные части разделены пустыми строками или разделителями.
//
// Примеры:
//   SplitChunks("a\n\nb\n\nc", "\n\n") → ["a", "b", "c"]
//   SplitChunks("a---b---c", "---") → ["a", "b", "c"]
func SplitChunks(s string, separator string) []string {
	if separator == "" {
		return []string{s}
	}

	chunks := strings.Split(s, separator)
	result := make([]string, 0, len(chunks))

	for _, chunk := range chunks {
		trimmed := strings.TrimSpace(chunk)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}

	return result
}

// WrapText переносит текст по словам с учетом заданной ширины.
//
// Сохраняет существующие переносы строк и не разрывает слова.
// Если ширина меньше 1, возвращает исходный текст без изменений.
//
// Параметры:
//   s - исходный текст
//   width - максимальная ширина строки в символах
//
// Примеры:
//   WrapText("hello world", 5) → "hello\nworld"
//   WrapText("a\nb\nc", 10) → "a\nb\nc" (сохраняет переносы)
func WrapText(s string, width int) string {
	if width < 1 {
		return s
	}

	// Разбиваем на исходные строки
	lines := strings.Split(s, "\n")
	var result []string

	for _, line := range lines {
		// Пропускаем пустые строки
		if strings.TrimSpace(line) == "" {
			result = append(result, "")
			continue
		}

		// Разбиваем строку на слова
		words := strings.Fields(line)

		if len(words) == 0 {
			result = append(result, "")
			continue
		}

		currentLine := words[0]
		for _, word := range words[1:] {
			// Проверяем, поместится ли следующее слово
			testLine := currentLine + " " + word
			if len(testLine) <= width {
				currentLine = testLine
			} else {
				// Слово не помещается - переносим строку
				result = append(result, currentLine)
				currentLine = word
			}
		}
		result = append(result, currentLine)
	}

	return strings.Join(result, "\n")
}

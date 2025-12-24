//go:build short

/* 
Для реализации нечеткого поиска (Fuzzy Search) по названиям цветов в Go подойдет библиотека, вычисляющая расстояние Левенштейна или использующая триграммы. Для простоты  github.com/lithammer/fuzzysearch или простая реализация на базе стандартной библиотеки (если не хотим лишних зависимостей).

Учитывая, что справочник цветов не такой уж огромный (не миллионы, а тысячи), простой перебор с ранжированием по сходству будет работать мгновенно.

Как это интегрировать в Flow?
Загрузка при старте:
В main.go или при первом обращении к тулу загружаем цвета:

go
colors, err := wbClient.GetColors(ctx)
colorService := wb.NewColorService(colors)
Использование в Tool:
Когда LLM просит найти цвет, мы вызываем colorService.FindTopMatches("персиковый", 5).
Это вернет:

"персиковый"

"персиковый джем"

"персиковый мелок"

И этот короткий список мы отдаем LLM, чтобы она выбрала лучший вариант.
*/
package wb

import (
    "sort"
    "strings"
    _ "unicode"
)

// SearchMatch - результат поиска
type SearchMatch struct {
    Color Color
    Score float64 // Чем больше, тем лучше (0.0 - 1.0)
}

// ColorService - обертка над списком цветов для поиска
type ColorService struct {
    colors []Color
}

func NewColorService(colors []Color) *ColorService {
    return &ColorService{colors: colors}
}

// FindTopMatches ищет топ-N похожих цветов
func (s *ColorService) FindTopMatches(query string, topN int) []Color {
    // TODO: Normalize query string (lowercase, trim)
    // TODO: Iterate through all colors and calculate match scores
    // TODO: Check for exact match (highest priority)
    // TODO: Check for substring matches
    // TODO: Check for reverse substring matches
    // TODO: Optionally implement Levenshtein distance for typos
    // TODO: Sort matches by score descending
    // TODO: Return top-N matches
    return nil
}
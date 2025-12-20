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
    query = strings.ToLower(strings.TrimSpace(query))
    var matches []SearchMatch

    for _, c := range s.colors {
        target := strings.ToLower(c.Name)
        
        // 1. Точное совпадение - высший приоритет
        if target == query {
            matches = append(matches, SearchMatch{Color: c, Score: 1.0})
            continue
        }

        // 2. Вхождение (substring)
        if strings.Contains(target, query) {
            // Штраф за лишние символы: len(query) / len(target)
            // Если ищем "красный", то "темно-красный" получит меньший скор, чем "красный"
            score := float64(len(query)) / float64(len(target)) * 0.9 
            matches = append(matches, SearchMatch{Color: c, Score: score})
            continue
        }

        // 3. Обратное вхождение (если запрос длиннее: "ярко-красный" ищем "красный")
        if strings.Contains(query, target) {
            score := float64(len(target)) / float64(len(query)) * 0.8
            matches = append(matches, SearchMatch{Color: c, Score: score})
            continue
        }
        
        // 4. (Опционально) Расстояние Левенштейна для опечаток
        // Можно добавить, если нужно, но для цветов обычно хватает substring
    }

    // Сортируем по убыванию Score
    sort.Slice(matches, func(i, j int) bool {
        return matches[i].Score > matches[j].Score
    })

    // Берем топ-N
    result := make([]Color, 0, topN)
    for i := 0; i < len(matches) && i < topN; i++ {
        result = append(result, matches[i].Color)
    }

    return result
}

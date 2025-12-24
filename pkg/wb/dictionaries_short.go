//go:build short

package wb

import (
	"context"
	_ "fmt"
)

// Dictionaries - контейнер для всех справочников
type Dictionaries struct {
    Colors  []Color
    Genders []string
	Countries []Country
    Seasons []string
	Vats    []string // <--- Добавили НДС
}

// LoadDictionaries загружает все необходимые справочники параллельно
func (c *Client) LoadDictionaries(ctx context.Context) (*Dictionaries, error) {
    // TODO: Load all dictionaries in parallel using errgroup.Group
    // TODO: Load colors from API
    // TODO: Load genders from API
    // TODO: Load seasons from API
    // TODO: Load VAT rates from API
    // TODO: Load countries from API
    // TODO: Return consolidated dictionaries or error
    return nil, nil
}

/* 
===
Использование в main.go
// ... внутри main
fmt.Print("📚 Loading WB dictionaries... ")
dicts, err := wbClient.LoadDictionaries(context.Background())
if err != nil {
    log.Fatal(err)
}
// Сохраняем в State
state.Dictionaries = dicts 
fmt.Printf("OK (%d colors, %d genders)\n", len(dicts.Colors), len(dicts.Genders))
===
Это решит проблему "разрозненных сущностей". Все справочные данные будут лежать в одном месте state.Dictionaries и будут доступны для Tools и LLM.

Пример Tool для пола:
LLM: "Пол: для мальчика"
Tool match_gender: Ищет "для мальчика" в state.Dictionaries.Genders. Находит "Детский" (если он там есть) или возвращает список доступных: ["Мужской", "Женский", "Детский", "Унисекс"].
*/

// ================
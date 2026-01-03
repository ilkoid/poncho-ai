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

// LoadDictionaries загружает все необходимые справочники.
// Параметры baseURL, rateLimit, burst передаются из конфигурации.
func (c *Client) LoadDictionaries(ctx context.Context, baseURL string, rateLimit int, burst int) (*Dictionaries, error) {
	colors, err := c.GetColors(ctx, baseURL, rateLimit, burst)
	if err != nil {
		return nil, err
	}

	genders, err := c.GetGenders(ctx, baseURL, rateLimit, burst)
	if err != nil {
		return nil, err
	}

	seasons, err := c.GetSeasons(ctx, baseURL, rateLimit, burst)
	if err != nil {
		return nil, err
	}

	vats, err := c.GetVats(ctx, baseURL, rateLimit, burst)
	if err != nil {
		return nil, err
	}

	countries, err := c.GetCountries(ctx, baseURL, rateLimit, burst)
	if err != nil {
		return nil, err
	}

	return &Dictionaries{
		Colors:    colors,
		Genders:   genders,
		Seasons:   seasons,
		Vats:      vats,
		Countries: countries,
	}, nil
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

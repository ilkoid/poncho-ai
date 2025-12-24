//go:build short

// Бизнес-логика методов
package wb

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// GetParentCategories возвращает список родительских категорий
func (c *Client) GetParentCategories(ctx context.Context) ([]ParentCategory, error) {
	// TODO: Call API to get parent categories
	// TODO: Check for API errors in response
	// TODO: Return category data or error
	return nil, nil
}

// GetSubjects возвращает список предметов (подкатегорий).
// Можно фильтровать по parentID, name и т.д. (см. доку).
// Для простоты пока без фильтров или с опциональными.

// FetchSubjectsPage - низкоуровневый запрос одной страницы + для GetAllSubjectsLazy
func (c *Client) FetchSubjectsPage(ctx context.Context, parentID, limit, offset int) ([]Subject, error) {
	// TODO: Build query parameters for pagination
	// TODO: Call API to get subjects page
	// TODO: Check for API errors in response
	// TODO: Return subject data or error
	return nil, nil
}

// GetAllSubjects2 - "ленивый" метод, который выкачивает всё используя FetchSubjectsPage. Основной метод и для Tools в том числе
func (c *Client) GetAllSubjectsLazy(ctx context.Context, parentID int) ([]Subject, error) {
	// TODO: Implement pagination loop to get all subjects
	// TODO: Use FetchSubjectsPage for each batch
	// TODO: Continue until fewer results than limit
	// TODO: Return all collected subjects
	return nil, nil
}

// GetCharacteristics получает хар-ки для предмета
func (c *Client) GetCharacteristics(ctx context.Context, subjectID int) ([]Characteristic, error) {
	// TODO: Build API path for subject characteristics
	// TODO: Call API to get characteristics
	// TODO: Check for API errors in response
	// TODO: Return characteristic data or error
	return nil, nil
}

/* добавляем цвет wb. URL: /content/v2/directory/colors. Это справочник ("directory"), а не объект.
Внимание! 
Использование в AI-агенте (Tool для LLM)
Это классический кейс для RAG (Retrieval Augmented Generation).
Список цветов может быть на 5000+ строк. Мы не можем запихнуть его весь в контекст LLM.

Стратегия:

При старте приложения (или раз в сутки) скачиваем GetColors() и кэшируем в памяти (в GlobalState).

Когда нужно определить цвет товара, мы используем Fuzzy Search (нечеткий поиск) внутри Go, а не спрашиваем LLM "выбери из 5000 вариантов".

Пример сценария:

LLM проанализировала эскиз: "Цвет платья: светло-персиковый".

Мы (Go-код) ищем в справочнике colors что-то похожее на "светло-персиковый".

Находим: "персиковый", "персиковый мелок", "светло-персиковый".

Отдаем LLM эти 3 варианта: "Выбери точный цвет WB из: [...]".

LLM выбирает "персиковый мелок".

Иметь в виду, что использовать его надо с кэшированием, а не дергать каждый раз.
*/

// GetColors возвращает справочник всех допустимых цветов WB
func (c *Client) GetColors(ctx context.Context) ([]Color, error) {
	// TODO: Call API to get all colors
	// TODO: Check for API errors in response
	// TODO: Return color data or error
	return nil, nil
}

// Метод GetGenders GetGenders (в API называется "Kinds") возвращает справочник полов/видов.
// Пример: "Мужской", "Женский", "Детский"
func (c *Client) GetGenders(ctx context.Context) ([]string, error) {
	// TODO: Call API to get genders/kinds
	// TODO: Check for API errors in response
	// TODO: Return gender data or error
	return nil, nil
}

// GetSeasons возвращает справочник сезонов.
func (c *Client) GetSeasons(ctx context.Context) ([]string, error) {
	// TODO: Call API to get seasons
	// TODO: Check for API errors in response
	// TODO: Return season data or error
	return nil, nil
}

// pkg/wb/types.go
type Tnved struct {
    Tnved string `json:"tnved"` // Код (строка, т.к. может начинаться с 0)
    IsKiz bool   `json:"isKiz"` // Требует ли маркировки КИЗ
}

// GetTnved возвращает список кодов ТНВЭД для конкретного предмета
func (c *Client) GetTnved(ctx context.Context, subjectID int, search string) ([]Tnved, error) {
	// TODO: Build query parameters for subject ID and search
	// TODO: Call API to get TNVED codes
	// TODO: Check for API errors in response
	// TODO: Return TNVED data or error
	return nil, nil
}

/* 
Сценарий использования GetTnved (Flow)
Вот как это будет выглядеть в диалоге с агентом:

Пользователь: "Заведи карточку на шелковую блузку".
LLM: (Анализ...) "Блузка" -> это SubjectID 123 (нашла через поиск предметов).
LLM: "Мне нужно выбрать код ТНВЭД для блузки. Вызываю get_tnved(subjectID=123)".
Tool: Возвращает список:
6206100000 (из шелка)
6206200000 (из шерсти)
...
LLM: "Ага, раз блузка шелковая, беру код 6206100000".
Это подтверждает, что ТНВЭД должен быть инструментом (Tool), а не частью предзагруженного словаря.
=============================
*/

// GetVats возвращает список ставок НДС. Пример: ["22%", "Без НДС", "10%"]
func (c *Client) GetVats(ctx context.Context) ([]string, error) {
	// TODO: Build query parameters for locale
	// TODO: Call API to get VAT rates
	// TODO: Check for API errors in response
	// TODO: Return VAT data or error
	return nil, nil
}

// GetCountries возвращает список стран производства.
func (c *Client) GetCountries(ctx context.Context) ([]Country, error) {
	// TODO: Build query parameters for locale
	// TODO: Call API to get countries
	// TODO: Check for API errors in response
	// TODO: Return country data or error
	return nil, nil
}

/* 
Резюме по справочникам
Мы собрали фулл-хаус статических справочников:
Цвета (Colors) -> Номенклатура (nmID)
Пол (Genders) -> Обязательное поле карточки
Страна (Countries) -> Обязательное поле
Сезон (Seasons) -> Обязательное поле (часто)
НДС (Vats) -> Финансы
Динамический: ТНВЭД (по запросу).

Теперь у нас есть всё, чтобы AI-агент мог "собрать" JSON карточки товара, опираясь на реальные, валидные значения WB, а не галлюцинируя "Страна: Поднебесная" или "Сезон: Дождливый".
*/
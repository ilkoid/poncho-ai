// Бизнес-логика методов
package wb

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// GetParentCategories возвращает список родительских категорий.
// Параметры baseURL, rateLimit, burst передаются из tool config.
func (c *Client) GetParentCategories(ctx context.Context, baseURL string, rateLimit int, burst int) ([]ParentCategory, error) {
	var resp APIResponse[[]ParentCategory]

	err := c.Get(ctx, "get_wb_parent_categories", baseURL, rateLimit, burst, "/content/v2/object/parent/all", nil, &resp)
	if err != nil {
		return nil, err
	}

	if resp.Error {
		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
	}

	return resp.Data, nil
}

// GetSubjects возвращает список предметов (подкатегорий).
// Можно фильтровать по parentID, name и т.д. (см. доку).
// Для простоты пока без фильтров или с опциональными.
// func (c *Client) GetSubjects(ctx context.Context, parentID int) ([]Subject, error) {
// 	params := url.Values{}
// 	if parentID > 0 {
// 		params.Set("parentID", fmt.Sprintf("%d", parentID))
// 	}
	
// 	// Лимит WB может отдавать много данных, возможно нужна пагинация (offset/limit)
// 	// Но в API /object/all пагинация делается через top/limit? 
// 	// В доке написано: "limit: int, offset: int". 
// 	// Давай добавим дефолтные лимиты, чтобы не качать всё
// 	params.Set("limit", "1000") 

// 	var resp APIResponse[[]Subject]
	
// 	err := c.get(ctx, "/content/v2/object/all", params, &resp)
// 	if err != nil {
// 		return nil, err
// 	}

// 	if resp.Error {
// 		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
// 	}

// 	return resp.Data, nil
// }

// // GetAllSubjects выкачивает ВСЕ предметы, автоматически листая страницы. deprecated?
// func (c *Client) GetAllSubjects(ctx context.Context, parentID int) ([]Subject, error) {
//     var allSubjects []Subject
//     limit := 1000
//     offset := 0

//     for {
//         params := url.Values{}
//         params.Set("limit", strconv.Itoa(limit))
//         params.Set("offset", strconv.Itoa(offset))
//         if parentID > 0 {
//             params.Set("parentID", strconv.Itoa(parentID))
//         }

//         var resp APIResponse[[]Subject]
//         // Наш умный .get() сам подождет лимиты
//         err := c.get(ctx, "/content/v2/object/all", params, &resp)
//         if err != nil {
//             return nil, err
//         }
//         if resp.Error {
//             return nil, fmt.Errorf("wb error: %s", resp.ErrorText)
//         }

//         // Добавляем полученное
//         allSubjects = append(allSubjects, resp.Data...)

//         // Если вернулось меньше лимита, значит это последняя страница
//         if len(resp.Data) < limit {
//             break
//         }

//         // Готовимся к следующей странице
//         offset += limit
//     }

//     return allSubjects, nil
// }

// FetchSubjectsPage - низкоуровневый запрос одной страницы предметов.
// Параметры baseURL, rateLimit, burst передаются из tool config.
func (c *Client) FetchSubjectsPage(ctx context.Context, baseURL string, rateLimit int, burst int, parentID, limit, offset int) ([]Subject, error) {
	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))
	params.Set("offset", strconv.Itoa(offset))
	if parentID > 0 {
		params.Set("parentID", strconv.Itoa(parentID))
	}

	var resp APIResponse[[]Subject]
	err := c.Get(ctx, "get_wb_subjects", baseURL, rateLimit, burst, "/content/v2/object/all", params, &resp)
	if err != nil {
		return nil, err
	}
	if resp.Error {
		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
	}
	return resp.Data, nil
}

// GetAllSubjectsLazy - "ленивый" метод, который выкачивает всё используя FetchSubjectsPage.
// Параметры baseURL, rateLimit, burst передаются из tool config.
func (c *Client) GetAllSubjectsLazy(ctx context.Context, baseURL string, rateLimit int, burst int, parentID int) ([]Subject, error) {
	var all []Subject
	limit := 1000
	offset := 0

	for {
		batch, err := c.FetchSubjectsPage(ctx, baseURL, rateLimit, burst, parentID, limit, offset)
		if err != nil {
			return nil, err
		}

		all = append(all, batch...)

		if len(batch) < limit {
			break
		}
		offset += limit
	}
	return all, nil
}

// GetCharacteristics получает характеристики для предмета.
// Параметры baseURL, rateLimit, burst передаются из tool config.
func (c *Client) GetCharacteristics(ctx context.Context, baseURL string, rateLimit int, burst int, subjectID int) ([]Characteristic, error) {
	path := fmt.Sprintf("/content/v2/object/charcs/%d", subjectID)

	var resp APIResponse[[]Characteristic]

	err := c.Get(ctx, "get_wb_characteristics", baseURL, rateLimit, burst, path, nil, &resp)
	if err != nil {
		return nil, err
	}

	if resp.Error {
		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
	}

	return resp.Data, nil
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

// GetColors возвращает справочник всех допустимых цветов WB.
// Параметры baseURL, rateLimit, burst передаются из tool config.
func (c *Client) GetColors(ctx context.Context, baseURL string, rateLimit int, burst int) ([]Color, error) {
	var resp APIResponse[[]Color]
	err := c.Get(ctx, "get_wb_colors", baseURL, rateLimit, burst, "/content/v2/directory/colors", nil, &resp)
	if err != nil {
		return nil, err
	}
	if resp.Error {
		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
	}
	return resp.Data, nil
}

// GetGenders возвращает справочник полов/видов.
// В API называется "Kinds". Пример: "Мужской", "Женский", "Детский".
// Параметры baseURL, rateLimit, burst передаются из tool config.
func (c *Client) GetGenders(ctx context.Context, baseURL string, rateLimit int, burst int) ([]string, error) {
	var resp APIResponse[[]string]

	err := c.Get(ctx, "get_wb_genders", baseURL, rateLimit, burst, "/content/v2/directory/kinds", nil, &resp)
	if err != nil {
		return nil, err
	}

	if resp.Error {
		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
	}

	return resp.Data, nil
}

// GetSeasons возвращает справочник сезонов.
// Параметры baseURL, rateLimit, burst передаются из tool config.
func (c *Client) GetSeasons(ctx context.Context, baseURL string, rateLimit int, burst int) ([]string, error) {
	var resp APIResponse[[]string]

	err := c.Get(ctx, "get_wb_seasons", baseURL, rateLimit, burst, "/content/v2/directory/seasons", nil, &resp)
	if err != nil {
		return nil, err
	}

	if resp.Error {
		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
	}

	return resp.Data, nil
}

// pkg/wb/types.go
type Tnved struct {
    Tnved string `json:"tnved"` // Код (строка, т.к. может начинаться с 0)
    IsKiz bool   `json:"isKiz"` // Требует ли маркировки КИЗ
}

// GetTnved возвращает список кодов ТНВЭД для конкретного предмета.
// Параметры baseURL, rateLimit, burst передаются из tool config.
func (c *Client) GetTnved(ctx context.Context, baseURL string, rateLimit int, burst int, subjectID int, search string) ([]Tnved, error) {
	params := url.Values{}
	params.Set("subjectID", fmt.Sprintf("%d", subjectID))
	if search != "" {
		params.Set("search", search)
	}

	var resp APIResponse[[]Tnved]

	err := c.Get(ctx, "get_wb_tnved", baseURL, rateLimit, burst, "/content/v2/directory/tnved", params, &resp)
	if err != nil {
		return nil, err
	}

	if resp.Error {
		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
	}

	return resp.Data, nil
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

// GetVats возвращает список ставок НДС. Пример: ["22%", "Без НДС", "10%"].
// Параметры baseURL, rateLimit, burst передаются из tool config.
func (c *Client) GetVats(ctx context.Context, baseURL string, rateLimit int, burst int) ([]string, error) {
	var resp APIResponse[[]string]

	params := url.Values{}
	params.Set("locale", "ru")

	err := c.Get(ctx, "get_wb_vats", baseURL, rateLimit, burst, "/content/v2/directory/vat", params, &resp)
	if err != nil {
		return nil, err
	}

	if resp.Error {
		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
	}

	return resp.Data, nil
}

// GetCountries возвращает список стран производства.
// Параметры baseURL, rateLimit, burst передаются из tool config.
func (c *Client) GetCountries(ctx context.Context, baseURL string, rateLimit int, burst int) ([]Country, error) {
	var resp APIResponse[[]Country]

	params := url.Values{}
	params.Set("locale", "ru")

	err := c.Get(ctx, "get_wb_countries", baseURL, rateLimit, burst, "/content/v2/directory/countries", params, &resp)
	if err != nil {
		return nil, err
	}

	if resp.Error {
		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
	}

	return resp.Data, nil
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

// BrandsResponse представляет ответ от API брендов
// Структура отличается от стандартной APIResponse
type BrandsResponse struct {
    Brands []Brand `json:"brands"`
    Next   int     `json:"next"`   // 0 если это последняя страница
    Total  int     `json:"total"`  // Общее количество брендов
}

// GetBrands возвращает список брендов для указанного предмета с авто-пагинацией.
//
// Параметры:
//   - baseURL: базовый URL API (из tool config)
//   - rateLimit: лимит запросов в минуту (из tool config)
//   - burst: burst для rate limiter (из tool config)
//   - subjectID: ID предмета для фильтрации брендов
//   - limit: максимальное количество брендов для возврата (0 = все доступные)
//
// Возвращает список брендов отсортированных по популярности.
func (c *Client) GetBrands(ctx context.Context, baseURL string, rateLimit int, burst int, subjectID int, limit int) ([]Brand, error) {
	var allBrands []Brand
	next := 0

	for {
		params := url.Values{}
		params.Set("subjectId", fmt.Sprintf("%d", subjectID))
		if next > 0 {
			params.Set("next", fmt.Sprintf("%d", next))
		}

		var brandsResp BrandsResponse

		err := c.Get(ctx, "get_wb_brands", baseURL, rateLimit, burst,
			"/api/content/v1/brands", params, &brandsResp)
		if err != nil {
			return nil, err
		}

		allBrands = append(allBrands, brandsResp.Brands...)

		if brandsResp.Next == 0 {
			break
		}
		if limit > 0 && len(allBrands) >= limit {
			allBrands = allBrands[:limit]
			break
		}

		next = brandsResp.Next
	}

	return allBrands, nil
}

// ============================================================================
// Product Search Methods (supplierArticle -> nmID mapping)
// ============================================================================

// GetProductsByArticles ищет товары по артикулам поставщика (supplierArticle).
//
// Этот метод используется для конвертации артикулов поставщика в nmID Wildberries.
// Пользователи обычно знают свой артикул (vendor code/supplier article), а не nmID.
//
// Использует Content API: POST /content/v2/get/cards/list с textSearch.
// Требует токен с категорией Promotion (бит 6).
//
// Параметры:
//   - ctx: контекст для отмены
//   - toolID: идентификатор tool для rate limiting
//   - baseURL: базовый URL API (content-api.wildberries.ru)
//   - rateLimit: лимит запросов в минуту
//   - burst: burst для rate limiter
//   - articles: список артикулов поставщика (поиск по одному за раз)
//
// Возвращает список найденных товаров с их nmID и другой информацией.
//
// Правило 8: добавляем функциональность через новые методы, не меняя существующие.
func (c *Client) GetProductsByArticles(ctx context.Context, toolID string, baseURL string, rateLimit int, burst int, articles []string) ([]ProductInfo, error) {
	if len(articles) == 0 {
		return []ProductInfo{}, nil
	}

	var results []ProductInfo

	// Content API не поддерживает поиск по нескольким артикулам одновременно
	// API с токеном Promotion видит только карточки продавца
	// Получаем все карточки и фильтруем на стороне клиента

	// Сначала получаем все карточки (без фильтра)
	reqBody := CardsListRequest{
		Settings: CardsSettings{
			Cursor: CardsCursor{
				Limit: 100, // Максимум
			},
		},
	}

	var resp CardsListResponse
	err := c.Post(ctx, toolID, baseURL, rateLimit, burst, "/content/v2/get/cards/list", reqBody, &resp)
	if err != nil {
		return nil, err
	}

	// Создаём map для быстрого поиска
	articleMap := make(map[string]bool)
	for _, a := range articles {
		articleMap[a] = true
	}

	// Фильтруем по vendorCode
	for _, card := range resp.Cards {
		if articleMap[card.VendorCode] {
			results = append(results, ProductInfo{
				NmID:    card.NmID,
				Article: card.VendorCode,
				Name:    card.Title,
				Price:   0,
			})
			delete(articleMap, card.VendorCode) // Удаляем из map чтобы не дублировать
		}
	}

	return results, nil
}

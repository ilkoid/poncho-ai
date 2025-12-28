// Бизнес-логика методов
package wb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// GetParentCategories возвращает список родительских категорий
func (c *Client) GetParentCategories(ctx context.Context) ([]ParentCategory, error) {
	var resp APIResponse[[]ParentCategory]
	
	err := c.get(ctx, "/content/v2/object/parent/all", nil, &resp)
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

// FetchSubjectsPage - низкоуровневый запрос одной страницы + для GetAllSubjectsLazy
func (c *Client) FetchSubjectsPage(ctx context.Context, parentID, limit, offset int) ([]Subject, error) {
    params := url.Values{}
    params.Set("limit", strconv.Itoa(limit))
    params.Set("offset", strconv.Itoa(offset))
    if parentID > 0 {
        params.Set("parentID", strconv.Itoa(parentID))
    }

    var resp APIResponse[[]Subject]
    err := c.get(ctx, "/content/v2/object/all", params, &resp)
    if err != nil {
        return nil, err
    }
    if resp.Error {
        return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
    }
    return resp.Data, nil
}

// GetAllSubjects2 - "ленивый" метод, который выкачивает всё используя FetchSubjectsPage. Основной метод и для Tools в том числе
func (c *Client) GetAllSubjectsLazy(ctx context.Context, parentID int) ([]Subject, error) {
    var all []Subject
    limit := 1000
    offset := 0

    for {
        batch, err := c.FetchSubjectsPage(ctx, parentID, limit, offset)
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

// GetCharacteristics получает хар-ки для предмета
func (c *Client) GetCharacteristics(ctx context.Context, subjectID int) ([]Characteristic, error) {
	path := fmt.Sprintf("/content/v2/object/charcs/%d", subjectID)
	
	var resp APIResponse[[]Characteristic]
	
	err := c.get(ctx, path, nil, &resp)
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

// GetColors возвращает справочник всех допустимых цветов WB
func (c *Client) GetColors(ctx context.Context) ([]Color, error) {
    // Этот список может быть огромным. В доке не сказано про limit/offset.
    // Обычно справочники отдаются целиком или имеют поиск.
    // Если в query params нет limit, значит отдается всё или топ-N.
    // Судя по документации, параметров пагинации НЕТ, только locale.
    
    var resp APIResponse[[]Color]
    err := c.get(ctx, "/content/v2/directory/colors", nil, &resp)
    if err != nil {
        return nil, err
    }
    if resp.Error {
        return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
    }
    return resp.Data, nil
}

// Метод GetGenders GetGenders (в API называется "Kinds") возвращает справочник полов/видов.
// Пример: "Мужской", "Женский", "Детский"
func (c *Client) GetGenders(ctx context.Context) ([]string, error) {
    // URL из документации: /content/v2/directory/kinds
    var resp APIResponse[[]string]
    
    err := c.get(ctx, "/content/v2/directory/kinds", nil, &resp)
    if err != nil {
        return nil, err
    }
    
    if resp.Error {
        return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
    }

    return resp.Data, nil
}


// GetSeasons возвращает справочник сезонов.
func (c *Client) GetSeasons(ctx context.Context) ([]string, error) {
    // URL: /content/v2/directory/seasons
    var resp APIResponse[[]string]
    
    err := c.get(ctx, "/content/v2/directory/seasons", nil, &resp)
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

// GetTnved возвращает список кодов ТНВЭД для конкретного предмета
func (c *Client) GetTnved(ctx context.Context, subjectID int, search string) ([]Tnved, error) {
    // Параметры
    params := url.Values{}
    params.Set("subjectID", fmt.Sprintf("%d", subjectID))
    if search != "" {
        params.Set("search", search) // Опциональный поиск по коду
    }
    
    var resp APIResponse[[]Tnved]
    
    // URL: /content/v2/directory/tnved
    err := c.get(ctx, "/content/v2/directory/tnved", params, &resp)
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

// GetVats возвращает список ставок НДС. Пример: ["22%", "Без НДС", "10%"]
func (c *Client) GetVats(ctx context.Context) ([]string, error) {
    // URL: /content/v2/directory/vat
    var resp APIResponse[[]string]
    
    // В доке пример с locale=ru, добавим это, хотя это дефолт
    params := url.Values{}
    params.Set("locale", "ru")

    err := c.get(ctx, "/content/v2/directory/vat", params, &resp)
    if err != nil {
        return nil, err
    }
    
    if resp.Error {
        return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
    }

    return resp.Data, nil
}


// GetCountries возвращает список стран производства.
func (c *Client) GetCountries(ctx context.Context) ([]Country, error) {
    // URL: /content/v2/directory/countries
    var resp APIResponse[[]Country]
    
    // locale=ru (хотя по дефолту ru)
    params := url.Values{}
    params.Set("locale", "ru")

    err := c.get(ctx, "/content/v2/directory/countries", params, &resp)
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

// GetBrands возвращает список брендов для указанного предмета с авто-пагинацией
//
// Параметры:
//   - subjectID: ID предмета для фильтрации брендов
//   - limit: максимальное количество брендов для возврата (0 = все доступные)
//
// Возвращает:
//   - []Brand: список брендов (отсортированы по популярности)
//   - error: ошибка при выполнении запроса
func (c *Client) GetBrands(ctx context.Context, subjectID int, limit int) ([]Brand, error) {
    var allBrands []Brand
    next := 0

    for {
        // Формируем параметры запроса
        params := url.Values{}
        params.Set("subjectId", fmt.Sprintf("%d", subjectID))
        if next > 0 {
            params.Set("next", fmt.Sprintf("%d", next))
        }

        // Выполняем запрос
        req, err := http.NewRequestWithContext(ctx, "GET",
            c.baseURL+"/api/content/v1/brands?"+params.Encode(), nil)
        if err != nil {
            return nil, err
        }

        req.Header.Set("Authorization", c.apiKey)
        req.Header.Set("Content-Type", "application/json")

        // Ждем разрешения от rate limiter
        if err := c.limiter.Wait(ctx); err != nil {
            return nil, fmt.Errorf("rate limiter wait: %w", err)
        }

        resp, err := c.httpClient.Do(req)
        if err != nil {
            return nil, err
        }
        defer resp.Body.Close()

        if resp.StatusCode != http.StatusOK {
            body, _ := io.ReadAll(resp.Body)
            return nil, fmt.Errorf("wb api error: status %d, body: %s", resp.StatusCode, string(body))
        }

        // Парсим ответ
        var brandsResp BrandsResponse
        if err := json.NewDecoder(resp.Body).Decode(&brandsResp); err != nil {
            return nil, fmt.Errorf("failed to decode brands response: %w", err)
        }

        // Добавляем бренды к результату
        allBrands = append(allBrands, brandsResp.Brands...)

        // Проверяем условия выхода
        if brandsResp.Next == 0 {
            // Это последняя страница
            break
        }
        if limit > 0 && len(allBrands) >= limit {
            // Достигнут лимит
            allBrands = allBrands[:limit]
            break
        }

        next = brandsResp.Next
    }

    return allBrands, nil
}

package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/llm"
)

// applyTemplate подставляет значения в шаблон с {placeholder} синтаксисом.
func applyTemplate(tmpl string, pairs ...string) string {
	return strings.NewReplacer(pairs...).Replace(tmpl)
}

// ─── Hardcoded defaults (fallback если не указано в config.yaml) ───

const defaultStage1System = `Ты — эксперт по анализу карточек Wildberries (одежда, аксессуары). Сравни фотографии (истина) с текстом карточки и найди расхождения.

Определи по фото:
- Тип изделия и комплектность (посмотри размерную сетку — кол-во изделий)
- Цвет (доминирующий первый), длину, рукав, покрой, декор, свойства ткани, назначение
- Целевую аудиторию по модели и размерам на эскизе

Характеристики переданы как JSON-массив: [{"id": 12345, "name": "Цвет", "value": "красный"}, ...].
Используй поле "id" как charc_id в каждом issue.

Проверь КАЖДУЮ характеристику из карточки:
- Цвет: доминирующий — первый. Принт = рисунок НА ткани, не декор на лентах/кантовке
- Пустые характеристики, которые видны по фото — ошибка заполнения (card_value="(пусто)")
- Назначение: летнее платье = минимум "повседневная" + "летняя"

НЕ отмечай: синонимы, пустые поля которые невозможно определить по фото (состав %, страна, ТНВЭД).

Ответь JSON:
{
  "product_type": "тип по фото (укажи 'комплект' если набор из нескольких изделий)",
  "attributes": {"цвет": "...", "длина": "...", "рукав": "...", "покрой": "...", "комплектность": "комплект из X изделий / единое изделие", "состав комплекта": "...", "аудитория": "одно из: взрослая женщина | взрослый мужчина | девочка-подросток (11-16) | мальчик-подросток (11-16) | девочка (6-10) | мальчик (6-10) | малышка (2-5) | малыш (2-5)", "пол": "женский / мужской"},
  "discrepancy": true/false,
  "issues": [{"charc_id": 12345, "field": "название характеристики", "card_value": "значение в карточке или (пусто)", "correct_value": "что видно на фото", "reason": "почему это ошибка"}],
  "summary": "описание расхождений на русском, пустая строка если всё ок"
}

charc_id — числовой ID из списка характеристик. Если поле не в списке — null.`

const defaultStage1User = `НАЗВАНИЕ: {title}

ОПИСАНИЕ:
{description}

ХАРАКТЕРИСТИКИ:
{characteristics}`

const defaultStage4SelectSystem = `Ты — специалист по классификации товаров Wildberries.
На основе Vision анализа (фото) определи подходящий предмет WB из списка.
Ответь ТОЛЬКО JSON, без markdown, без пояснений:
{"subject_id": <число>, "subject_name": "<название>"}

Правила:
1. Выбери предмет который лучше всего описывает ТИП ИЗДЕЛИЯ с фото (Vision тип изделия).
2. Игнорируй текущий предмет карточки — опирайся только на Vision.
3. Выбери ТОЧНО предмет из списка — не придумывай новые.
4. Ответь ТОЛЬКО на русском языке. Никакого английского или китайского.

СПИСОК ВСЕХ ПРЕДМЕТОВ WB:
{subjects_json}`

const defaultStage4SelectUser = `Текущий предмет карточки: {subject_name} (subject_id={subject_id}) — МОЖЕТ БЫТЬ НЕВЕРНЫМ

VISION АНАЛИЗ (ФОТО — истина):
Тип изделия: {vision_product_type}
Атрибуты: {vision_attributes}
Замечания: {vision_summary}`

const defaultStage4FillSystem = `Ты — контент-менеджер бренда {brand} на Wildberries. Заполни параметры карточки на основе Vision анализа (фото — истина).

Предмет WB: {subject_name} (subject_id={subject_id})
Целевая аудитория: {audience}

ПРАВИЛА ДЛЯ НАЗВАНИЯ (title, максимум 80 символов):
Краткое и точное описание товара — 3-4 ключевых слова максимум.
Структура: [тип изделия] [ключевое свойство] {brand} [назначение].
{title_rules}
- Тип изделия: "платье", "костюм", "брюки", "футболка" и т.д.
- Одно ключевое свойство: цвет, принт или ткань
- Назначение если уместно: "для школы", "нарядное"
- Бренд {brand}
НЕ перечисляй все свойства — выбери главное.

ПРАВИЛА ДЛЯ ОПИСАНИЯ (description, максимум 500 символов):
Тон — уверенный, лаконичный, про качество и стиль. Без восклицательных знаков.
Без "вау", "must-have", "идеальный", "невероятный", "тот самый". Без marketing-клише.
{desc_rules}
Длина 3-5 предложений, максимум 500 символов.

ОБЩИЕ ПРАВИЛА:
1. Используй ТОЛЬКО charcID из списка ниже. Не придумывай ID.
2. Заполни обязательные и релевантные необязательные характеристики.
3. НЕ заполняй: Описание, SKU, Бренд, Артикул, Рос. размер, Размер, Вес товара, упаковка, Код упаковки, ИКПУ, Артикул OZON, NTIN, Код ТРУ, Рост модели, Размер на модели, Параметры модели, ТНВЭД, Утеплитель.
4. Цвет кратко: "зелёный/синий/оранжевый".
5. ВСЕ тексты — ТОЛЬКО на русском. Никакого английского или китайского.
6. Ответь ТОЛЬКО JSON, без markdown.
7. Сезон бери ТОЛЬКО из Vision атрибутов — не придумывай.
8. ОБЯЗАТЕЛЬНО сохрани из текущих характеристик: авторов принтов, названия серий, лицензионных персонажей (поле "Любимые герои", "Рисунок") — это точные данные, которые Vision не может определить по фото. Если указан автор (например Кандинский), обязательно перенеси его.

Формат: {"title": "...", "description": "...", "characteristics": [{"charc_id": <число>, "value": "..."}]}`

const defaultStage4FillUser = `Артикул: {vendor_code} (nm_id={nm_id})
ТОП-ЗАПРОС ИЗ ПОИСКА: {top_query}

ТЕКУЩИЕ ХАРАКТЕРИСТИКИ (справочно, МОГУТ СОДЕРЖАТЬ ОШИБКИ — не копируй вслепую, используй только как подсказку для сертификатов, состава, коллекции):
{characteristics}

VISION АНАЛИЗ (ФОТО — единственный источник истины):
Тип изделия: {vision_product_type}
Атрибуты: {vision_attributes}
Замечания: {vision_summary}
Аудитория: {seo_context}

ДОПУСТИМЫЕ ХАРАКТЕРИСТИКИ ПРЕДМЕТА "{subject_name}" (subject_id={subject_id}):
{char_defs_json}`

// ─── Stage 4: Characteristics-only (issues-driven) ───

const defaultStage4CharsSystem = `Ты — контент-менеджер на Wildberries. Твоя задача — исправить характеристики карточки товара по результатам фото-аудита.

Предмет WB: {subject_name} (subject_id={subject_id})

ПРАВИЛА:
1. Исправь ТОЛЬКО характеристики из списка ISSUES ниже. НЕ генерируй title или description.
2. Для каждого issue: возьми suggested как основу и отформатируй значение корректно для WB API.
3. Используй ТОЛЬКО charc_id из списка допустимых характеристик. Не придумывай ID.
4. Если issue помечен "is_empty": true — характеристика отсутствует, её нужно добавить.
5. Если issue помечен "is_empty": false — характеристика заполнена неверно, замени значение.
6. ВСЕ тексты — ТОЛЬКО на русском. Никакого английского или китайского.
7. Ответь ТОЛЬКО JSON, без markdown.

Формат: {"characteristics": [{"charc_id": <число>, "value": "<строка>"}]}`

const defaultStage4CharsUser = `Артикул: {vendor_code} (nm_id={nm_id})

VISION АНАЛИЗ (ФОТО — истина):
Тип изделия: {vision_product_type}
Атрибуты: {vision_attributes}

ОПИСАНИЕ ТОВАРА (текстовые особенности для обогащения характеристик):
{description}

ISSUES (структурированные расхождения из фото-аудита):
{issues_structured}

ТЕКУЩИЕ ЗНАЧЕНИЯ ПРОБЛЕМНЫХ ХАРАКТЕРИСТИК (справочно):
{characteristics}

ДОПУСТИМЫЕ ХАРАКТЕРИСТИКИ ПРЕДМЕТА "{subject_name}" (subject_id={subject_id}):
{char_defs_json}`

// ─── Audience rule defaults ───

var defaultAudienceRules = map[string]AudienceRule{
	"взрослая женщина": {
		TitleRules: `- Сезон из Vision (летнее, демисезонное, зимнее). НЕ придумывай "круглогодичное".
- "для женщин", "женская"
Пример: "Летние сабо женские {brand} — эко-замша"`,
		DescRules: `- Пиши лаконично и стильно — женщина выбирает сама.
- Первое предложение — про стиль и назначение вещи.
- Упомяни бренд {brand}.
- Опиши сценарий: куда носить, с чем сочетать. Женщина ценит универсальность.
- Упомяни уход: стирка, не мнётся, принт не выцветает.
- НЕ перечисляй отсутствие чего-либо — только позитивные свойства.
- Включи 2-3 SEO-фразы (женская, базовая, летняя, повседневная).`,
		SEOContext: "женская одежда, аудитория — взрослая женщина",
	},
	"взрослый мужчина": {
		TitleRules: `- Сезон из Vision (летнее, демисезонное, зимнее). НЕ придумывай "круглогодичное".
- "мужская", "для мужчин"
Пример: "Летняя мужская футболка {brand} — хлопок, свободный крой"`,
		DescRules: `- Пиши прямо и по делу — мужчина ценит комфорт и функциональность.
- Первое предложение — про комфорт: "лёгкая, удобная".
- Упомяни бренд {brand}.
- Опиши сценарий: "для тренировки", "на дачу", "каждый день".
- Упомяни уход: стирка, не мнётся, не садится.
- Включи 2-3 SEO-фразы (мужская, спортивная, базовая).`,
		SEOContext: "мужская одежда, аудитория — взрослый мужчина",
	},
	"девочка-подросток (11-16)": {
		TitleRules: `- Сезон из Vision (летнее, демисезонное, зимнее). НЕ придумывай "круглогодичное".
- "подростковая", "для девочки-подростка"
Пример: "Подростковая футболка для девочки {brand} — оверсайз, с принтом"`,
		DescRules: `- Пиши для мамы, но с оглядкой на подростка — она покупает, но дочь решает.
- Первое предложение — стиль и тренд.
- Упомяни бренд {brand}.
- Опиши сценарий: "в школу", "на встречу с друзьями". Подросток ценит самовыражение.
- Упомяни уход: стирка, не мнётся, принт не выцветает.
- Включи 2-3 SEO-фразы (подростковая, для девочки, школьная).`,
		SEOContext: "подростковая одежда для девочки 11-16 лет",
	},
	"мальчик-подросток (11-16)": {
		TitleRules: `- Сезон из Vision (летнее, демисезонное, зимнее). НЕ придумывай "круглогодичное".
- "подростковая", "для мальчика-подростка"
Пример: "Подростковый костюм для мальчика {brand} — толстовка и джоггеры"`,
		DescRules: `- Пиши для мамы, но с оглядкой на подростка — она покупает, но сын решает.
- Первое предложение — комфорт и стиль.
- Упомяни бренд {brand}.
- Опиши сценарий: "в школу", "на тренировку". Подросток-мальчик ценит, когда вещь не "детская".
- Упомяни уход: стирка, не мнётся, принт не выцветает.
- Включи 2-3 SEO-фразы (подростковая, для мальчика, спортивная).`,
		SEOContext: "подростковая одежда для мальчика 11-16 лет",
	},
	"малыш": {
		TitleRules: `- Сезон из Vision (летнее, демисезонное, зимнее). НЕ придумывай "круглогодичное".
- "для малышки", "малышковая"
Пример: "Малышковое платье для девочки {brand} — хлопок, с принтом"`,
		DescRules: `- Пиши для мамы — ей важны мягкость и комфорт.
- Первое предложение — про комфорт и материал.
- Упомяни бренд {brand}.
- Опиши сценарий: "для прогулки", "в садик".
- Упомяни уход: стирка, гипоаллергенно, не линяет.
- Включи 2-3 SEO-фразы (малышковая, для малышки, детская).`,
		SEOContext: "малышковая одежда (2-5 лет)",
	},
	"по умолчанию": {
		TitleRules: `- Сезон из Vision (летнее, демисезонное, зимнее). НЕ придумывай "круглогодичное".
- Кому: "для девочки", "для мальчика"
Пример: "Летнее платье для девочки {brand} — джерси с принтом"`,
		DescRules: `- Пиши для мамы — лаконично, про качество и стиль.
- Первое предложение — про назначение и материал.
- Упомяни бренд {brand}.
- Опиши сценарий: "в школу", "на прогулку", "на праздник".
- Упомяни уход: стирка, не мнётся, принт не выцветает.
- НЕ перечисляй отсутствие чего-либо — только позитивные свойства.
- Включи 2-3 SEO-фразы (детское, нарядное, школьное).`,
		SEOContext: "детская одежда (6-10 лет)",
	},
}

// resolvePrompt возвращает шаблон из конфига или дефолтный.
func resolvePrompt(configured, defaultTmpl string) string {
	if configured != "" {
		return configured
	}
	return defaultTmpl
}

// ─── Builder functions ───

// buildAuditMessages строит сообщения для единого аудита (этап 1).
func buildAuditMessages(title, description string, chars []CardChar, photoURLs []string, prompts PromptConfig) []llm.Message {
	charText := formatCharacteristics(chars)

	system := resolvePrompt(prompts.Stage1System, defaultStage1System)
	userTmpl := resolvePrompt(prompts.Stage1User, defaultStage1User)
	user := applyTemplate(userTmpl,
		"{title}", title,
		"{description}", description,
		"{characteristics}", charText,
	)

	return []llm.Message{
		{Role: llm.RoleSystem, Content: system},
		{Role: llm.RoleUser, Content: user, Images: photoURLs},
	}
}

// buildStage4SelectMessages строит сообщения для выбора предмета WB (этап 4).
func buildStage4SelectMessages(
	card CardData,
	row VisionAnalysisRow,
	subjectsJSON string,
	prompts PromptConfig,
) (system, user string) {
	sysTmpl := resolvePrompt(prompts.Stage4SelectSys, defaultStage4SelectSystem)
	userTmpl := resolvePrompt(prompts.Stage4SelectUser, defaultStage4SelectUser)

	system = applyTemplate(sysTmpl,
		"{subjects_json}", subjectsJSON,
	)
	user = applyTemplate(userTmpl,
		"{subject_name}", card.SubjectName,
		"{subject_id}", fmt.Sprintf("%d", card.SubjectID),
		"{vision_product_type}", row.VisionProductType,
		"{vision_attributes}", row.VisionAttributes,
		"{vision_summary}", row.VisionSummary,
	)
	return
}

// buildStage4CharsMessages строит сообщения для issues-driven генерации характеристик (этап 4).
// Передаёт структурированные issues и только релевантные char definitions.
func buildStage4CharsMessages(
	row VisionAnalysisRow,
	chars []CardChar,
	subjectID int,
	subjectName string,
	defsJSON string,
	issuesStructured string,
	prompts PromptConfig,
) (system, user string) {
	sysTmpl := resolvePrompt(prompts.Stage4CharsSys, defaultStage4CharsSystem)
	userTmpl := resolvePrompt(prompts.Stage4CharsUser, defaultStage4CharsUser)

	system = applyTemplate(sysTmpl,
		"{subject_name}", subjectName,
		"{subject_id}", fmt.Sprintf("%d", subjectID),
	)

	// Фильтруем характеристики: только проблемные (чьи имена есть в matched issues)
	charText := formatCharacteristics(chars)

	user = applyTemplate(userTmpl,
		"{vendor_code}", row.VendorCode,
		"{nm_id}", fmt.Sprintf("%d", row.NmID),
		"{vision_product_type}", row.VisionProductType,
		"{vision_attributes}", row.VisionAttributes,
		"{description}", row.Description,
		"{issues_structured}", issuesStructured,
		"{characteristics}", charText,
		"{subject_name}", subjectName,
		"{subject_id}", fmt.Sprintf("%d", subjectID),
		"{char_defs_json}", defsJSON,
	)
	return
}

// charcForPrompt — элемент списка характеристик для промпта Stage 1.
type charcForPrompt struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// formatCharacteristics форматирует характеристики как JSON-массив с charc_id.
func formatCharacteristics(chars []CardChar) string {
	if len(chars) == 0 {
		return "(нет характеристик)"
	}
	items := make([]charcForPrompt, 0, len(chars))
	for _, c := range chars {
		var value string
		var arr []string
		if json.Unmarshal([]byte(c.Value), &arr) == nil {
			value = strings.Join(arr, ", ")
		} else {
			var s string
			if json.Unmarshal([]byte(c.Value), &s) == nil {
				value = s
			} else {
				value = c.Value
			}
		}
		items = append(items, charcForPrompt{ID: c.CharID, Name: c.Name, Value: value})
	}
	b, _ := json.Marshal(items)
	return string(b)
}

// textAnalysisResult — результат парсинга JSON от LLM.
type textAnalysisResult struct {
	Discrepancy bool   `json:"discrepancy"`
	Summary     string `json:"summary"`
}

// flexibleMap is a map[string]string that accepts both string and []string values
// from LLM JSON output, flattening arrays to comma-joined strings.
type flexibleMap map[string]string

func (f *flexibleMap) UnmarshalJSON(data []byte) error {
	raw := make(map[string]json.RawMessage)
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	result := make(map[string]string, len(raw))
	for k, v := range raw {
		result[k] = flattenJSONString(v)
	}
	*f = result
	return nil
}

// flattenJSONString converts a JSON string or array of strings to a single string.
func flattenJSONString(raw json.RawMessage) string {
	raw = []byte(strings.TrimSpace(string(raw)))

	// Try string first
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}

	// Try array of strings
	var arr []string
	if json.Unmarshal(raw, &arr) == nil {
		return strings.Join(arr, ", ")
	}

	// Fallback: return raw without quotes
	return strings.Trim(string(raw), `"`)
}

// flexibleString is a string that accepts both string and []string from LLM JSON output.
type flexibleString string

func (f *flexibleString) UnmarshalJSON(data []byte) error {
	*f = flexibleString(flattenJSONString(data))
	return nil
}

// visionAnalysisResult — результат парсинга JSON от Vision LLM.
type visionAnalysisResult struct {
	ProductType string      `json:"product_type"`
	Attributes  flexibleMap `json:"attributes"`
	Discrepancy bool        `json:"discrepancy"`
	Issues      []struct {
		CharcID      *int   `json:"charc_id"`
		Field        string `json:"field"`
		CardValue    string `json:"card_value"`
		CorrectValue string `json:"correct_value"`
		Reason       string `json:"reason"`
	} `json:"issues"`
	Summary string `json:"summary"`
}

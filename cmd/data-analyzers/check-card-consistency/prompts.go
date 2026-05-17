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

const defaultStage1System = `Ты — аудитор карточек товаров Wildberries. Твоя задача — найти расхождения между названием, описанием и характеристиками товара.

Проанализируй данные карточки и найди ЛОГИЧЕСКИЕ ПРОТИВОРЕЧИЯ:
- Название говорит одно, а характеристики — другое (например: "платье макси" но длина "мини")
- Описание описывает один тип изделия, а характеристики — другой
- Характеристика "Комплектация" заполнена шаблонными данными (например "Футболка + брюки" для платья)
- Цвет в описании не совпадает с характеристикой цвета
- Сезон в описании не совпадает с характеристикой сезона

НЕ отмечай как расхождение:
- Мелкие стилистические различия
- Отсутствие необязательных полей
- Синонимы (например "футболка" vs "топ")

Ответь СТРОГО JSON:
{"discrepancy": true/false, "summary": "краткое описание расхождений на русском, пустая строка если всё ок"}`

const defaultStage1User = `НАЗВАНИЕ: {title}

ОПИСАНИЕ:
{description}

ХАРАКТЕРИСТИКИ:
{characteristics}`

const defaultStage3System = `Ты — эксперт по анализу фотографий одежды и аксессуаров Wildberries. Фото — истина.

Проанализируй ВСЕ фотографии товара (обычно 5) и сравни с данными карточки. Определи:
1. Тип изделия по фото (платье, брюки, шорты, футболка, костюм, комплект и т.д.)
2. Видимые атрибуты: цвет, длина изделия, рукав, покрой, декор
3. Комплектность: сколько отдельных изделий входит в товар. Внимательно посмотри на фото размерной сетки / эскиза (обычно одно из последних фото) — там указано количество изделий. Если на размерной сетке указано 2 изделия — это комплект, если 1 — единое изделие.
4. Целевая аудитория: для кого товар — посмотри на модель на фото, диапазон размеров на эскизе, стиль изделия
5. Есть ли расхождения между фото и описанием/характеристиками

КРИТИЧЕСКИ ВАЖНО — комплектность:
- Если на фото видно несколько предметов одежды (платье + лонгслив, топ + брюки, футболка + шорты) — это КОМПЛЕКТ.
- Фото размерной сетки / эскиза обычно одно из последних — на ней написано количество изделий (1 шт, 2 шт и т.д.).
- Если на размерной сетке "2 шт" или видны два отдельных предмета — product_type должен содержать слово "комплект".
- Если это одно изделие (платье, футболка, брюки) — не называй комплектом.

КРИТИЧЕСКИ ВАЖНО — целевая аудитория:
- Посмотри на модель: это взрослый человек, подросток или ребёнок? Возраст модели определяет аудиторию.
- Посмотри на размерную сетку: размеры 80-92 = малыши, 98-140 = дети, 134-170 = подростки, XS-XL/40-46 = взрослые.
- Определи пол модели: мужская или женская одежда.
- audience должна быть ТОЧНО одним из: "взрослая женщина", "взрослый мужчина", "девочка-подросток (11-16)", "мальчик-подросток (11-16)", "девочка (6-10)", "мальчик (6-10)", "малышка (2-5)", "малыш (2-5)".

Ответь СТРОГО JSON:
{
  "product_type": "тип изделия по фото (обязательно укажи 'комплект' если это набор из нескольких изделий)",
  "attributes": {"цвет": "...", "длина": "...", "рукав": "...", "покрой": "...", "комплектность": "комплект из X изделий / единое изделие", "состав комплекта": "перечисли что входит", "аудитория": "взрослая женщина / взрослый мужчина / девочка-подросток / мальчик-подросток / девочка / мальчик / малышка / малыш", "пол": "женский / мужской"},
  "discrepancy": true/false,
  "summary": "что именно не совпадает между фото и описанием, пустая строка если всё ок"
}`

const defaultStage3User = `НАЗВАНИЕ: {title}

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

ТЕКУЩИЕ ХАРАКТЕРИСТИКИ (справочно, МОГУТ СОДЕРЖАТЬ ОШИБКИ — не копируй вслепую, используй только как подсказку для сертификатов, состава, коллекции):
{characteristics}

VISION АНАЛИЗ (ФОТО — единственный источник истины):
Тип изделия: {vision_product_type}
Атрибуты: {vision_attributes}
Замечания: {vision_summary}
Аудитория: {seo_context}

{search_queries}

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

// resolveAudienceRules объединяет конфиг и дефолты: конфиг перекрывает дефолты.
func resolveAudienceRules(configured map[string]AudienceRule) map[string]AudienceRule {
	result := make(map[string]AudienceRule, len(defaultAudienceRules))
	for k, v := range defaultAudienceRules {
		result[k] = v
	}
	for k, v := range configured {
		result[k] = v
	}
	return result
}

// ─── Builder functions ───

// buildTextAnalysisMessages строит сообщения для текстового анализа (этап 1).
func buildTextAnalysisMessages(title, description string, chars []CardChar, prompts PromptConfig) []llm.Message {
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
		{Role: llm.RoleUser, Content: user},
	}
}

// buildVisionMessages строит сообщения для Vision анализа (этап 3).
func buildVisionMessages(title, description string, chars []CardChar, photoURLs []string, prompts PromptConfig) []llm.Message {
	charText := formatCharacteristics(chars)

	system := resolvePrompt(prompts.Stage3System, defaultStage3System)
	userTmpl := resolvePrompt(prompts.Stage3User, defaultStage3User)
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

// buildStage4FillMessages строит сообщения для заполнения параметров карточки (этап 4).
func buildStage4FillMessages(
	row VisionAnalysisRow,
	chars []CardChar,
	subjectID int,
	subjectName string,
	defsJSON string,
	brand string,
	audience string,
	titleRules string,
	descRules string,
	seoContext string,
	searchQueriesText string,
	prompts PromptConfig,
) (system, user string) {
	sysTmpl := resolvePrompt(prompts.Stage4FillSys, defaultStage4FillSystem)
	userTmpl := resolvePrompt(prompts.Stage4FillUser, defaultStage4FillUser)

	system = applyTemplate(sysTmpl,
		"{brand}", brand,
		"{subject_name}", subjectName,
		"{subject_id}", fmt.Sprintf("%d", subjectID),
		"{audience}", audience,
		"{title_rules}", titleRules,
		"{desc_rules}", descRules,
	)

	charText := formatCharacteristics(chars)
	user = applyTemplate(userTmpl,
		"{vendor_code}", row.VendorCode,
		"{nm_id}", fmt.Sprintf("%d", row.NmID),
		"{characteristics}", charText,
		"{vision_product_type}", row.VisionProductType,
		"{vision_attributes}", row.VisionAttributes,
		"{vision_summary}", row.VisionSummary,
		"{seo_context}", seoContext,
		"{search_queries}", searchQueriesText,
		"{subject_name}", subjectName,
		"{subject_id}", fmt.Sprintf("%d", subjectID),
		"{char_defs_json}", defsJSON,
	)
	return
}

// formatSearchQueries форматирует поисковые запросы для промпта Stage 4.
func formatSearchQueries(queries []SearchQuery) string {
	if len(queries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("ТОП-ПОИСКОВЫЕ ЗАПРОСЫ (реальные данные WB за 30 дней):")
	for i, q := range queries {
		fmt.Fprintf(&b, "\n%d. \"%s\" (открытий: %d, заказов: %d, частотность: %d)",
			i+1, q.Text, q.OpenCard, q.Orders, q.Frequency)
	}
	b.WriteString("\n\nИспользуй 1-2 самые релевантные фразы естественно в title и description.\nНЕ вставляй запрос, если он противоречит тому, что видно на фото.")
	return b.String()
}

// formatCharacteristics форматирует характеристики для промпта.
func formatCharacteristics(chars []CardChar) string {
	if len(chars) == 0 {
		return "(нет характеристик)"
	}
	var b strings.Builder
	for _, c := range chars {
		var values []string
		if err := json.Unmarshal([]byte(c.Value), &values); err == nil {
			fmt.Fprintf(&b, "- %s: %s\n", c.Name, strings.Join(values, ", "))
		} else {
			var single string
			if err := json.Unmarshal([]byte(c.Value), &single); err == nil {
				fmt.Fprintf(&b, "- %s: %s\n", c.Name, single)
			} else {
				fmt.Fprintf(&b, "- %s: %s\n", c.Name, c.Value)
			}
		}
	}
	return b.String()
}

// textAnalysisResult — результат парсинга JSON от LLM.
type textAnalysisResult struct {
	Discrepancy bool   `json:"discrepancy"`
	Summary     string `json:"summary"`
}

// visionAnalysisResult — результат парсинга JSON от Vision LLM.
type visionAnalysisResult struct {
	ProductType string            `json:"product_type"`
	Attributes  map[string]string `json:"attributes"`
	Discrepancy bool              `json:"discrepancy"`
	Summary     string             `json:"summary"`
}

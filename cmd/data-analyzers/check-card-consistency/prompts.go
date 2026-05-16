package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/llm"
)

// buildTextAnalysisMessages строит сообщения для текстового анализа (этап 1).
func buildTextAnalysisMessages(title, description string, chars []CardChar) []llm.Message {
	charText := formatCharacteristics(chars)

	system := `Ты — аудитор карточек товаров Wildberries. Твоя задача — найти расхождения между названием, описанием и характеристиками товара.

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

	user := fmt.Sprintf("НАЗВАНИЕ: %s\n\nОПИСАНИЕ:\n%s\n\nХАРАКТЕРИСТИКИ:\n%s", title, description, charText)

	return []llm.Message{
		{Role: llm.RoleSystem, Content: system},
		{Role: llm.RoleUser, Content: user},
	}
}

// buildVisionMessages строит сообщения для Vision анализа (этап 3).
func buildVisionMessages(title, description string, chars []CardChar, photoURLs []string) []llm.Message {
	charText := formatCharacteristics(chars)

	system := `Ты — эксперт по анализу фотографий одежды и аксессуаров Wildberries. Фото — истина.

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

	user := fmt.Sprintf("НАЗВАНИЕ: %s\n\nОПИСАНИЕ:\n%s\n\nХАРАКТЕРИСТИКИ:\n%s", title, description, charText)

	return []llm.Message{
		{Role: llm.RoleSystem, Content: system},
		{Role: llm.RoleUser, Content: user, Images: photoURLs},
	}
}

// buildGenerateParamsMessages строит сообщения для генерации новых параметров (этап 4).
func buildGenerateParamsMessages(title, description string, chars []CardChar, visionProductType, visionAttrs, visionSummary string, subjectChars []string) []llm.Message {
	charText := formatCharacteristics(chars)

	system := `Ты — специалист по заполнению карточек товаров Wildberries. На основе Vision анализа (фото) сгенерируй корректные параметры карточки.

Используй ТОЛЬКО допустимые значения характеристик из предоставленного справочника.
Название должно точно описывать товар по фото. Описание — подробное и truthful.

Ответь СТРОГО JSON:
{
  "title": "новое название",
  "description": "новое описание",
  "characteristics": [{"id": 123, "value": "значение"}],
  "subject_name": "название предмета WB"
}`

	user := fmt.Sprintf(`ТЕКУЩЕЕ НАЗВАНИЕ: %s

ТЕКУЩЕЕ ОПИСАНИЕ:
%s

ТЕКУЩИЕ ХАРАКТЕРИСТИКИ:
%s

VISION АНАЛИЗ (ФОТО):
Тип изделия: %s
Атрибуты: %s
Замечания: %s

ДОПУСТИМЫЕ ХАРАКТЕРИСТИКИ ПРЕДМЕТА:
%s`, title, description, charText, visionProductType, visionAttrs, visionSummary, strings.Join(subjectChars, "\n"))

	return []llm.Message{
		{Role: llm.RoleSystem, Content: system},
		{Role: llm.RoleUser, Content: user},
	}
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
	Summary     string            `json:"summary"`
}

// generateParamsResult — результат генерации новых параметров.
type generateParamsResult struct {
	Title          string                `json:"title"`
	Description    string                `json:"description"`
	Characteristics []struct {
		ID    int    `json:"id"`
		Value string `json:"value"`
	} `json:"characteristics"`
	SubjectName string `json:"subject_name"`
}

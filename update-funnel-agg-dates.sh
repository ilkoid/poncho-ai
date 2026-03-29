#!/bin/bash
# Обновляет даты в config.yaml для download-wb-funnel-agg
# Используется для еженедельной загрузки данных за прошлую неделю (Пн-Вс)
# Сравнение с позапрошлой неделей

set -e

CONFIG_PATH="cmd/data-downloaders/download-wb-funnel-agg/config.yaml"

# Проверка существования конфига
if [ ! -f "$CONFIG_PATH" ]; then
    echo "❌ Файл не найден: $CONFIG_PATH"
    exit 1
fi

# Вычисляем даты
# Текущая неделя: если сегодня воскресенье (7), то "last monday" = эта неделя
# Нужно использовать logic для прошлой недели

TODAY=$(date +%u)  # 1=Пн, 7=Вс

# Если сегодня воскресенье (7), то прошлую неделю считаем от прошлого понедельника
# Если любой другой день, то "last monday" = прошлый понедельник
if [ "$TODAY" -eq 7 ]; then
    # Сегодня воскресенье - прошлый понедельник был 6 дней назад
    MONDAY_LAST=$(date -d "6 days ago" +%Y-%m-%d)
    SUNDAY_LAST=$(date +%Y-%m-%d)  # Сегодня
else
    # Сегодня любой другой день - last monday = прошлый понедельник
    MONDAY_LAST=$(date -d "last monday" +%Y-%m-%d)
    SUNDAY_LAST=$(date -d "last sunday" +%Y-%m-%d)
fi

# Позапрошлая неделя (минус 7 дней от прошлой недели)
MONDAY_PREV=$(date -d "$MONDAY_LAST -7 days" +%Y-%m-%d)
SUNDAY_PREV=$(date -d "$SUNDAY_LAST -7 days" +%Y-%m-%d)

echo "📅 Обновление дат для funnel-agg:"
echo "   Selected: $MONDAY_LAST → $SUNDAY_LAST (прошлая неделя)"
echo "   Past:      $MONDAY_PREV → $SUNDAY_PREV (позапрошлая неделя)"
echo

# Обновляем config.yaml
sed -i "s/selected_start:.*/selected_start: \"$MONDAY_LAST\"  # Понедельник прошлой недели/" "$CONFIG_PATH"
sed -i "s/selected_end:.*/selected_end: \"$SUNDAY_LAST\"  # Воскресенье прошлой недели/" "$CONFIG_PATH"
sed -i "s/past_start:.*/past_start: \"$MONDAY_PREV\"  # Понедельник позапрошлой недели/" "$CONFIG_PATH"
sed -i "s/past_end:.*/past_end: \"$SUNDAY_PREV\"  # Воскресенье позапрошлой недели/" "$CONFIG_PATH"

echo "✅ Конфиг обновлён: $CONFIG_PATH"
echo
echo "Теперь можно запускать:"
echo "   cd cmd/data-downloaders/download-wb-funnel-agg && go run main.go"

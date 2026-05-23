# download-wb-sales-v2

**v2 версия** downloader продаж, построенная на новой переиспользуемой библиотеке `pkg/sales`.

## Цель

- Протестировать логику после выноса в `pkg/`
- Не ломать оригинальную утилиту `download-wb-sales/`
- Быть основой для будущего Tool-а `download_wb_sales`

## Использование

```bash
cd cmd/data-downloaders/download-wb-sales-v2
cp ../../.configs/.../some-config.yaml ./config.yaml   # или используй свой
WB_STAT=xxx go run .
```

Рекомендуется использовать отдельную тестовую БД при активном тестировании.

## Отличия от оригинала

- Основана на `pkg/sales.Downloader`
- Логика дат и resume теперь в переиспользуемом пакете
- Готовится к использованию как Tool

Оригинальная утилита `download-wb-sales` остаётся неизменной.

# WB API Fields vs Database Schema Mapping

Анализ соответствия полей WB API (`reportDetailByPeriod`) и схемы базы данных `sales-2026.db`.

## Статус на 2026-03-19

- **Таблица sales**: 27 полей
- **Таблица service_records**: 19 полей
- **Итого сохранено**: ~46 из ~67 полей (**69%**)

---

## ✅ Поля, которые СОХРАНЯЕТСЯ в БД

### Таблица `sales` (основные продажи)

| WB API поле | БД поле | Тип | Описание |
|-------------|---------|-----|----------|
| rrd_id | rrd_id | INTEGER | Уникальный ID записи (пагинация) |
| realizationreport_id | realizationreport_id | INTEGER | ID отчёта реализации |
| nm_id | nm_id | INTEGER | ID товара (WB) |
| sa_name | supplier_article | TEXT | Артикул продавца |
| barcode | barcode | TEXT | Штрихкод |
| brand_name | brand_name | TEXT | Бренд |
| subject_name | subject_name | TEXT | Предмет |
| ts_name | ts_name | TEXT | Размер |
| doc_type_name | doc_type_name | TEXT | Тип ("Продажа", "Возврат") |
| quantity | quantity | INTEGER | Количество |
| retail_price | retail_price | REAL | Розничная цена |
| retail_amount | retail_amount | REAL | Сумма продажи |
| sale_percent | sale_percent | REAL | Процент продажи |
| commission_percent | commission_percent | REAL | Процент комиссии |
| ppvz_for_pay | ppvz_for_pay | REAL | К выплате продавцу |
| delivery_rub | delivery_rub | REAL | Стоимость доставки |
| delivery_method | delivery_method | TEXT | Метод доставки (FBW/FBS) |
| gi_box_type_name | gi_box_type_name | TEXT | Тип короба (FBW) |
| office_name | office_name | TEXT | Склад/офис |
| order_dt | order_dt | TEXT | Дата заказа |
| sale_dt | sale_dt | TEXT | Дата продажи |
| rr_dt | rr_dt | TEXT | Дата отчёта |

**Итого: 23 поля** (+ 4 технических: id, is_cancel, cancel_dt, created_at)

### Таблица `service_records` (логистика, издержки)

| WB API поле | БД поле | Тип | Описание |
|-------------|---------|-----|----------|
| rrd_id | rrd_id | INTEGER | Уникальный ID записи |
| realizationreport_id | realizationreport_id | INTEGER | ID отчёта |
| supplier_oper_name | supplier_oper_name | TEXT | Тип операции (ключевое!) |
| nm_id | nm_id | INTEGER | ID товара |
| supplier_article | supplier_article | TEXT | Артикул |
| brand_name | brand_name | TEXT | Бренд |
| subject_name | subject_name | TEXT | Предмет |
| barcode | barcode | TEXT | Штрихкод |
| shk_id | shk_id | INTEGER | ID штрихкода |
| srid | srid | TEXT | Уникальный ID |
| delivery_method | delivery_method | TEXT | Метод доставки |
| gi_box_type_name | gi_box_type_name | TEXT | Тип короба |
| delivery_rub | delivery_rub | REAL | Стоимость доставки |
| ppvz_vw | ppvz_vw | REAL | Корректировка |
| ppvz_vw_nds | ppvz_vw_nds | REAL | НДС корректировки |
| rebill_logistic_cost | rebill_logistic_cost | REAL | Стоимость логистики |
| order_dt | order_dt | TEXT | Дата заказа |
| sale_dt | sale_dt | TEXT | Дата продажи |
| rr_dt | rr_dt | TEXT | Дата отчёта |

**Итого: 19 полей** (+ 1 технический: id)

---

## ❌ Поля, которые НЕ СОХРАНЯЮТСЯ в БД (~21 поле)

### Финансовые (аналитика продаж)

| WB API поле | Тип | Описание | Причина не сохранения |
|-------------|-----|----------|----------------------|
| retail_price_withdisc_rub | REAL | Цена со скидкой | Вычисляемое поле |
| ppvz_spp_prc | REAL | Процент СПП | Расчётная аналитика |
| ppvz_kvw_prc_base | REAL | Базовый % комиссии | Внутренняя логика WB |
| ppvz_kvw_prc | REAL | Итоговый % комиссии | Вычисляемое поле |
| ppvz_sales_commission | REAL | Комиссия продавца | Детализация |
| ppvz_reward | REAL | Вознаграждение | Детализация |
| acquiring_fee | REAL | Эквайринговая комиссия | Платежи |
| acquiring_percent | REAL | % эквайринга | Платежи |

### Банковские данные

| WB API поле | Тип | Описание |
|-------------|-----|----------|
| payment_processing | TEXT | Тип платежа |
| acquiring_bank | TEXT | Банк-эквайер |
| ppvz_office_id | INTEGER | ID ПВЗ |
| ppvz_supplier_id | INTEGER | ID продавца WB |
| ppvz_supplier_name | TEXT | Имя продавца WB |
| ppvz_inn | TEXT | ИНН продавца |

### Логистика и издержки (детализация)

| WB API поле | Тип | Описание |
|-------------|-----|----------|
| storage_fee | REAL | Стоимость хранения |
| deduction | REAL | Удержание |
| acceptance | REAL | Приёмка |
| bonus_type_name | TEXT | Тип бонуса/штрафа |
| penalty | REAL | Штраф |
| additional_payment | REAL | Дополнительная выплата |
| gi_id | INTEGER | ID короба |
| dlv_prc | REAL | % доставки |

### Маркетинг и промо

| WB API поле | Тип | Описание |
|-------------|-----|----------|
| seller_promo_id | INTEGER | ID промо продавца |
| seller_promo_discount | REAL | Скидка промо |
| loyalty_id | INTEGER | ID программы лояльности |
| loyalty_discount | REAL | Скидка лояльности |
| uuid_promocode | TEXT | UUID промокода |

### Кэшбэк

| WB API поле | Тип | Описание |
|-------------|-----|----------|
| cashback_amount | REAL | Сумма кэшбэка |
| cashback_discount | REAL | Скидка кэшбэка |
| cashback_commission_change | REAL | Изменение комиссии |

### Прочее

| WB API поле | Тип | Описание |
|-------------|-----|----------|
| kiz | TEXT | Метка маркировки (Честный ЗНАК) |
| srid | TEXT | Уникальный ID (есть в service_records) |
| trbx_id | TEXT | ID транзакции |
| installment_cofinancing_amount | REAL | Рассрочка |
| order_uid | TEXT | UUID заказа |
| wibes_wb_discount_percent | REAL | Скидка WB |
| delivery_amount | REAL | Сумма доставки |
| return_amount | REAL | Сумма возврата |
| date_from | TEXT | Начало периода отчёта |
| date_to | TEXT | Конец периода отчёта |
| create_dt | TEXT | Дата создания отчёта |
| currency_name | TEXT | Валюта |
| suppliercontract_code | TEXT | Код контракта |
| fix_tariff_date_from | TEXT | Дата начала фиксированного тарифа |
| fix_tariff_date_to | TEXT | Дата конца фиксированного тарифа |
| declaration_number | TEXT | Номер декларации |
| sticker_id | TEXT | ID стикера |
| site_country | TEXT | Страна сайта |
| srv_dbs | BOOLEAN | ??? |
| assembly_id | INTEGER | ID сборки |
| report_type | INTEGER | Тип отчёта |
| is_legal_entity | BOOLEAN | Юридическое лицо |
| payment_schedule | INTEGER | График платежей |

---

## Причины не сохранения полей

### 1. **Вычисляемые поля**
Могут быть получены из существующих:
- `ppvz_kvw_prc` = из `ppvz_for_pay` / `retail_amount`
- `retail_price_withdisc_rub` = из `retail_price` - скидки

### 2. **Редко используемая детализация**
- Платёжные данные (acquiring)
- Детализация комиссии (ppvz分解)
- Промо/лояльность

### 3. **Внутренние данные WB**
- IDs систем WB (ppvz_office_id, trbx_id)
- Технические поля (report_type, srv_dbs)

### 4. **Исторические/метаданные**
- Периоды отчётов (date_from, date_to)
- Контракты, декларации

---

## Рекомендации по расширению

### Приоритет 1 (финансовая аналитика)
```sql
ALTER TABLE sales ADD COLUMN ppvz_spp_prc REAL;
ALTER TABLE sales ADD COLUMN retail_price_withdisc_rub REAL;
ALTER TABLE sales ADD COLUMN acquiring_fee REAL;
```

### Приоритет 2 (маркетинг)
```sql
ALTER TABLE sales ADD COLUMN seller_promo_id INTEGER;
ALTER TABLE sales ADD COLUMN seller_promo_discount REAL;
ALTER TABLE sales ADD COLUMN loyalty_discount REAL;
```

### Приоритет 3 (детализация логистики)
```sql
ALTER TABLE sales ADD COLUMN storage_fee REAL;
ALTER TABLE sales ADD COLUMN deduction REAL;
ALTER TABLE sales ADD COLUMN bonus_type_name TEXT;
```

---

## SQL для миграции (если потребуется расширение)

```sql
-- Создаём новую таблицу с расширенными полями
CREATE TABLE sales_v2 (
    -- Все существующие поля...
    rrd_id INTEGER UNIQUE NOT NULL,
    nm_id INTEGER NOT NULL,
    -- ... остальные поля ...

    -- Новые поля (Приоритет 1)
    ppvz_spp_prc REAL,
    retail_price_withdisc_rub REAL,
    acquiring_fee REAL,
    acquiring_percent REAL,

    -- Новые поля (Приоритет 2)
    seller_promo_id INTEGER,
    seller_promo_discount REAL,
    loyalty_id INTEGER,
    loyalty_discount REAL,
    uuid_promocode TEXT,

    -- Новые поля (Приоритет 3)
    storage_fee REAL,
    deduction REAL,
    acceptance REAL,
    bonus_type_name TEXT,
    penalty REAL,

    created_at TEXT DEFAULT CURRENT_TIMESTAMP
);

-- Мигрируем данные
INSERT INTO sales_v2 (rrd_id, nm_id, /* ... */)
SELECT rrd_id, nm_id, /* ... */ FROM sales;

-- Заменяем таблицу
DROP TABLE sales;
ALTER TABLE sales_v2 RENAME TO sales;
```

---

## Заключение

Текущая схема покрывает **~69% полей WB API**, чего достаточно для:
- ✅ Базовой аналитики продаж
- ✅ Анализа доставки (FBW vs FBS)
- ✅ Служебных записей (логистика, издержки)
- ✅ Resume mode и пагинации

Для более глубокой финансовой аналитики рекомендуется добавить поля **Приоритета 1**.

---

**Дата создания**: 2026-03-19
**Версия API**: WB Statistics API v5 (`reportDetailByPeriod`)

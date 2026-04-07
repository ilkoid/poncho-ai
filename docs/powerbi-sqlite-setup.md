# Инструкция для администраторов: Подключение PowerBI к SQLite

---

## Контекст и архитектура

**Задача**: Обеспечить доступ Microsoft PowerBI к данным SQLite базы для построения дашбордов и отчётов.

**Архитектура решения**:

```
┌─────────────────────────────────┐              ┌─────────────────────────────────┐
│  Windows Server                 │              │  Linux Ubuntu (WSL2 или VPS)    │
│                                 │   SMB/CIFS   │                                 │
│  PowerBI Desktop                │◄─────────────►│  Рабочая БД: wb-sales.db        │
│  ├─ SQLite ODBC Driver          │  (сетевая    │  Расположение:                  │
│  ├─ Подключённый диск Z:        │   шара)      │  /home/ilkoid/.../db/           │
│  └─ Отчёты .pbix                │              │                                 │
│                                 │              │  Go-утилиты пишут данные:       │
│  Опционально:                   │              │  ├─ download-wb-sales           │
│  On-premises Data Gateway       │              │  ├─ download-wb-cards           │
│  (для PowerBI Service облака)   │              │  ├─ download-wb-promotion       │
│                                 │              │  └─ download-wb-funnel + др.    │
└─────────────────────────────────┘              └─────────────────────────────────┘
```

**Как это работает**:

1. На Linux папка с базой данных расшаривается через Samba (SMB/CIFS)
2. Windows Server подключает эту шару как сетевой диск (Z:)
3. На Windows ставится SQLite ODBC драйвер — он умеет читать .db файлы
4. PowerBI подключается к базе через ODBC как к обычному источнику данных
5. База данных в режиме WAL (Write-Ahead Logging) — позволяет параллельно читать из PowerBI и писать из Go-утилит без блокировок

**Важно**: SQLite — файловая база. ODBC драйвер читает файл напрямую по сети. Никакого сервера БД поднимать не нужно.

---

## Шаг 1: Настройка Linux (Ubuntu) — Samba шара

### 1.1 Установка Samba

```bash
sudo apt update
sudo apt install samba samba-common-bin -y
```

Проверка:

```bash
smbd --version
# Должно показать версию, например: Version 4.15.x
```

### 1.2 Создание пользователя Samba

```bash
# Пользователь ОС уже существует (ilkoid), добавляем в Samba
sudo smbpasswd -a ilkoid
# Вводим пароль (рекомендуется отличный от системного)
```

### 1.3 Настройка шары

```bash
sudo nano /etc/samba/smb.conf
```

Добавить в конец файла:

```ini
[wb-data]
   comment = WB Analytics SQLite Database
   path = /home/ilkoid/go-workspace/src/poncho-ai/db
   valid users = ilkoid
   read only = yes
   browsable = yes
   create mask = 0644
   directory mask = 0755
   # Для корректной работы с SQLite файлами
   strict locking = no
   oplocks = no
   level2 oplocks = no
```

**Пояснение по настройкам**:

- `read only = yes` — PowerBI только читает, Go-утилиты пишут локально на Linux
- `strict locking = no` + `oplocks = no` — предотвращают проблемы с блокировками SQLite при сетевом доступе. **Это критически важно**, без этих настроек ODBC драйвер может получать ошибки "database is locked"
- `valid users` — ограничиваем доступ по пользователю

### 1.4 Перезапуск Samba

```bash
sudo systemctl restart smbd nmbd
sudo systemctl enable smbd nmbd
```

### 1.5 Проверка (на Linux)

```bash
# Проверить что шара видна
smbclient -L localhost -U ilkoid
# Должен показать wb-data в списке

# Проверить подключение
smbclient //localhost/wb-data -U ilkoid
# Ввести пароль, затем: ls
# Должен показать файлы .db
```

### 1.6 Firewall

```bash
# Если используется ufw
sudo ufw allow from <windows-server-ip> to any app Samba
# Или просто:
sudo ufw allow samba
```

---

## Шаг 2: Настройка Windows Server — сетевой диск

### 2.1 Подключение сетевого диска

**Вариант через GUI**:

1. Открой File Explorer → This PC → Map network drive
2. Drive: `Z:`
3. Folder: `\\<LINUX-IP>\wb-data`
4. Галка: "Reconnect at sign-in"
5. Галка: "Connect using different credentials"
6. Ввести: пользователь `ilkoid`, пароль от Samba

**Вариант через PowerShell** (для автоматизации):

```powershell
# Сохранить credentials
cmdkey /add:<LINUX-IP> /user:ilkoid /pass:<samba-password>

# Подключить диск
net use Z: \\<LINUX-IP>\wb-data /persistent:yes
```

### 2.2 Проверка

1. Открыть File Explorer → диск Z:
2. Убедиться что видны файлы `wb-sales.db`, `wb-cards.db` и т.д.
3. Попробовать скопировать один файл локально — проверить что читается

---

## Шаг 3: Установка SQLite ODBC драйвера на Windows

### 3.1 Скачивание

Скачать с официального сайта:

**URL**: `https://www.ch-werner.de/sqliteodbc/`

Нужен файл: **sqliteodbc_w64.exe** (64-bit)

> Если URL недоступен, альтернативный источник: поиск "SQLite ODBC Driver Werner"

### 3.2 Установка

1. Запустить `sqliteodbc_w64.exe` от имени администратора
2. Установить со стандартными настройками
3. Перезагрузка **не требуется**

### 3.3 Проверка установки

1. Открой: **ODBC Data Source Administrator** (64-bit)
   - Путь: Control Panel → Administrative Tools → ODBC Data Sources (64-bit)
   - Или Win+R: `odbcad32`
2. Вкладка **Drivers** — должны быть:
   - `SQLite3 ODBC Driver`
   - `SQLite ODBC Driver` (версия 2.x)
3. Нужен именно **SQLite3 ODBC Driver** (для современных баз)

### 3.4 Создание DSN (опционально, но рекомендуется)

На вкладке **System DSN** → **Add**:

- Driver: `SQLite3 ODBC Driver`
- Data Source Name: `WB-Analytics`
- Database: `Z:\wb-sales.db` *(указываем полный путь)*
- Options → Timeout: `30`

---

## Шаг 4: Подключение PowerBI

### 4.1 PowerBI Desktop — подключение

1. Открыть PowerBI Desktop
2. **Get Data** → **More...** → **ODBC**
3. Выбрать DSN `WB-Analytics` (если создан)
   - Или ввести connection string: `DRIVER={SQLite3 ODBC Driver};DATABASE=Z:\wb-sales.db`
4. Нажать **Connect**
5. В Navigator появятся все таблицы базы:
   - `sales`, `fbw_sales`
   - `funnel_metrics_daily`, `funnel_metrics_aggregated`
   - `campaigns`, `campaign_stats_daily`
   - `stocks_daily_warehouses`
   - `products`, `cards`, `product_prices`
   - `feedbacks`, `questions`
   - `region_sales`
   - и другие

### 4.2 Проверка данных

В PowerBI Navigator:

1. Выбрать таблицу `sales`
2. Проверить что данные отображаются
3. Колонка `sale_dt` должна распознаваться как дата
4. Если PowerBI предлагает "Apply changes" — согласиться

---

## Шаг 5: Настройка обновления (для расписания)

### Если PowerBI Desktop (локально)

Данные обновляются по кнопке **Refresh** в PowerBI. Никаких дополнительных настроек.

### Если PowerBI Service (облачная публикация)

Нужен **On-premises Data Gateway**:

1. На Windows Server скачать и установить: **Microsoft On-premises Data Gateway**
   - Скачать из: Power BI Service → Settings → Manage gateways → Download
2. Зарегистрировать gateway в PowerBI Service
3. В настройках gateway добавить ODBC источник `WB-Analytics`
4. В PowerBI Service настроить scheduled refresh (до 8 раз/день)

---

## Шаг 6: Верификация — чеклист

| # | Проверка | Команда/Действие | Ожидаемый результат |
|---|----------|-------------------|---------------------|
| 1 | Samba работает | `smbclient -L <linux-ip> -U ilkoid` | Видна шара `wb-data` |
| 2 | Диск подключён | File Explorer → Z: | Видны `.db` файлы |
| 3 | ODBC драйвер | `odbcad32` → Drivers | `SQLite3 ODBC Driver` |
| 4 | DSN создан | `odbcad32` → System DSN | `WB-Analytics` |
| 5 | PowerBI подключается | Get Data → ODBC | Navigator показывает таблицы |
| 6 | Данные читаются | Выбрать `sales` в Navigator | Данные с датами |
| 7 | Параллельная работа | Запустить downloader + Refresh в PowerBI | Нет ошибок "database locked" |

---

## Возможные проблемы и решения

| Проблема | Решение |
|----------|---------|
| "database is locked" в PowerBI | Проверить WAL mode: на Linux выполнить `sqlite3 wb-sales.db "PRAGMA journal_mode;"` → должно быть `wal`. Если `delete` — Go-утилита не включила WAL |
| Диск Z: не переподключается после ребута | Проверить `cmdkey /list` — должны быть сохранены credentials для Linux IP |
| Медленная загрузка больших таблиц | В ODBC DSN → Options → поставить `BusyTimeout=30000` (30 сек ожидания) |
| PowerBI не видит новые данные | Нажать Refresh. Если через Service — проверить scheduled refresh в gateway |
| "Unable to open database file" | Проверить что файл не повреждён: `sqlite3 Z:\wb-sales.db "PRAGMA integrity_check;"` |

---

## Итоговая схема

```
Linux Ubuntu                          Windows Server
─────────────────────                 ─────────────────────

Go-утилиты ──запись──► wb-sales.db   PowerBI Desktop
         (локально)       │          ┌─ ODBC Driver
                          │          ├─ Z: ← SMB/CIFS ←┘
                     Samba шара     ├─ .pbix отчёты
                     (read only)    └─ Refresh → актуальные данные
```

**Ключевой принцип**: Linux — master (пишет), Windows — reader (читает через SMB+ODBC). WAL mode обеспечивает параллельность без блокировок.

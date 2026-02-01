#!/usr/bin/env python3
"""
Ozon Parser — Утилита для извлечения данных с Ozon используя headless браузер.

Автор: Claude Code
Дата: 2026-01-26
"""

import argparse
import json
import random
import sys
from pathlib import Path
from urllib.parse import urlencode, urlparse, parse_qs

from playwright.sync_api import sync_playwright, TimeoutError as PlaywrightTimeoutError
from bs4 import BeautifulSoup


# CSS-селекторы для Ozon (могут меняться, требуют обновления)
SELECTORS = {
    # Страница товара
    "product": {
        "title": "h1[data-widget='webProductHeading']",
        "price": ".tsHeadline500Medium",
        "old_price": ".tsBodyControlLergeStrike",
        "discount": ".tsCaptionBold",
        "rating": "[itemprop='ratingValue']",
        "reviews": "[itemprop='reviewCount']",
        "brand": ".a6c6",
        "seller": ".a0o6",
        "description": ".pda6",
    },
    # Страница категории / поиска
    "listing": {
        "items": ".widget-search-result-container .tile-wrapper",
        "title": ".tsBody500Medium",
        "price": ".tsHeadline500Medium",
        "old_price": ".tsBodyControlLergeStrike",
        "discount": ".tsCaptionBold",
        "rating": "[itemprop='ratingValue']",
        "reviews": "[itemprop='reviewCount']",
        "link": "a",
    }
}


# User Agent'ы ТОЛЬКО для Chromium-based браузеров (consistency check!)
USER_AGENTS = [
    # Chrome
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36",
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/118.0.0.0 Safari/537.36",
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36",
    # Opera (Chromium-based)
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/118.0.0.0 Safari/537.36 OPR/104.0.0.0",
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36 OPR/105.0.0.0",
    # Edge (Chromium-based)
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.0.0",
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.0.0",
    # Brave (Chromium-based)
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Brave/120.0.0.0",
    # Yandex Browser (Chromium-based)
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 YaBrowser/24.1.0.0",
]


def detect_timezone_by_ip():
    """Определяет timezone по IP через API."""
    try:
        import requests
        response = requests.get('https://ipapi.co/json/', timeout=5)
        if response.status_code == 200:
            data = response.json()
            timezone = data.get('timezone', 'Europe/Moscow')
            print(f"🌍 Определён timezone по IP: {timezone}")
            return timezone
    except Exception as e:
        print(f"⚠️  Не удалось определить timezone: {e}")
    return 'Europe/Moscow'  # Дефолт для России


def generate_random_fingerprint(detect_timezone: bool = True):
    """Генерирует случайный отпечаток браузера с консистентными параметрами."""
    # Определяем timezone по IP (для консистентности)
    if detect_timezone:
        timezone = detect_timezone_by_ip()
    else:
        timezone = 'Europe/Moscow'

    # Выбираем платформу и timezone должны быть консистентны
    # Если timezone России — используем Windows/Mac больше всего
    is_russian_tz = 'Moscow' in timezone or 'Kaliningrad' in timezone or 'Samara' in timezone or 'Yekaterinburg' in timezone

    if is_russian_tz:
        platforms = ['Win32', 'Win32', 'Win32', 'MacIntel', 'Linux x86_64']  # Больше Win32 для РФ
        # Все USER_AGENTS теперь Chromium-based, не фильтруем
    else:
        platforms = ['Win32', 'MacIntel', 'Linux x86_64']

    # Все UA в списке Chromium-based, используем все
    user_agents = USER_AGENTS[:]

    # Разрешения экранов (популярные)
    resolutions = [
        (1920, 1080),  # Full HD - самый популярный
        (1920, 1080),
        (1920, 1080),
        (1920, 1080),
        (2560, 1440),  # 2K
        (1366, 768),   # Laptop
        (1536, 864),   # Laptop 2
        (1440, 900),   # Mac
        (1680, 1050),  # Widescreen
    ]

    # Количество ядер CPU (реалистичные значения)
    core_counts = [4, 6, 8, 8, 12, 16]

    # Память (GB) - должна соответствовать core count
    memory_map = {4: 8, 6: 16, 8: 16, 12: 32, 16: 32}
    cores = random.choice(core_counts)

    width, height = random.choice(resolutions)
    avail_width = width - random.randint(0, 100)
    avail_height = height - random.randint(40, 150)

    platform = random.choice(platforms)

    # Консистентность: MacIntel → macOS timezone, Win32 → Windows
    if platform == 'MacIntel' and is_russian_tz:
        timezone = 'Europe/Moscow'
    elif platform == 'Win32' and is_russian_tz:
        timezone = timezone  # Оставляем как есть

    return {
        "user_agent": random.choice(user_agents),
        "screen": {
            "width": width,
            "height": height,
            "avail_width": avail_width,
            "avail_height": avail_height,
            "color_depth": 24,
            "pixel_depth": 24,
        },
        "viewport": {
            "width": width,
            "height": height - 40,  # Минус браузерный интерфейс
        },
        "navigator": {
            "hardware_concurrency": cores,
            "device_memory": memory_map[cores],
            "max_touch_points": 0 if platform == 'Win32' else random.choice([0, 5]),  # Windows обычно без touch
            "platform": platform,
            "language": "ru-RU",
            "languages": ["ru-RU", "ru", "en-US", "en"],
            "vendor": "Google Inc.",  # Все Chromium браузеры используют Blink от Google
        },
        "timezone": timezone,
        "locale": "ru-RU",
    }


def get_fingerprint_script(fingerprint):
    """Генерирует JavaScript код для инъекции отпечатка."""
    return f"""
    // Navigator свойства - скрываем webdriver
    Object.defineProperty(navigator, 'webdriver', {{
        get: () => undefined
    }});

    Object.defineProperty(navigator, 'hardwareConcurrency', {{
        get: () => {fingerprint['navigator']['hardware_concurrency']},
        configurable: true
    }});

    Object.defineProperty(navigator, 'deviceMemory', {{
        get: () => {fingerprint['navigator']['device_memory']},
        configurable: true
    }});

    Object.defineProperty(navigator, 'maxTouchPoints', {{
        get: () => {fingerprint['navigator']['max_touch_points']},
        configurable: true
    }});

    Object.defineProperty(navigator, 'platform', {{
        get: () => "{fingerprint['navigator']['platform']}",
        configurable: true
    }});

    Object.defineProperty(navigator, 'language', {{
        get: () => "{fingerprint['navigator']['language']}",
        configurable: true
    }});

    Object.defineProperty(navigator, 'languages', {{
        get: () => {fingerprint['navigator']['languages']},
        configurable: true
    }});

    // Vendor для всех Chromium браузеров
    Object.defineProperty(navigator, 'vendor', {{
        get: () => "Google Inc.",
        configurable: true
    }});

    Object.defineProperty(navigator, 'product', {{
        get: () => "Gecko",
        configurable: true
    }});

    // Screen свойства
    Object.defineProperty(screen, 'width', {{
        get: () => {fingerprint['screen']['width']},
        configurable: true
    }});

    Object.defineProperty(screen, 'height', {{
        get: () => {fingerprint['screen']['height']},
        configurable: true
    }});

    Object.defineProperty(screen, 'availWidth', {{
        get: () => {fingerprint['screen']['avail_width']},
        configurable: true
    }});

    Object.defineProperty(screen, 'availHeight', {{
        get: () => {fingerprint['screen']['avail_height']},
        configurable: true
    }});

    Object.defineProperty(screen, 'colorDepth', {{
        get: () => {fingerprint['screen']['color_depth']},
        configurable: true
    }});

    Object.defineProperty(screen, 'pixelDepth', {{
        get: () => {fingerprint['screen']['pixel_depth']},
        configurable: true
    }});

    // WebGL fingerprint (подмена под реальный GPU)
    const getParameter = WebGLRenderingContext.prototype.getParameter;
    WebGLRenderingContext.prototype.getParameter = function(parameter) {{
        if (parameter === 37445) {{ // UNMASKED_VENDOR_WEBGL
            return 'Intel Inc.';
        }}
        if (parameter === 37446) {{ // UNMASKED_RENDERER_WEBGL
            return 'Intel Iris OpenGL Engine';
        }}
        if (parameter === 7938) {{ // MAX_TEXTURE_SIZE
            return 16384;
        }}
        if (parameter === 7937) {{ // MAX_VIEWPORT_DIMS
            return [16384, 16384];
        }}
        return getParameter.call(this, parameter);
    }};

    // Canvas fingerprint noise (минимальный, для уникальности)
    const originalToDataURL = HTMLCanvasElement.prototype.toDataURL;
    HTMLCanvasElement.prototype.toDataURL = function(type) {{
        const context = this.getContext('2d');
        if (context && this.width > 0 && this.height > 0) {{
            const imageData = context.getImageData(0, 0, Math.min(this.width, 100), Math.min(this.height, 100));
            for (let i = 0; i < imageData.data.length; i += 4) {{
                imageData.data[i] += Math.random() > 0.5 ? 1 : 0;
            }}
        }}
        return originalToDataURL.apply(this, arguments);
    }};

    // Chrome detection (эмулируем chrome объект)
    Object.defineProperty(window, 'chrome', {{
        get: () => ({{
            runtime: {{}},
            loadTimes: function() {{}},
            csi: function() {{}},
            app: {{}}
        }}),
        configurable: true
    }});

    // Permissions API
    const originalQuery = window.navigator.permissions.query;
    if (originalQuery) {{
        window.navigator.permissions.query = (parameters) => (
            parameters.name === 'notifications' ?
                Promise.resolve({{ state: Notification.permission }}) :
                originalQuery(parameters)
        );
    }}

    // Plugins (эмулируем базовые плагины)
    Object.defineProperty(navigator, 'plugins', {{
        get: () => [
            {{ name: 'Chrome PDF Plugin', filename: 'internal-pdf-viewer' }},
            {{ name: 'Chrome PDF Viewer', filename: 'mhjfbmdgcfjbbpaeojofohoefgiehjai' }},
            {{ name: 'Native Client', filename: 'internal-nacl-plugin' }}
        ],
        configurable: true
    }});

    // Добавляем fake плагины
    navigator.plugins.length = 3;

    console.log('[Fingerprint] Applied consistent fingerprint for {fingerprint['timezone']}');
    """


class OzonParser:
    """Парсер для Ozon с использованием Playwright."""

    def __init__(self, headless: bool = True, timeout: int = 30000, seed: int = None):
        """
        Инициализация парсера.

        Args:
            headless: Запускать браузер без GUI
            timeout: Таймаут ожидания загрузки в мс
            seed: Seed для случайности (опционально, для воспроизводимости)
        """
        self.headless = headless
        self.timeout = timeout
        self.seed = seed

        # Если seed указан, фиксируем случайность
        if seed is not None:
            random.seed(seed)

        self.playwright = None
        self.browser = None
        self.page = None

    def __enter__(self):
        """Контекстный менеджер для автоматической очистки."""
        self.start()
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        """Закрытие браузера при выходе."""
        self.stop()

    def start(self):
        """Запуск браузера с рандомизацией отпечатка."""
        # Генерируем случайный отпечаток
        self.fingerprint = generate_random_fingerprint()

        print(f"🎭 Fingerprint: {self.fingerprint['navigator']['platform']} | "
              f"{self.fingerprint['screen']['width']}x{self.fingerprint['screen']['height']} | "
              f"{self.fingerprint['navigator']['hardware_concurrency']} cores | "
              f"{self.fingerprint['navigator']['device_memory']}GB RAM")

        self.playwright = sync_playwright().start()
        self.browser = self.playwright.chromium.launch(
            headless=self.headless,
            args=[
                '--no-sandbox',
                '--disable-setuid-sandbox',
                '--disable-blink-features=AutomationControlled',
                '--disable-dev-shm-usage',
                '--disable-web-security',
                '--disable-features=IsolateOrigins,site-per-process',
            ]
        )

        # Создаём страницу с настройками viewport
        self.page = self.browser.new_page(
            user_agent=self.fingerprint['user_agent'],
            viewport={
                'width': self.fingerprint['viewport']['width'],
                'height': self.fingerprint['viewport']['height']
            },
            locale='ru-RU',
            timezone_id=self.fingerprint['timezone'],
        )

        # Инъектируем скрипт для подмены отпечатка
        fingerprint_script = get_fingerprint_script(self.fingerprint)
        self.page.add_init_script(fingerprint_script)

        self.page.set_default_timeout(self.timeout)

        # Пауза для "прогрева" браузера - делаем его более естественным
        print("⏸️  Прогрев браузера...")
        self.page.wait_for_timeout(random.randint(2000, 4000))

    def stop(self):
        """Остановка браузера."""
        if self.page:
            self.page.close()
        if self.browser:
            self.browser.close()
        if self.playwright:
            self.playwright.stop()

    def _extract_text(self, selector: str, default: str = "") -> str:
        """Извлечь текст элемента по селектору."""
        try:
            element = self.page.query_selector(selector)
            if element:
                return element.inner_text().strip()
        except Exception:
            pass
        return default

    def _extract_attribute(self, selector: str, attr: str, default: str = "") -> str:
        """Извлечь атрибут элемента."""
        try:
            element = self.page.query_selector(selector)
            if element:
                return element.get_attribute(attr) or default
        except Exception:
            pass
        return default

    def parse_product(self, url: str) -> dict:
        """
        Парсинг страницы товара.

        Args:
            url: URL страницы товара

        Returns:
            Словарь с данными товара
        """
        print(f"🔍 Загрузка: {url}")

        try:
            print(f"⏳ Загружаю страницу: {url}")
            response = self.page.goto(url, wait_until="domcontentloaded", timeout=60000)
            print(f"✅ Ответ сервера: {response.status if response else 'None'}")

            # Дополнительное ожидание для JS рендеринга
            print("⏳ Ожидаю рендеринга контента (может занять до 30 сек)...")
            self.page.wait_for_timeout(5000)

            # Проверка на CAPTCHA с возможностью ручного ввода
            page_text = self.page.inner_text("body")
            captcha_indicators = [
                "доступ ограничен",
                "captcha",
                "подтвердите, что вы не бот",
                "подтвердите что вы не бот",
                "передвиньте ползунок",
                "verify you are human",
                "human verification",
                "challenge",
            ]

            detected_captcha = any(indicator in page_text.lower() for indicator in captcha_indicators)

            if detected_captcha or len(page_text) < 200:
                print("⚠️  Обнаружена CAPTCHA!")
                print(f"📄 Текст на странице: {page_text[:200]}")
                print("💡 Пожалуйста, пройдите CAPTCHA в браузере...")

                # Ждём пока пользователь пройдёт CAPTCHA (до 2 минут)
                print("⏳ Ожидаю прохождения CAPTCHA (до 120 секунд)...")

                for i in range(120):
                    self.page.wait_for_timeout(1000)
                    current_text = self.page.inner_text("body")

                    # Проверяем прошла ли CAPTCHA
                    still_captcha = any(indicator in current_text.lower() for indicator in captcha_indicators)

                    if not still_captcha and len(current_text) > 500:
                        print(f"✅ CAPTCHA пройдена! (через {i+1} сек)")
                        self.page.wait_for_timeout(2000)  # Дополнительная пауза после CAPTCHA
                        break

                    if i == 59 and still_captcha:
                        print(f"⏰ Прошла минута, проверяю снова...")

                    if i >= 119:
                        print("⏰ Время вышло! CAPTCHA не была пройдена.")
                        return {"error": "CAPTCHA timeout - пользователь не прошёл проверку"}

            # Сохраняем скриншот для отладки
            screenshot_path = "/tmp/ozon_debug.png"
            self.page.screenshot(path=screenshot_path)
            print(f"📸 Скриншот сохранён: {screenshot_path}")

            # Проверка содержимого страницы
            page_text = self.page.inner_text("body")
            print(f"📄 Длина текста на странице: {len(page_text)} символов")

            if len(page_text) < 500:
                print("⚠️  Текст на странице слишком короткий, возможно контент не загрузился")
                print(f"📄 Первые 500 символов: {page_text[:500]}")

            print("✅ Проверка пройдена, извлекаю данные...")

        except PlaywrightTimeoutError:
            print("⚠️  Таймаут загрузки, продолжаем...")
        except Exception as e:
            return {"error": f"Не удалось загрузить страницу: {e}"}

        # Извлекаем данные
        data = {
            "url": url,
            "title": self._extract_text(SELECTORS["product"]["title"]),
            "price": self._extract_text(SELECTORS["product"]["price"]),
            "old_price": self._extract_text(SELECTORS["product"]["old_price"]),
            "discount": self._extract_text(SELECTORS["product"]["discount"]),
            "rating": self._extract_text(SELECTORS["product"]["rating"]),
            "reviews": self._extract_text(SELECTORS["product"]["reviews"]),
            "brand": self._extract_text(SELECTORS["product"]["brand"]),
            "seller": self._extract_text(SELECTORS["product"]["seller"]),
            "description": self._extract_text(SELECTORS["product"]["description"]),
        }

        # Очистка цены от символов
        if data["price"]:
            import re
            price_match = re.search(r'[\d\s]+', data["price"])
            if price_match:
                data["price_number"] = int(price_match.group().replace(' ', ''))

        print(f"📦 Данные извлечены: {data.get('title', 'без названия')}")

        # Пауза перед закрытием браузера (как запрошено пользователем)
        print("⏸️  Пауза перед закрытием браузера (5 сек)...")
        self.page.wait_for_timeout(5000)

        return data

    def parse_listing(self, url: str, limit: int = 20) -> list:
        """
        Парсинг страницы категории/поиска.

        Args:
            url: URL страницы категории
            limit: Максимальное количество товаров

        Returns:
            Список словарей с данными товаров
        """
        print(f"🔍 Загрузка категории: {url}")

        try:
            self.page.goto(url, wait_until="networkidle")
            self.page.wait_for_timeout(3000)
        except PlaywrightTimeoutError:
            print("⚠️  Таймаут загрузки, продолжаем...")
        except Exception as e:
            return [{"error": f"Не удалось загрузить страницу: {e}"}]

        items = []
        selectors = SELECTORS["listing"]

        # Пробуем найти элементы
        try:
            item_elements = self.page.query_selectors_all(selectors["items"])
            print(f"📦 Найдено товаров: {len(item_elements)}")

            for i, elem in enumerate(item_elements[:limit]):
                item_data = {
                    "position": i + 1,
                    "title": "",
                    "price": "",
                    "old_price": "",
                    "discount": "",
                    "rating": "",
                    "reviews": "",
                    "link": "",
                }

                # Пытаемся извлечь данные из элемента
                try:
                    # Название
                    title_elem = elem.query_selector(selectors["title"])
                    if title_elem:
                        item_data["title"] = title_elem.inner_text().strip()

                    # Цена
                    price_elem = elem.query_selector(selectors["price"])
                    if price_elem:
                        item_data["price"] = price_elem.inner_text().strip()

                    # Ссылка
                    link_elem = elem.query_selector(selectors["link"])
                    if link_elem:
                        href = link_elem.get_attribute("href")
                        if href:
                            item_data["link"] = href if href.startswith("http") else f"https://www.ozon.ru{href}"

                except Exception as e:
                    item_data["error"] = str(e)

                items.append(item_data)

        except Exception as e:
            return [{"error": f"Не удалось найти товары: {e}"}]

        return items

    def search(self, query: str, limit: int = 10) -> list:
        """
        Поиск товаров по запросу.

        Args:
            query: Поисковый запрос
            limit: Максимальное количество товаров

        Returns:
            Список словарей с данными товаров
        """
        search_url = f"https://www.ozon.ru/search/?{urlencode({'text': query})}"
        return self.parse_listing(search_url, limit)


def save_json(data, filepath: str):
    """Сохранить данные в JSON файл."""
    with open(filepath, 'w', encoding='utf-8') as f:
        json.dump(data, f, ensure_ascii=False, indent=2)
    print(f"💾 Сохранено: {filepath}")


def main():
    """Главная функция."""
    parser = argparse.ArgumentParser(
        description="Ozon Parser — утилита для извлечения данных с Ozon",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Примеры:
  python ozon_parser.py product --url "https://www.ozon.ru/product/futbolka-cosmo-2543561282/"
  python ozon_parser.py category --url "https://www.ozon.ru/category/hlopkovye-muzhskie-futbolki/" --limit 20
  python ozon_parser.py search --query "футболки мужские хлопковые" --limit 10
        """
    )

    parser.add_argument("command", choices=["product", "category", "search"],
                       help="Команда: product (страница товара), category (категория), search (поиск)")
    parser.add_argument("--url", help="URL страницы")
    parser.add_argument("--query", help="Поисковый запрос")
    parser.add_argument("--limit", type=int, default=20, help="Лимит товаров (для category/search)")
    parser.add_argument("--output", "-o", help="Файл для сохранения JSON")
    parser.add_argument("--no-headless", action="store_true", help="Показать браузер (для отладки)")
    parser.add_argument("--seed", type=int, help="Seed для воспроизводимости отпечатка браузера")

    args = parser.parse_args()

    # Проверка аргументов
    if args.command in ["product", "category"] and not args.url:
        parser.error(f"Команда '{args.command}' требует --url")
    if args.command == "search" and not args.query:
        parser.error("Команда 'search' требует --query")

    # Запуск парсера
    with OzonParser(headless=not args.no_headless, seed=args.seed) as ozon:
        result = None

        if args.command == "product":
            result = ozon.parse_product(args.url)
        elif args.command == "category":
            result = ozon.parse_listing(args.url, args.limit)
        elif args.command == "search":
            result = ozon.search(args.query, args.limit)

        # Вывод результата
        print(json.dumps(result, ensure_ascii=False, indent=2))

        # Сохранение в файл
        if args.output:
            save_json(result, args.output)
        else:
            # Автоматическое сохранение
            output_dir = Path(__file__).parent / "output"
            output_dir.mkdir(exist_ok=True)
            default_file = output_dir / f"{args.command}_result.json"
            save_json(result, str(default_file))


if __name__ == "__main__":
    main()

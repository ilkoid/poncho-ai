# Models Configuration
models:
  default_chat: "glm-4.6"   # Текстовая модель для чата (ReAct loop)
  default_vision: "glm-4.6v-flash" # Vision модель для анализа изображений
  definitions:
    glm-4.6:
      provider: "zai"
      model_name: "glm-4.6"
      api_key: "${ZAI_API_KEY}"
      base_url: "https://api.z.ai/api/paas/v4"
      max_tokens: 2000
      temperature: 0.5
      timeout: "120s"
      thinking: "enabled"
    glm-4.6v-flash:
      provider: "zai"
      model_name: "glm-4.6v-flash"
      api_key: "${ZAI_API_KEY}"
      base_url: "https://api.z.ai/api/paas/v4"
      max_tokens: 4000
      temperature: 0.1
      timeout: "120s"

# S3 Configuration
s3:
  endpoint: "storage.yandexcloud.net"
  region: "ru-central1"
  bucket: "plm-ai"
  access_key: "${S3_ACCESS_KEY}"
  secret_key: "${S3_SECRET_KEY}"
  use_ssl: true

# Image Settings
image_processing:
  max_width: 800
  quality: 90

# App Settings
app:
  debug: true
  prompts_dir: "./prompts"

# Классификация файлов в бакете
file_rules:
  - tag: "sketch"
    patterns:
      - "*.jpg"
      - "*.jpeg"
      - "*.png"
    required: true

  - tag: "plm_data"
    patterns:
      - "*.json"
    required: true

  - tag: "marketing"
    patterns:
      - "*seo*"
      - "*keys*"

# Wildberries API (используется для дефолтных значений)
wb:
  api_key: "${WB_API_KEY}"
  # Rate limiting параметры (опционально, есть дефолтные значения)
# это теперь в tools:
#  base_url: "https://content-api.wildberries.ru"  # Default
#  rate_limit: 100      # Default: запросов в минуту
#  burst_limit: 5       # Default: burst capacity
  retry_attempts: 3    # Default: retry attempts
  timeout: "30s"       # Default: HTTP timeout

# Tools Configuration
#
# Каждый tool имеет свою конфигурацию с параметрами:
#   - enabled: включён ли tool
#   - description: описание для LLM (function calling)
#   - endpoint: базовый URL API (если применимо)
#   - path: путь к endpoint (если применимо)
#   - rate_limit: запросов в минуту (если применимо)
#   - burst: burst capacity (если применимо)
#   - default_take: дефолтное количество записей (для feedbacks)
#   - post_prompt: путь к post-prompt файлу (опционально)
#
# Если tool не указан в этом списке или enabled=false, он не регистрируется.

tools:
  # === WB Content API Tools ===
  search_wb_products:
    enabled: false
    description: "Ищет товары Wildberries по артикулам поставщика (vendor code/supplier article) и возвращает их nmID. Использует Content API (категория Promotion). ВАЖНО: видит только товары продавца, которому принадлежит API токен (до 100 карточек). Для поиска товаров других продавцов используйте get_wb_feedbacks или get_wb_questions."
    endpoint: "https://content-api.wildberries.ru"
    rate_limit: 100
    burst: 5
    post_prompt: "agent_system.yaml"

  get_wb_parent_categories:
    enabled: false
    description: "Возвращает список родительских категорий Wildberries (например: Женщинам, Электроника). Используй это, чтобы найти ID категории."
    endpoint: "https://content-api.wildberries.ru"
    path: "/content/v2/object/parent/all"
    rate_limit: 100
    burst: 5
    timeout: "30s"
    post_prompt: "parent_categories_analysis.yaml"

  get_wb_subjects:
    enabled: false
    description: "Возвращает список предметов (подкатегорий) для заданной родительской категории с пагинацией."
    endpoint: "https://content-api.wildberries.ru"
    path: "/content/v2/object/all"
    rate_limit: 100
    burst: 5
    post_prompt: "subjects_analysis.yaml"

  ping_wb_api:
    enabled: false
    description: "Проверяет доступность Wildberries Content API. Возвращает статус сервиса, timestamp и информацию об ошибках (например, неверный API ключ, недоступность сети). Используй для диагностики перед другими операциями с WB."
    endpoint: "https://content-api.wildberries.ru"
    path: "/ping"
    rate_limit: 100
    burst: 5
    post_prompt: "api_health_report.yaml"

  # === WB Feedbacks API Tools (новые) ===
  get_wb_feedbacks:
    enabled: false
    description: "Возвращает отзывы на товары Wildberries с пагинацией. Позволяет фильтровать по отвеченности (isAnswered: true/false) и артикулу (nmID)."
    endpoint: "https://feedbacks-api.wildberries.ru"
    path: "/api/v1/feedbacks"
    rate_limit: 60    # 1 req/sec
    burst: 3
    default_take: 100
    post_prompt: "agent_system.yaml"

  get_wb_questions:
    enabled: false
    description: "Возвращает вопросы о товарах Wildberries с пагинацией. Позволяет фильтровать по отвеченности (isAnswered: true/false) и артикулу (nmID)."
    endpoint: "https://feedbacks-api.wildberries.ru"
    path: "/api/v1/questions"
    rate_limit: 60
    burst: 3
    default_take: 100
    post_prompt: "agent_system.yaml"

  get_wb_new_feedbacks_questions:
    enabled: false
    description: "Проверяет наличие новых отзывов и вопросов на Wildberries. Возвращает количество непрочитанных отзывов и вопросов."
    endpoint: "https://feedbacks-api.wildberries.ru"
    path: "/api/v1/new-feedbacks-questions"
    rate_limit: 60
    burst: 3
    post_prompt: "agent_system.yaml"

  get_wb_unanswered_feedbacks_counts:
    enabled: false
    description: "Возвращает количество неотвеченных отзывов на Wildberries (общее и за сегодня). Используй для мониторинга качества сервиса."
    endpoint: "https://feedbacks-api.wildberries.ru"
    rate_limit: 60
    burst: 3
    post_prompt: "agent_system.yaml"

  get_wb_unanswered_questions_counts:
    enabled: false
    description: "Возвращает количество неотвеченных вопросов на Wildberries (общее и за сегодня). Используй для мониторинга качества сервиса."
    endpoint: "https://feedbacks-api.wildberries.ru"
    rate_limit: 60
    burst: 3
    post_prompt: "agent_system.yaml"

  # === WB Dictionary Tools ===
  wb_colors:
    enabled: false
    description: "Ищет цвета в справочнике Wildberries по подстроке. Возвращает топ-N подходящих цветов с названиями и базовыми цветами (parentName). Используй для точного определения цвета товара из описания или анализа изображения."
    post_prompt: "agent_system.yaml"

  wb_countries:
    enabled: false
    description: "Возвращает справочник стран производства для Wildberries. Используй для выбора страны происхождения товара при создании карточки."
    post_prompt: "agent_system.yaml"

  wb_genders:
    enabled: false
    description: "Возвращает справочник значений пола (gender/kind) для Wildberries. Используй для выбора пола товара при создании карточки."
    post_prompt: "agent_system.yaml"

  wb_seasons:
    enabled: false
    description: "Возвращает справочник сезонов для Wildberries. Используй для выбора сезона товара при создании карточки."
    post_prompt: "agent_system.yaml"

  wb_vat_rates:
    enabled: false
    description: "Возвращает справочник ставок НДС (VAT) для Wildberries. Используй для выбора ставки НДС товара при создании карточки."
    post_prompt: "agent_system.yaml"

  # === WB Service Tools ===
  reload_wb_dictionaries:
    enabled: false
    description: "Перезагружает справочники Wildberries из API. Возвращает количество записей в каждом справочнике. Используй для проверки доступности API или после изменения данных. ВНИМАНИЕ: не обновляет состояние агента, только возвращает данные."
    endpoint: "https://content-api.wildberries.ru"
    rate_limit: 100
    burst: 5
    post_prompt: "agent_system.yaml"

  # === S3 Basic Tools ===
  list_s3_files:
    enabled: true
    description: "Возвращает список файлов в S3 хранилище по указанному пути (префиксу). Используй это, чтобы найти артикулы или проверить наличие файлов."
    post_prompt: "agent_system.yaml"

  read_s3_object:
    enabled: true
    description: "Читает содержимое файла из S3. Поддерживает текстовые файлы (JSON, TXT, MD). Не используй для картинок."
    post_prompt: "agent_system.yaml"

  read_s3_image:
    enabled: true
    description: "Скачивает изображение из S3, оптимизирует его (resize) и возвращает в формате Base64. Используй это для Vision-анализа."
    post_prompt: "agent_system.yaml"

  # === Planner Tools ===
  plan_add_task:
    enabled: true
    description: "Добавляет новую задачу в план действий. Указывай четкое описание того, что нужно выполнить."
    post_prompt: "agent_system.yaml"

  plan_mark_done:
    enabled: true
    description: "Отмечает задачу как выполненную. Используй когда задача успешно завершена."
    post_prompt: "agent_system.yaml"

  plan_mark_failed:
    enabled: true
    description: "Отмечает задачу как проваленную с указанием причины. Используй когда задачу невозможно выполнить по независимым причинам."
    post_prompt: "agent_system.yaml"

  plan_clear:
    enabled: true
    description: "Очищает весь план действий. Используй для удаления всех задач из списка."
    post_prompt: "agent_system.yaml"

  # === S3 Batch Tools ===
  classify_and_download_s3_files:
    enabled: true
    description: "Загружает все файлы артикула из S3 хранилища, классифицирует их по тегам (sketch, plm_data, marketing) и сохраняет в состояние. Выполняет ресайз изображений если включено в конфиге. Используй это для загрузки артикула перед анализом изображений."
    post_prompt: "agent_system.yaml"

  analyze_article_images_batch:
    enabled: true
    description: "Анализирует изображения из текущего артикула с помощью Vision LLM. Обрабатывает картинки параллельно в горутинах. Опционально фильтрует по тегу (sketch, plm_data, marketing). Используй это после classify_and_download_s3_files для анализа эскизов."
    post_prompt: "sketch_description_prompt.yaml"

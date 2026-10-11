#!/bin/bash
# API Specs Auto-Refresh — спеки WB (13 YAML) и Ozon (OpenAPI JSON) в docs/.
#
# Канал HTTP — прямой GET; челленджи и Ozon добираются браузером (CDP, Edge).
# Файлы обновляются, git-коммит делается руками:
#   git add docs/wb_api_swagger docs/ozon_api_swagger ../poncho-oz/docs/ozon_api_swagger
#   git commit -m "docs(swagger): автообновление спек WB и Ozon ($(date +%Y-%m-%d))"
#
# Usage:
#   bash download-api-specs.sh                 # HTTP → фолбэк CDP
#   bash download-api-specs.sh --http-only     # без браузера
#   bash download-api-specs.sh --cdp-only      # форс-проверка браузерного канала
#   bash download-api-specs.sh --dry-run       # скачать, не записывать

PONCHO="$(cd "$(dirname "$0")" && pwd)"
TOOL="$PONCHO/cmd/data-downloaders/download-api-specs"
mkdir -p "$PONCHO/logs"

LOG="$PONCHO/logs/api-specs-$(date +%Y-%m-%d).log"

go run "$TOOL" --config "$TOOL/config.yaml" "$@" 2>&1 | tee "$LOG"
exit "${PIPESTATUS[0]}"

#!/bin/bash
# build.sh — кросс-компиляция collect-card-characteristics v1.6
#
# Собирает бинарники под Mac (Intel + Apple Silicon) и Windows.
# Использует modernc.org/sqlite (pure Go, без CGo).
#
# Usage:
#   bash cmd/data-analyzers/collect-card-characteristics/build.sh
#
# Результат:
#   collect-card-characteristics_darwin_amd64
#   collect-card-characteristics_darwin_arm64
#   collect-card-characteristics_windows_amd64.exe

set -euo pipefail

VERSION="v1.6"
NAME="collect-card-characteristics"
DIR="./cmd/data-analyzers/collect-card-characteristics"

# Флаги сборки: -ldflags для встраивания версии, -trimpath для чистых путей
LDFLAGS="-s -w -X main.Version=${VERSION}"
BUILD_FLAGS="-trimpath"

echo "══════════════════════════════════════════════════"
echo "  collect-card-characteristics ${VERSION} — кросс-компиляция (с --export-all)"
echo "══════════════════════════════════════════════════"
echo ""

# Mac Intel
echo -n "  darwin/amd64...  "
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build ${BUILD_FLAGS} -ldflags "${LDFLAGS}" -o "${NAME}_darwin_amd64" ${DIR}
echo "✓"

# Mac Apple Silicon
echo -n "  darwin/arm64...  "
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build ${BUILD_FLAGS} -ldflags "${LDFLAGS}" -o "${NAME}_darwin_arm64" ${DIR}
echo "✓"

# Windows
echo -n "  windows/amd64... "
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ${BUILD_FLAGS} -ldflags "${LDFLAGS}" -o "${NAME}_windows_amd64.exe" ${DIR}
echo "✓"

echo ""
echo "──────────────────────────────────────────────────"
echo "  Результат:"
echo "──────────────────────────────────────────────────"
ls -lh ${NAME}_darwin_amd64 ${NAME}_darwin_arm64 ${NAME}_windows_amd64.exe
echo ""
echo "Готово!"

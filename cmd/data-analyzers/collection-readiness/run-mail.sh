#!/usr/bin/env bash
#
# run-mail.sh — собрать отчёт готовности карточек и отправить xlsx по почте.
#
# Делает cd в свою папку (рядом config.yaml), требует SMTP_PASSWORD в env,
# собирает отчёт и шлёт его вложением через pkg/email (секция email в config.yaml).
#
# Два режима:
#   mock  — отправить ДЕМО-xlsx (без обращения к PostgreSQL). Чистый дымовой тест
#           связки «SMTP-сервер + креды + письмо со вложением». Не нужны ни БД, ни PG-креды.
#   real  — собрать настоящий отчёт из wb_data_prod (нужен доступ к PostgreSQL:
#           PGHOST/PGUSER/PG_PWD и пр., см. dev_v2_postgres.md) и отправить его.
#
# Usage:
#   export SMTP_PASSWORD='ваш-пароль'
#   ./run-mail.sh mock                 # тест почты без БД
#   ./run-mail.sh real                 # реальный отчёт + отправка
#   SEASONS="Школа" OUT=/tmp/r.xlsx ./run-mail.sh real
#
# SMTP-подключение (host/port/from/...) вшито в config.yaml (email.smtp); единственный
# секрет — пароль, который берётся отсюда (SMTP_PASSWORD).
set -euo pipefail

# ─── секрет: пароль SMTP ───
: "${SMTP_PASSWORD:?Задайте SMTP_PASSWORD перед запуском: export SMTP_PASSWORD='ваш-пароль'}"

# ─── параметры (перекрываются env) ───
MODE="${1:-real}"                       # mock | real
SEASONS="${SEASONS:-Школа}"
COLLECTIONS="${COLLECTIONS:-}"          # напр. "CLASSIC 2026 girls Tween"; пусто → только сезоны
OUT="${OUT:-/tmp/readiness-mail.xlsx}"

# Скрипт лежит рядом с config.yaml и package main — переходим в свою папку,
# чтобы --config config.yaml и `go run .` разрешались корректно из любого cwd.
cd "$(dirname "$0")"

# Общий набор флагов.
COMMON=( --config config.yaml --xlsx "$OUT" --mail )
if [ -n "$COLLECTIONS" ]; then
	COMMON+=( --collections "$COLLECTIONS" )
else
	COMMON+=( --seasons "$SEASONS" )
fi

case "$MODE" in
	mock)
		echo ">>> Дымовой тест почты: демо-xlsx (без PostgreSQL) → ${OUT}"
		go run . --mock "${COMMON[@]}"
		;;
	real)
		# PG-подключение строится из env (BuildPgDSN: PGHOST/PGPORT/PGUSER + пароль из
		# $PG_PWD); в config.yaml полей host/user нет. Без PGUSER утилита молча берёт
		# дефолт 'postgres' и падает на auth — проверяем явно и fail-fast.
		miss=()
		for v in PGHOST PGPORT PGUSER PG_PWD; do
			[ -z "${!v:-}" ] && miss+=("$v")
		done
		if [ "${#miss[@]}" -gt 0 ]; then
			echo "ОШИБКА: не заданы PG-переменные: ${miss[*]}" >&2
			echo "Экспортируйте перед запуском (prod-VPS):" >&2
			echo "  export PGUSER=arm_ai_admin        # под существующий \$PG_PWD (пароль arm_ai_admin)" >&2
			echo "  # PGHOST/PGPORT/PGDATABASE/PG_PWD обычно уже в профиле машины" >&2
			echo "Проверка: PGPASSWORD=\"\$PG_PWD\" psql -h \"\$PGHOST\" -p \"\$PGPORT\" -U \"\$PGUSER\" -d \"\${PGDATABASE:-wb_data_prod}\" -c 'SELECT 1'" >&2
			exit 1
		fi
		echo ">>> Реальный отчёт (seasons=${SEASONS} collections='${COLLECTIONS}') из ${PGDATABASE:-wb_data_prod} @ ${PGHOST}:${PGPORT} (user=${PGUSER}) → ${OUT}"
		go run . "${COMMON[@]}"
		;;
	*)
		echo "Usage: $0 [mock|real]   (по умолчанию real)" >&2
		echo "  mock — отправить демо-xlsx (тест почты, БД не нужна)" >&2
		echo "  real — собрать отчёт из PostgreSQL и отправить" >&2
		exit 1
		;;
esac

echo
echo ">>> Файл сохранён: ${OUT}"
echo ">>> Получатели / тема / текст письма: config.yaml → email.recipients / email.subject / email.body"

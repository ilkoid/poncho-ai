#!/usr/bin/env python3
"""fix-env-format.py — убрать префикс `export ` из строк .env (dotenv-стиль).

⚠️  ПРЕЖДЕ ЧЕМ ЗАПУСКАТЬ НА РЕАЛЬНОМ .env — прочитай:

  Формат `export VAR='...'` — ЗАДОКУМЕНТИРОВАННЫЙ канон проекта (AGENTS.md +
  шапка самого .env), и он нагрузочный:
    • run-daily-analytics.sh:37 (VPS-пайплайн) грузит .env ПРОСТЫМ `source`
      БЕЗ `set -a` — без `export` переменные перестанут экспортироваться
      в дочерние процессы, утилиты из bin/ потеряют ключи.
    • Ручное `source .env` в шелле тоже работает только из-за `export`.
    • dotenv-инструментам (python-dotenv, docker compose) префикс `export`
      не мешает — они его понимают.
  Выгода от удаления — только косметическая. Если всё же применяешь:
  сначала добавь `set -a` в run-daily-analytics.sh и обнови AGENTS.md.

  Значения переменных НЕ читаются и НЕ выводятся (только имена).

Usage:
  python3 fix-env-format.py [path/to/.env]          # dry-run: показать план
  python3 fix-env-format.py path/to/.env --apply    # применить (+ бэкап)
"""
import argparse
import os
import re
import shutil
import sys
import time

EXPORT_RE = re.compile(r"^export[ \t]+(?=[A-Za-z_][A-Za-z0-9_]*=)")
ASSIGN_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*=")


def transform(text: str):
    """Возвращает (новый_текст, [имена_переменных], [строки_с_подозрительным_export]).

    Трогает ТОЛЬКО строки вида `export NAME=...`: убирает префикс `export `.
    Всё остальное (комментарии, пустые строки, кавычки, значения) — байт-в-байт.
    """
    out_lines, names, suspicious = [], [], []
    for i, line in enumerate(text.splitlines(keepends=True), 1):
        stripped_body = line.rstrip("\r\n")
        if stripped_body.startswith("export") and not EXPORT_RE.match(line):
            # `export` есть, но за ним не `NAME=` — боимся портить, помечаем.
            suspicious.append(i)
            out_lines.append(line)
            continue
        if EXPORT_RE.match(line):
            new_line = EXPORT_RE.sub("", line, count=1)
            if not ASSIGN_RE.match(new_line):  # страховка, недостижимо по regex
                suspicious.append(i)
                out_lines.append(line)
                continue
            names.append(ASSIGN_RE.match(new_line).group(0)[:-1])
            out_lines.append(new_line)
        else:
            out_lines.append(line)
    return "".join(out_lines), names, suspicious


def validate(original: str, transformed: str) -> list:
    """Проверки целостности: число строк, все присваивания валидны, ничего не исчезло."""
    errors = []
    if len(original.splitlines()) != len(transformed.splitlines()):
        errors.append("изменилось число строк — отказываюсь писать")
    for ln in transformed.splitlines():
        body = ln.strip()
        if not body or body.startswith("#"):
            continue
        if not ASSIGN_RE.match(body):
            errors.append(f"после правки осталась не-присваивание строка: {body[:20]}…")
    return errors


def main():
    ap = argparse.ArgumentParser(description="Убрать `export ` из .env (dotenv-стиль)")
    ap.add_argument("path", nargs="?", default=".env", help="путь к .env (по умолчанию ./.env)")
    ap.add_argument("--apply", action="store_true", help="применить изменения (иначе dry-run)")
    args = ap.parse_args()

    if not os.path.isfile(args.path):
        sys.exit(f"FAIL: файл не найден: {args.path}")

    with open(args.path, "r", encoding="utf-8") as f:
        original = f.read()

    transformed, names, suspicious = transform(original)

    if suspicious:
        sys.exit(f"FAIL: строки {suspicious} содержат `export`, но не `NAME=` — "
                 "вручную проверь файл, автоматически не трогаю.")
    errors = validate(original, transformed)
    if errors:
        sys.exit("FAIL: " + "; ".join(errors))

    if not names:
        print("Изменений не требуется: строк с `export` нет.")
        return

    print(f"Будет убран префикс `export ` у {len(names)} переменных:")
    print("  " + ", ".join(names))

    if not args.apply:
        print("\nDry-run: файл НЕ изменён. Для применения: --apply")
        return

    stamp = time.strftime("%Y%m%d-%H%M%S")
    backup = f"{args.path}.bak-{stamp}"
    shutil.copy2(args.path, backup)  # бэкап с сохранением прав

    tmp = args.path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        f.write(transformed)
    os.chmod(tmp, os.stat(args.path).st_mode)  # права как у исходника
    os.replace(tmp, args.path)  # атомарная замена

    print(f"\nOK: {args.path} переписан ({len(names)} строк), бэкап: {backup}")
    print("Проверка: set -a; source " + args.path + "; set +a; printenv | wc -l")


if __name__ == "__main__":
    main()

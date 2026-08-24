"""Подключение к PostgreSQL для skill `supply-time-report`.

Env загружается из канонического файла проекта /Users/ilkoid/dev/poncho-ai/.env
(см. AGENTS.md — это shell-синтаксис `export VAR='...'` с одинарными кавычками,
не dotenv-формат). Поэтому парсим его через zsh, а не python-dotenv.

DSN резолвится по той же логике, что и pkg/config/pgconfig.go GetEffectiveDSN:
host/port/user/password/database из env с дефолтами.
"""

from __future__ import annotations

import os
import subprocess
import sys
from dataclasses import dataclass

import psycopg2
from psycopg2.extras import RealDictCursor

# Канонический источник секретов проекта (см. AGENTS.md, раздел ".env").
ENV_FILE = "/Users/ilkoid/dev/poncho-ai/.env"

# Дефолты совпадают с pkg/config/pgconfig.go:187-193.
DEFAULTS = {
    "PGHOST": "192.168.10.7",
    "PGPORT": "15432",
    "PGUSER": "postgres",
    "PGDATABASE": "wb_data_prod",
}


@dataclass
class DSN:
    host: str
    port: int
    user: str
    password: str
    dbname: str

    def masked(self) -> str:
        """Строка для логов без пароля (пароль не должен светиться в чате/ps)."""
        pwd_hint = f"{self.password[:1]}***{self.password[-1:]}" if len(self.password) >= 3 else "***"
        return (
            f"postgresql://{self.user}:{pwd_hint}@{self.host}:{self.port}/"
            f"{self.dbname}?sslmode=disable"
        )


def load_env(env_file: str = ENV_FILE) -> dict[str, str]:
    """Source'ит .env через zsh и возвращает dict переменных.

    zsh -c non-interactive НЕ исполняет ~/.zshrc (см. AGENTS.md gotcha),
    поэтому source'им .env явно. `set -a` экспортирует все переменные в env.
    """
    if not os.path.exists(env_file):
        raise FileNotFoundError(
            f".env не найден: {env_file}. Это канонический источник секретов "
            "проекта (см. AGENTS.md)."
        )
    # Выводим нужные переменные через echo "${VAR:-}" внутри того же shell-процесса,
    # который уже source'ил .env. Используем ${VAR:-} (а не printenv VAR), потому что
    # printenv с несуществующей переменной возвращает rc=1; в .env проекта обычно
    # задан только PG_PWD, а PGHOST/PGPORT/PGUSER/PGDATABASE берутся из дефолтов.
    keys = ["PGHOST", "PGPORT", "PGUSER", "PG_PWD", "PGDATABASE"]
    # Каждую переменную на своей строке через ${VAR:-} — пустая строка для unset.
    echo_lines = "\n".join(f'echo "${{{k}:-}}"' for k in keys)
    shell_cmd = f"set -a; source {env_file} 2>/dev/null; set +a; {echo_lines}"
    proc = subprocess.run(
        ["zsh", "-c", shell_cmd],
        capture_output=True,
        text=True,
        check=False,
    )
    if proc.returncode != 0:
        raise RuntimeError(
            f"Не удалось загрузить .env через zsh (rc={proc.returncode}): "
            f"{proc.stderr.strip()}"
        )
    values = proc.stdout.splitlines()
    env: dict[str, str] = {}
    for key, val in zip(keys, values):
        if val:
            env[key] = val
    if "PG_PWD" not in env or not env["PG_PWD"]:
        # Двойная проверка (AGENTS.md): printenv может быть пустым, если var
        # не загружена. Проверяем прямо в файле.
        raise RuntimeError(
            f"PG_PWD пуст после source {env_file}. Проверьте grep PG_PWD {env_file}."
        )
    return env


def resolve_dsn(env: dict[str, str] | None = None) -> DSN:
    """Резолвит DSN из env с дефолтами. Повторяет pkg/config/pgconfig.go."""
    env = env or load_env()
    return DSN(
        host=env.get("PGHOST") or DEFAULTS["PGHOST"],
        port=int(env.get("PGPORT") or DEFAULTS["PGPORT"]),
        user=env.get("PGUSER") or DEFAULTS["PGUSER"],
        password=env["PG_PWD"],
        dbname=env.get("PGDATABASE") or DEFAULTS["PGDATABASE"],
    )


def connect(dsn: DSN | None = None, statement_timeout: str = "60s") -> psycopg2.extensions.connection:
    """Открывает соединение, выставляет statement_timeout и проверяет пинг.

    Параметр password передаётся psycopg2 через kwargs (не в conninfo строке),
    чтобы не светиться в `ps`. См. AGENTS.md gotcha про PGPASSWORD.
    """
    dsn = dsn or resolve_dsn()
    conn = psycopg2.connect(
        host=dsn.host,
        port=dsn.port,
        user=dsn.user,
        password=dsn.password,
        dbname=dsn.dbname,
        sslmode="disable",
        connect_timeout=10,
        cursor_factory=RealDictCursor,
    )
    conn.autocommit = True  # только read-only; транзакции не нужны
    with conn.cursor() as cur:
        cur.execute(f"SET statement_timeout = {statement_timeout!r};")
        cur.execute("SELECT 1 AS ok;")
        row = cur.fetchone()
        if not row or row["ok"] != 1:
            raise RuntimeError("Ping PG failed: SELECT 1 не вернул 1")
    return conn


def log_dsn(dsn: DSN, stream=sys.stderr) -> None:
    """Логирует DSN с замаскированным паролем (для отладки)."""
    print(f"[db] {dsn.masked()}", file=stream)

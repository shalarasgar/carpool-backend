#!/usr/bin/env bash
# Logical backup of the carpool database. Safe to run while the API is up:
# pg_dump takes a transaction snapshot and does not block writers for the dump.
#
# Usage:
#   ./scripts/backup-db.sh
#   ./scripts/backup-db.sh --docker
#   BACKUP_DIR=/var/backups/carpool BACKUP_RETENTION_DAYS=14 ./scripts/backup-db.sh
#
# --docker runs pg_dump inside the compose Postgres service so the client
# matches the server. COMPOSE_FILE selects the compose file (default
# docker-compose.yml). Without --docker, pg_dump on PATH is used with
# DATABASE_URL from the environment or from .env.
#
# Dumps are custom format, mode 0600, and contain phone numbers. Keep the
# directory private. A second run exits 0 while one backup holds the lock.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT_DIR="${BACKUP_DIR:-$ROOT/backups}"
RETENTION_DAYS="${BACKUP_RETENTION_DAYS:-14}"
COMPOSE_FILE="${COMPOSE_FILE:-$ROOT/docker-compose.yml}"
MODE="pg_dump"
umask 077

usage() {
  echo "usage: backup-db.sh [--docker] [--output-dir DIR]" >&2
  exit 2
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --docker) MODE="docker"; shift ;;
    --output-dir)
      [[ $# -ge 2 ]] || usage
      OUT_DIR="$2"
      shift 2
      ;;
    -h|--help) usage ;;
    *) echo "unknown argument: $1" >&2; usage ;;
  esac
done

if ! [[ "$RETENTION_DAYS" =~ ^[0-9]+$ ]] || [[ "$RETENTION_DAYS" -lt 1 ]]; then
  echo "BACKUP_RETENTION_DAYS must be an integer >= 1" >&2
  exit 2
fi

if [[ "$MODE" == "pg_dump" && -z "${DATABASE_URL:-}" && -f "$ROOT/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  . "$ROOT/.env"
  set +a
fi

mkdir -p "$OUT_DIR"
lock="$OUT_DIR/.backup.lock"

file_mtime() {
  if stat -c %Y "$1" >/dev/null 2>&1; then
    stat -c %Y "$1"
  else
    stat -f %m "$1"
  fi
}

if ! mkdir "$lock" 2>/dev/null; then
  now="$(date +%s)"
  mtime="$(file_mtime "$lock")"
  age="$((now - mtime))"
  if [[ "$age" -gt 21600 ]]; then
    rm -rf "$lock"
    mkdir "$lock"
  else
    echo "backup already running" >&2
    exit 0
  fi
fi

stamp="$(date -u +%Y%m%dT%H%M%SZ)"
tmp="$OUT_DIR/.carpool-${stamp}.dump.tmp"
final="$OUT_DIR/carpool-${stamp}.dump"

cleanup() {
  rm -f "$tmp"
  rmdir "$lock" 2>/dev/null || true
}
trap cleanup EXIT

if [[ "$MODE" == "docker" ]]; then
  docker compose -f "$COMPOSE_FILE" exec -T postgres \
    pg_dump -U carpool -d carpool --format=custom --no-owner --no-acl >"$tmp"
else
  if [[ -z "${DATABASE_URL:-}" ]]; then
    echo "DATABASE_URL is required (or pass --docker)" >&2
    exit 2
  fi
  pg_dump --format=custom --no-owner --no-acl --file="$tmp" "$DATABASE_URL"
fi

if [[ ! -s "$tmp" ]]; then
  echo "backup is empty" >&2
  exit 1
fi

mv "$tmp" "$final"

find "$OUT_DIR" -name 'carpool-*.dump' -type f -mtime +"$RETENTION_DAYS" -delete

echo "wrote $final"

#!/usr/bin/env bash
# Restore a custom-format dump produced by backup-db.sh.
# Stop the API first. This drops and recreates objects in the target database.
#
# Usage:
#   ./scripts/restore-db.sh backups/carpool-20261010T030000Z.dump --yes
#   ./scripts/restore-db.sh --docker backups/carpool-20261010T030000Z.dump --yes
#
# --docker restores into the compose Postgres service. COMPOSE_FILE selects
# the compose file (default docker-compose.yml). Without --docker, pg_restore
# uses DATABASE_URL from the environment or from .env.
# --yes is required so a stray invocation cannot replace the database.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
COMPOSE_FILE="${COMPOSE_FILE:-$ROOT/docker-compose.yml}"
MODE="pg_restore"
CONFIRM=0
FILE=""

usage() {
  echo "usage: restore-db.sh [--docker] --yes <file.dump>" >&2
  exit 2
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --docker) MODE="docker"; shift ;;
    --yes) CONFIRM=1; shift ;;
    -h|--help) usage ;;
    --)
      shift
      break
      ;;
    -*)
      echo "unknown argument: $1" >&2
      usage
      ;;
    *)
      if [[ -n "$FILE" ]]; then
        echo "unexpected argument: $1" >&2
        usage
      fi
      FILE="$1"
      shift
      ;;
  esac
done

if [[ "$CONFIRM" -ne 1 || -z "$FILE" ]]; then
  usage
fi
if [[ ! -f "$FILE" ]]; then
  echo "backup file not found" >&2
  exit 2
fi

if [[ "$MODE" == "pg_restore" && -z "${DATABASE_URL:-}" && -f "$ROOT/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  . "$ROOT/.env"
  set +a
fi

echo "restoring $FILE (stop the API first)" >&2

if [[ "$MODE" == "docker" ]]; then
  docker compose -f "$COMPOSE_FILE" exec -T postgres \
    pg_restore -U carpool -d carpool --clean --if-exists --no-owner --no-acl --exit-on-error --single-transaction \
    <"$FILE"
else
  if [[ -z "${DATABASE_URL:-}" ]]; then
    echo "DATABASE_URL is required (or pass --docker)" >&2
    exit 2
  fi
  pg_restore --clean --if-exists --no-owner --no-acl --exit-on-error --single-transaction \
    --dbname="$DATABASE_URL" "$FILE"
fi

echo "restore finished" >&2

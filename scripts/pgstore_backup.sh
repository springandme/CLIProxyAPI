#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: pgstore_backup.sh [env_file] [backup_dir] [retention_days]

Defaults:
  env_file       ./.env
  backup_dir     ./backups/postgres
  retention_days 7

Environment variables:
  PGSTORE_DSN                     PostgreSQL connection string (required)
  PGSTORE_BACKUP_RETENTION_DAYS   Retention override in days
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

ENV_FILE="${1:-.env}"
BACKUP_DIR="${2:-./backups/postgres}"
RETENTION_DAYS="${3:-${PGSTORE_BACKUP_RETENTION_DAYS:-7}}"

if [[ ! -f "$ENV_FILE" ]]; then
  echo "env file not found: $ENV_FILE" >&2
  exit 1
fi

set -a
# shellcheck disable=SC1090
source "$ENV_FILE"
set +a

if [[ -z "${PGSTORE_DSN:-}" ]]; then
  echo "PGSTORE_DSN is required in $ENV_FILE" >&2
  exit 1
fi

if ! command -v pg_dump >/dev/null 2>&1; then
  echo "pg_dump is required but was not found in PATH" >&2
  exit 1
fi

mkdir -p "$BACKUP_DIR"

timestamp="$(date +%Y%m%d-%H%M%S)"
base_name="cliproxy-pgstore-${timestamp}"
dump_path="${BACKUP_DIR}/${base_name}.dump"
sha_path="${dump_path}.sha256"

echo "Creating backup: $dump_path"
pg_dump \
  --dbname="$PGSTORE_DSN" \
  --format=custom \
  --file="$dump_path" \
  --no-owner \
  --no-privileges

sha256sum "$dump_path" >"$sha_path"

if [[ "$RETENTION_DAYS" =~ ^[0-9]+$ ]] && [[ "$RETENTION_DAYS" -gt 0 ]]; then
  find "$BACKUP_DIR" -type f \( -name '*.dump' -o -name '*.dump.sha256' \) -mtime "+${RETENTION_DAYS}" -delete
fi

echo "Backup complete"
echo "Dump: $dump_path"
echo "SHA256: $sha_path"

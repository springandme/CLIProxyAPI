#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: pgstore_restore.sh <dump_path> [env_file] [--yes]

Defaults:
  env_file ./.env

The script restores the dump into PGSTORE_DSN from the env file.
Because this is destructive, pass --yes to continue.
EOF
}

if [[ $# -lt 1 ]]; then
  usage >&2
  exit 1
fi

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

DUMP_PATH="$1"
ENV_FILE=".env"
CONFIRM="false"

shift
for arg in "$@"; do
  case "$arg" in
    --yes)
      CONFIRM="true"
      ;;
    *)
      ENV_FILE="$arg"
      ;;
  esac
done

if [[ ! -f "$DUMP_PATH" ]]; then
  echo "dump file not found: $DUMP_PATH" >&2
  exit 1
fi

if [[ ! -f "$ENV_FILE" ]]; then
  echo "env file not found: $ENV_FILE" >&2
  exit 1
fi

if [[ "$CONFIRM" != "true" ]]; then
  echo "refusing to restore without --yes" >&2
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

if ! command -v pg_restore >/dev/null 2>&1; then
  echo "pg_restore is required but was not found in PATH" >&2
  exit 1
fi

schema_args=()
if [[ -n "${PGSTORE_SCHEMA:-}" ]]; then
  schema_args+=(--schema="$PGSTORE_SCHEMA")
fi

echo "Restoring dump: $DUMP_PATH"
pg_restore \
  --dbname="$PGSTORE_DSN" \
  --clean \
  --if-exists \
  --no-owner \
  --no-privileges \
  "${schema_args[@]}" \
  "$DUMP_PATH"

echo "Restore complete"

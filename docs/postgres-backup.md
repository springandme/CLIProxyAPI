# PostgreSQL Backup and Restore

This document describes a simple first-line backup strategy for deployments that use `PGSTORE_DSN`.

## Scope

- `scripts/pgstore_backup.sh` creates a PostgreSQL dump from `PGSTORE_DSN`.
- `scripts/pgstore_restore.sh` restores a dump back into the configured database.
- These scripts are intended for deployment directories that already contain the runtime `.env`.

## Backup

Run from the deployment directory or pass the `.env` path explicitly:

```bash
./scripts/pgstore_backup.sh /mnt/servers/cli-proxy-api/.env /mnt/servers/cli-proxy-api/backups/postgres 7
```

Behavior:

- reads `PGSTORE_DSN` from the env file
- creates a custom-format dump such as `cliproxy-pgstore-20260402-173000.dump`
- writes a matching `.sha256` file
- deletes backup files older than the configured retention window

## Cron example

Backup every day at 03:15 and keep 7 days by default:

```cron
15 3 * * * cd /mnt/servers/cli-proxy-api && /bin/bash ./scripts/pgstore_backup.sh ./.env ./backups/postgres 7 >> ./backups/postgres/backup.log 2>&1
```

Recommended first-line policy:

- at least 1 backup per day
- keep 7 days locally
- test restoration regularly instead of assuming the dump is valid

## Restore

Restore is destructive. Always stop write traffic first and confirm the target database.

```bash
./scripts/pgstore_restore.sh /mnt/servers/cli-proxy-api/backups/postgres/cliproxy-pgstore-20260402-173000.dump /mnt/servers/cli-proxy-api/.env --yes
```

Notes:

- the script restores into `PGSTORE_DSN`
- if `PGSTORE_SCHEMA` is set, the restore is scoped to that schema
- `--yes` is required to avoid accidental restores

## Auth-file level recovery

For accidental deletion of a subset of auth files, prefer:

1. management JSON upload of the affected auth files
2. Auth Files Excel import with `create_missing=true`

This is safer than rolling the whole database back when only a few auth files were removed.

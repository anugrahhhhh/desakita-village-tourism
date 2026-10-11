#!/usr/bin/env bash
# Restores a backup made by scripts/backup.sh into an EMPTY database and upload directory.
#
#   DATABASE_URL=postgres://.../desakita_restored UPLOAD_DIR=/data/uploads \
#     scripts/restore.sh /backups/desakita-20261011T020000Z --yes
#
# It refuses to run without --yes, verifies checksums first, and fails if the target
# database already has tables, so it can't silently overwrite live data.
set -euo pipefail

backup="${1:?usage: restore.sh BACKUP_DIR --yes}"
[ "${2:-}" = "--yes" ] || { echo "refusing to restore without --yes" >&2; exit 2; }
: "${DATABASE_URL:?DATABASE_URL is required}"
UPLOAD_DIR="${UPLOAD_DIR:-./data/uploads}"
PSQL="${PSQL:-psql}"
PG_RESTORE="${PG_RESTORE:-pg_restore}"

(cd "$backup" && sha256sum --check --quiet SHA256SUMS)

tables="$("$PSQL" -Atqc "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'" "$DATABASE_URL" </dev/null)"
if [ "$tables" != "0" ]; then
  echo "target database is not empty ($tables tables); restore into a fresh database" >&2
  exit 1
fi

"$PG_RESTORE" --no-owner --no-privileges --exit-on-error --dbname="$DATABASE_URL" "$backup/db.dump"

if [ -f "$backup/uploads.tar.gz" ]; then
  mkdir -p "$UPLOAD_DIR"
  tar -xzf - -C "$UPLOAD_DIR" < "$backup/uploads.tar.gz"
fi
echo "restored $backup"

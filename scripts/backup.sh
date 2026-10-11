#!/usr/bin/env bash
# Backs up the database (pg_dump custom format) and uploaded photos into
# $BACKUP_DIR/desakita-<UTC timestamp>/, verifies the dump, writes checksums and
# deletes backups older than $RETENTION_DAYS. Prints the new backup directory.
#
#   DATABASE_URL=postgres://... UPLOAD_DIR=/data/uploads BACKUP_DIR=/backups scripts/backup.sh
#
# Optional: RETENTION_DAYS (default 14), PG_DUMP / PG_RESTORE (tool paths).
# Backups contain personal data (names, phone numbers): files are created mode 0600/0700.
set -euo pipefail

: "${DATABASE_URL:?DATABASE_URL is required}"
UPLOAD_DIR="${UPLOAD_DIR:-./data/uploads}"
BACKUP_DIR="${BACKUP_DIR:-./backups}"
RETENTION_DAYS="${RETENTION_DAYS:-14}"
PG_DUMP="${PG_DUMP:-pg_dump}"
PG_RESTORE="${PG_RESTORE:-pg_restore}"

umask 077
mkdir -p "$BACKUP_DIR"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
final="$BACKUP_DIR/desakita-$stamp"
work="$BACKUP_DIR/.desakita-$stamp.partial"
trap 'rm -rf "$work"' EXIT
mkdir "$work"

"$PG_DUMP" --format=custom --no-owner --no-privileges --file="$work/db.dump" "$DATABASE_URL"
# A dump that pg_restore can't list is not a backup.
"$PG_RESTORE" --list "$work/db.dump" >/dev/null

if [ -d "$UPLOAD_DIR" ]; then
  # Archive via stdout: GNU tar treats "host:path" archive names (e.g. C:/...) as remote.
  tar -czf - -C "$UPLOAD_DIR" . > "$work/uploads.tar.gz"
else
  echo "warning: UPLOAD_DIR $UPLOAD_DIR not found; backing up the database only" >&2
fi

(cd "$work" && sha256sum ./* > SHA256SUMS)
mv "$work" "$final"   # publish only complete backups
trap - EXIT

find "$BACKUP_DIR" -maxdepth 1 -type d -name 'desakita-*' -mtime +"$RETENTION_DAYS" -exec rm -rf {} +
echo "$final"

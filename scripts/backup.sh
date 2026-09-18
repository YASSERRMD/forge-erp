#!/bin/sh
# ForgeERP backup/restore (Phase 6 operational parity).
#
# A ForgeERP system is TWO stateful pieces that must move together:
#   1. PostgreSQL (all ferp_* tables) — pg_dump custom format
#   2. the document object store (FERP_STORAGE_DIR, default ./var/docs) — tar
# Restoring only one leaves dangling share links or orphaned blobs.
#
# Usage:
#   scripts/backup.sh backup --db <URL> --docs <DIR> --out <prefix>
#       writes <prefix>.dump (database) + <prefix>.docs.tgz (blobs) +
#       <prefix>.manifest.json (versions, counts, date)
#   scripts/backup.sh restore --db <URL> --docs <DIR> --in <prefix>
#       drops public-schema content, pg_restores the dump, unpacks blobs
#
# Requirements: pg_dump, pg_restore, psql, tar. Never run restore against
# the live database without a fresh backup first (it wipes schema content).
set -eu

cmd="${1:-}"; shift || true
DB=""; DOCS="./var/docs"; PF=""

while [ $# -gt 0 ]; do
  case "$1" in
    --db) DB="$2"; shift 2;;
    --docs) DOCS="$2"; shift 2;;
    --out) PF="$2"; shift 2;;
    --in) PF="$2"; shift 2;;
    *) echo "unknown flag $1" >&2; exit 2;;
  esac
done

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing: $1" >&2; exit 3; }; }

case "$cmd" in
  backup)
    [ -n "$DB" ] && [ -n "$PF" ] || { echo "backup needs --db and --out" >&2; exit 2; }
    need pg_dump; need psql; need tar
    pg_dump --format=custom --file="$PF.dump" "$DB"
    TABLES=$(psql "$DB" -tAX -c "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name LIKE 'ferp\_%';")
    if [ -d "$DOCS" ]; then
      tar -czf "$PF.docs.tgz" -C "$(dirname "$DOCS")" "$(basename "$DOCS")"
      BLOBS=$(tar -tzf "$PF.docs.tgz" | wc -l | tr -d ' ')
    else
      BLOBS=0
    fi
    printf '{"date":"%s","tables":%s,"blobs":%s}\n' "$(date -u +%FT%TZ)" "$TABLES" "$BLOBS" > "$PF.manifest.json"
    echo "backup: tables=$TABLES blobs=$BLOBS -> $PF.{dump,docs.tgz,manifest.json}"
    ;;
  restore)
    [ -n "$DB" ] && [ -n "$PF" ] || { echo "restore needs --db and --in" >&2; exit 2; }
    need pg_restore; need psql; need tar
    [ -f "$PF.dump" ] || { echo "missing $PF.dump" >&2; exit 2; }
    cat "$PF.manifest.json"
    # Wipe schema content (CASCADE drops all ferp_* tables and the migration
    # history together, so the restore is whole — never partial).
    TABLENAMES=$(psql "$DB" -tAX -c "SELECT string_agg(format('%I.%I', schemaname, tablename), ',') FROM pg_tables WHERE schemaname='public';")
    if [ -n "$TABLENAMES" ]; then
      psql "$DB" -c "DROP TABLE IF EXISTS $TABLENAMES CASCADE;" >/dev/null
    fi
    pg_restore --dbname="$DB" --no-owner "$PF.dump"
    if [ -f "$PF.docs.tgz" ]; then
      mkdir -p "$DOCS"
      tar -xzf "$PF.docs.tgz" -C "$(dirname "$DOCS")"
    fi
    echo "restore: done from $PF"
    ;;
  *)
    echo "usage: $0 {backup|restore} --db URL [--docs DIR] (--out PF | --in PF)" >&2
    exit 2
    ;;
esac

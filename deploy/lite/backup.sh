#!/bin/sh
set -eu

deploy_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
cd "$deploy_dir"

if [ ! -f .env ]; then
    echo "Missing $deploy_dir/.env" >&2
    exit 1
fi

umask 077

mkdir -p backups
timestamp="$(date +%Y%m%d-%H%M%S)"
backup_file="backups/sub2api.${timestamp}.sql.gz"
raw_file="backups/sub2api.${timestamp}.sql"
trap 'rm -f "$raw_file"' EXIT

docker compose exec -T postgres \
    sh -c 'exec pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB"' \
    > "$raw_file"

test -s "$raw_file"
gzip -c "$raw_file" > "$backup_file"
test -s "$backup_file"
rm -f "$raw_file"
find backups -maxdepth 1 -type f -name 'sub2api.*.sql.gz' -mtime +6 -delete

#!/bin/sh
set -eu

deploy_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
cd "$deploy_dir"

if [ ! -f .env ]; then
    echo "Missing $deploy_dir/.env" >&2
    exit 1
fi

umask 077
set -a
. ./.env
set +a

mkdir -p backups
timestamp="$(date +%Y%m%d-%H%M%S)"
backup_file="backups/sub2api.${timestamp}.sql.gz"

docker compose exec -T postgres \
    pg_dump -U "${POSTGRES_USER:-sub2api}" -d "${POSTGRES_DB:-sub2api}" \
    | gzip > "$backup_file"

test -s "$backup_file"
find backups -maxdepth 1 -type f -name 'sub2api.*.sql.gz' -mtime +6 -delete

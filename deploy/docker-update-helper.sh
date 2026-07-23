#!/bin/sh
set -eu

readonly image_repo="ghcr.io/ywainzh/sub2api"
readonly deploy_dir="${DEPLOY_DIR:-/opt/sub2api}"
readonly compose_file="${deploy_dir}/docker-compose.yml"
readonly env_file="${deploy_dir}/.env"

if ! printf '%s\n' "${TARGET_IMAGE:-}" | grep -Eq '^ghcr\.io/ywainzh/sub2api:v[0-9]+\.[0-9]+\.[0-9]+$'; then
    echo "Invalid target image" >&2
    exit 1
fi

test -S /var/run/docker.sock
test -f "$compose_file"
test -f "$env_file"

sleep 3
umask 077
mkdir -p "${deploy_dir}/backups"
timestamp="$(date +%Y%m%d-%H%M%S)"
log_file="${deploy_dir}/backups/update-${timestamp}.log"
env_backup="${deploy_dir}/backups/.env.before-update-${timestamp}"
env_tmp="${deploy_dir}/.env.update.$$"
trap 'rm -f "$env_tmp"' EXIT
exec >>"$log_file" 2>&1

echo "[$(date '+%Y-%m-%dT%H:%M:%S%z')] Starting online update to ${TARGET_IMAGE}"
docker image inspect "$TARGET_IMAGE" >/dev/null

# A failed backup aborts the update before .env or the running container changes.
sh "${deploy_dir}/backup.sh"
cp -p "$env_file" "$env_backup"

awk -v image="$TARGET_IMAGE" '
    BEGIN { replaced = 0 }
    /^APP_IMAGE=/ { print "APP_IMAGE=" image; replaced = 1; next }
    { print }
    END { if (!replaced) print "APP_IMAGE=" image }
' "$env_file" >"$env_tmp"
chmod 600 "$env_tmp"
mv -f "$env_tmp" "$env_file"

compose() {
    docker compose --env-file "$env_file" -f "$compose_file" --project-directory "$deploy_dir" "$@"
}

restore_previous() {
    echo "[$(date '+%Y-%m-%dT%H:%M:%S%z')] Restoring previous image configuration"
    cp -p "$env_backup" "$env_file"
    compose up -d --no-deps --force-recreate sub2api || true
}

if ! compose config --quiet; then
    restore_previous
    exit 1
fi

if ! compose up -d --no-deps --force-recreate sub2api; then
    restore_previous
    exit 1
fi

healthy=false
attempt=0
while [ "$attempt" -lt 30 ]; do
    status="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' sub2api 2>/dev/null || true)"
    if [ "$status" = "healthy" ]; then
        healthy=true
        break
    fi
    if [ "$status" = "unhealthy" ] || [ "$status" = "exited" ] || [ "$status" = "dead" ]; then
        break
    fi
    attempt=$((attempt + 1))
    sleep 2
done

if [ "$healthy" != "true" ]; then
    echo "[$(date '+%Y-%m-%dT%H:%M:%S%z')] New container did not become healthy"
    restore_previous
    exit 1
fi

echo "[$(date '+%Y-%m-%dT%H:%M:%S%z')] Online update completed"

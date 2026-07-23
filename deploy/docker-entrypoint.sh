#!/bin/sh
set -e

# Fix data directory permissions when running as root.
# Docker named volumes / host bind-mounts may be owned by root,
# preventing the non-root sub2api user from writing files.
if [ "$(id -u)" = "0" ]; then
    mkdir -p /app/data
    # Use || true to avoid failure on read-only mounted files (e.g. config.yaml:ro)
    chown -R sub2api:sub2api /app/data 2>/dev/null || true

    # su-exec initializes the target user's groups and would otherwise discard
    # Compose's numeric group_add entry. Mirror the mounted Docker socket GID in
    # /etc/group so the non-root application keeps online-update access.
    if [ -S /var/run/docker.sock ]; then
        socket_gid="$(stat -c '%g' /var/run/docker.sock)"
        case "$socket_gid" in
            ''|*[!0-9]*)
                echo "Invalid Docker socket group ID" >&2
                exit 1
                ;;
        esac
        socket_group="$(awk -F: -v gid="$socket_gid" '$3 == gid { print $1; exit }' /etc/group)"
        if [ -z "$socket_group" ]; then
            socket_group="docker-host"
            if grep -q "^${socket_group}:" /etc/group; then
                socket_group="docker-host-${socket_gid}"
            fi
            addgroup -g "$socket_gid" "$socket_group"
        fi
        if ! id -Gn sub2api | tr ' ' '\n' | grep -qx "$socket_group"; then
            addgroup sub2api "$socket_group"
        fi
    fi

    # Re-invoke this script as sub2api so the flag-detection below
    # also runs under the correct user.
    exec su-exec sub2api "$0" "$@"
fi

# Compatibility: if the first arg looks like a flag (e.g. --help),
# prepend the default binary so it behaves the same as the old
# ENTRYPOINT ["/app/sub2api"] style.
if [ "${1#-}" != "$1" ]; then
    set -- /app/sub2api "$@"
fi

exec "$@"

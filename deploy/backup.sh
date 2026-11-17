#!/usr/bin/env bash
# Dumps the database and the uploaded photos into /var/backups/yolugra and keeps
# 14 days. Run daily from root's crontab. Copies on this server do not survive the
# loss of the server: download them elsewhere from time to time.
set -euo pipefail

cd "$(dirname "$0")"
dir=/var/backups/yolugra
stamp=$(date +%Y%m%d-%H%M%S)
mkdir -p "$dir"
chmod 700 "$dir"

# Write to a temporary name first so a failed dump never looks like a good one.
docker compose exec -T postgres pg_dump -U carpool -d carpool --no-owner | gzip >"$dir/db-$stamp.sql.gz.tmp"
mv "$dir/db-$stamp.sql.gz.tmp" "$dir/db-$stamp.sql.gz"

docker run --rm -v yolugra_media:/media:ro alpine:3.20 tar czf - -C /media . >"$dir/media-$stamp.tar.gz.tmp"
mv "$dir/media-$stamp.tar.gz.tmp" "$dir/media-$stamp.tar.gz"

find "$dir" -name '*.gz' -mtime +14 -delete

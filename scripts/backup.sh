#!/usr/bin/env bash
set -euo pipefail

if [ -f .env ]; then
    source .env
fi

BACKUP_DIR="backups/$(date +%Y%m%d_%H%M%S)"
mkdir -p "${BACKUP_DIR}"

echo "Writing backup to ${BACKUP_DIR}"

docker compose exec -T db pg_dump -U "${POSTGRES_USER}" "${POSTGRES_DB}" >"${BACKUP_DIR}/database.sql"
docker compose exec -T minio mc mirror --overwrite /data "${BACKUP_DIR}/minio/"

cp .env "${BACKUP_DIR}/env.backup"
cp docker-compose.yml "${BACKUP_DIR}/"

tar -czf "${BACKUP_DIR}.tar.gz" -C backups "$(basename "${BACKUP_DIR}")"
rm -rf "${BACKUP_DIR}"

find backups/ -name "*.tar.gz" -type f -mtime +7 -delete

echo "Backup complete: ${BACKUP_DIR}.tar.gz"

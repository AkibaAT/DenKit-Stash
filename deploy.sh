#!/usr/bin/env bash
set -euo pipefail

if [ ! -f .env ]; then
    echo ".env file not found. Copy .env.example to .env and configure it."
    exit 1
fi

source .env

echo "Building DenKit Stash image..."
docker compose build --no-cache denkit-stash

echo "Creating volumes if needed..."
docker volume create "${DB_VOLUME_NAME}" >/dev/null
docker volume create "${MINIO_VOLUME_NAME}" >/dev/null

echo "Restarting services..."
docker compose down
docker compose up -d

echo "Waiting for service health..."
sleep 10

if docker compose ps | grep -q "unhealthy\|exited"; then
    echo "Some services are not healthy. Recent logs:"
    docker compose logs --tail=50
    exit 1
fi

echo "Deployment completed."
echo "DenKit Stash: https://${DENKIT_SUBDOMAIN}.${DOMAIN}"
echo "DenKit Stash API: https://${DENKIT_API_SUBDOMAIN}.${DOMAIN}"
echo "MinIO Storage: https://${DENKIT_STORAGE_SUBDOMAIN}.${DOMAIN}"
echo "MinIO Console: SSH tunnel to localhost:9001"

#!/usr/bin/env bash
set -euo pipefail

if [ -f .env ]; then
    source .env
fi

echo "Checking compose services..."
if ! docker compose ps | grep -q "Up"; then
    echo "One or more services are not running."
    docker compose ps
    exit 1
fi

echo "Checking DenKit Stash..."
curl -fsS "https://${DENKIT_SUBDOMAIN}.${DOMAIN}/" >/dev/null

echo "Checking DenKit Stash API..."
curl -fsS "https://${DENKIT_API_SUBDOMAIN}.${DOMAIN}/" >/dev/null

echo "Checking RustFS..."
curl -fsS "https://${DENKIT_STORAGE_SUBDOMAIN}.${DOMAIN}/health" >/dev/null

echo "Checking PostgreSQL..."
docker compose exec -T db pg_isready -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" >/dev/null

echo "All services are healthy."

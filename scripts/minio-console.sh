#!/usr/bin/env bash
set -euo pipefail

if [ -f .env ]; then
    source .env
fi

cat <<EOF
MinIO console access

The compose file binds the console to localhost only.

Remote tunnel:
  ssh -L 9001:localhost:9001 user@your-server

Local URL:
  http://localhost:9001

Credentials:
  username: ${MINIO_ROOT_USER:-admin}
  password: ${MINIO_ROOT_PASSWORD:-check .env}

Useful commands:
  docker compose exec minio sh -c 'mc alias set local http://localhost:9000 "\$MINIO_ROOT_USER" "\$MINIO_ROOT_PASSWORD" && mc ls local'
  docker compose exec minio sh -c 'mc alias set local http://localhost:9000 "\$MINIO_ROOT_USER" "\$MINIO_ROOT_PASSWORD" && mc stat "local/\$MINIO_BUCKET"'
  docker compose exec minio sh -c 'mc alias set local http://localhost:9000 "\$MINIO_ROOT_USER" "\$MINIO_ROOT_PASSWORD" && mc admin info local'
EOF

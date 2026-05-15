# Production Deployment

DenKit Stash is a single Go service backed by PostgreSQL and MinIO-compatible object storage. The included `docker-compose.yml` is a production-oriented starting point for deployments behind Traefik.

## Prerequisites

- Docker and Docker Compose
- A Traefik reverse proxy using the Docker provider
- An external Docker network for Traefik, named by `TRAEFIK_NETWORK`
- DNS records for the configured DenKit Stash API and storage hostnames
- TLS handled by your reverse proxy or edge provider

## Deploy

```bash
cp .env.example .env
$EDITOR .env
./deploy.sh
```

The service runs schema setup at startup through the Go database layer. Do not mount the removed legacy SQL migration directory into PostgreSQL.

DenKit also checks `MINIO_BUCKET` at startup and creates it when the configured MinIO/S3 credentials are allowed to create buckets. It then removes any bucket policy so the bucket stays private; clients receive signed URLs for uploads and downloads instead of public object access. If you use restricted S3 credentials, create the bucket before starting the service and grant those credentials read/write/multipart plus bucket-policy permissions for that bucket.

After the service starts, create a DenKit user token on the server:

```bash
docker compose exec denkit-stash ./denkit-stash --create-user=alice
```

Use the printed API key with butler:

```bash
export BUTLER_API_KEY=printed-api-key
butler --address=https://${DENKIT_API_SUBDOMAIN}.${DOMAIN} push ./build alice/my-game:stable
butler --address=https://${DENKIT_API_SUBDOMAIN}.${DOMAIN} status alice/my-game:stable
```

Published images are built by GitHub Actions on native hosted runners for `linux/amd64` and `linux/arm64`, then pushed to GitHub Container Registry on branch and tag pushes.

## Environment Configuration

Required settings:

- `DOMAIN`, `DENKIT_SUBDOMAIN`, `DENKIT_API_SUBDOMAIN`, `DENKIT_STORAGE_SUBDOMAIN`
- `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD`
- `MINIO_ENDPOINT`, `MINIO_PUBLIC_ENDPOINT`, `MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY`, `MINIO_BUCKET`
- `MINIO_ROOT_USER`, `MINIO_ROOT_PASSWORD`
- `TRAEFIK_NETWORK`

Set `ENABLE_DEV_ENDPOINTS=false` in production. The development-only OAuth and MinIO test routes are not registered unless that value is exactly `true`.

## Traefik Configuration

The compose file exposes:

- DenKit Stash routes on `${DENKIT_SUBDOMAIN}.${DOMAIN}` and `${DENKIT_API_SUBDOMAIN}.${DOMAIN}`
- MinIO API route on `${DENKIT_STORAGE_SUBDOMAIN}.${DOMAIN}`
- MinIO console bound only to `127.0.0.1:9001`

## Security

- Containers run as non-root where applicable.
- MinIO storage should stay private; downloads and uploads use signed URLs.
- The MinIO console is local-only by default. Access it through SSH tunneling or `docker compose exec`.
- Store real secrets outside version control. `.env` files are ignored.
- Keep `ENABLE_DEV_ENDPOINTS=false` outside local development.

## MinIO Console

```bash
ssh -L 9001:localhost:9001 user@your-server
```

Then open `http://localhost:9001` locally.

Useful MinIO checks:

```bash
docker compose exec minio sh -c 'mc alias set local http://localhost:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" && mc ls local'
docker compose exec minio sh -c 'mc alias set local http://localhost:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" && mc stat "local/$MINIO_BUCKET"'
docker compose exec minio sh -c 'mc alias set local http://localhost:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" && mc anonymous set none "local/$MINIO_BUCKET"'
```

## Volumes

- PostgreSQL data: `${DB_VOLUME_NAME}`
- MinIO data: `${MINIO_VOLUME_NAME}`

## Maintenance

```bash
docker compose ps
docker compose logs -f denkit-stash
./scripts/health-check.sh
./scripts/backup.sh
git pull
./deploy.sh
```

## Troubleshooting

- Check `docker compose ps` and service health first.
- Check Traefik routing and DNS when HTTP routes fail.
- Check `docker compose logs db` for PostgreSQL credential or readiness problems.
- Check `docker compose logs minio` for bucket or credential problems.

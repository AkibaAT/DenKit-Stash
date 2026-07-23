# Production Deployment

DenKit Stash is a single Go service backed by PostgreSQL and RustFS/S3-compatible object storage. The included `docker-compose.yml` is a production-oriented starting point for deployments behind Traefik.

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

DenKit uses the official AWS SDK for Go v2 against the configured S3-compatible endpoint. It checks `S3_BUCKET` at startup and creates it when the configured S3 credentials are allowed to create buckets. It then removes any bucket policy so the bucket stays private; clients receive signed URLs for uploads and downloads instead of public object access. If you use restricted S3 credentials, create the bucket before starting the service and grant those credentials read/write/multipart plus bucket-policy permissions for that bucket.

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
- `S3_ENDPOINT`, `S3_PUBLIC_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_BUCKET`. Both endpoint values must be full URLs with an `http://` or `https://` scheme.
- `RUSTFS_ACCESS_KEY`, `RUSTFS_SECRET_KEY`
- `TRAEFIK_NETWORK`

Set `ENABLE_DEV_ENDPOINTS=false` in production. The development-only OAuth and object storage test routes are not registered unless that value is exactly `true`.

## Traefik Configuration

The compose file exposes:

- DenKit Stash routes on `${DENKIT_SUBDOMAIN}.${DOMAIN}` and `${DENKIT_API_SUBDOMAIN}.${DOMAIN}`
- RustFS S3 API route on `${DENKIT_STORAGE_SUBDOMAIN}.${DOMAIN}`
- RustFS console bound only to `127.0.0.1:9001`

## Security

- Containers run as non-root where applicable.
- RustFS storage should stay private; downloads and uploads use signed URLs.
- The RustFS console is local-only by default. Access it through SSH tunneling.
- Store real secrets outside version control. `.env` files are ignored.
- Keep `ENABLE_DEV_ENDPOINTS=false` outside local development.

## RustFS Console

```bash
ssh -L 9001:localhost:9001 user@your-server
```

Then open `http://localhost:9001` locally.

Useful RustFS checks:

```bash
docker run --rm --network denkit-network \
  -e AWS_ACCESS_KEY_ID="$S3_ACCESS_KEY" \
  -e AWS_SECRET_ACCESS_KEY="$S3_SECRET_KEY" \
  -e AWS_DEFAULT_REGION="${S3_REGION:-us-east-1}" \
  amazon/aws-cli s3api head-bucket \
  --endpoint-url http://rustfs:9000 \
  --bucket "$S3_BUCKET"

docker run --rm --network denkit-network \
  -e AWS_ACCESS_KEY_ID="$S3_ACCESS_KEY" \
  -e AWS_SECRET_ACCESS_KEY="$S3_SECRET_KEY" \
  -e AWS_DEFAULT_REGION="${S3_REGION:-us-east-1}" \
  amazon/aws-cli s3 ls \
  --endpoint-url http://rustfs:9000
```

## Volumes

- PostgreSQL data: `${DB_VOLUME_NAME}`
- RustFS data: `${RUSTFS_VOLUME_NAME}`
- Archive rebuild scratch space: `${SCRATCH_VOLUME_NAME}` (temp data only, safe to wipe when the server is stopped)

## Archive Cache Eviction

Full game archives are a cache: patches and signatures stored per build are
the permanent source of truth, and any build's archive can be rebuilt on
demand by replaying its patch chain. Eviction is **opt-in** via
`DENKIT_ARCHIVE_GC_ENABLED=true`; until then no archive objects are ever
deleted. Channel-head archives, patches, and signatures are never evicted.

Tuning (see `.env.example`): `DENKIT_ARCHIVE_TTL` (evict archives not
downloaded for this long, default 720h), `DENKIT_ARCHIVE_GC_INTERVAL`,
`DENKIT_ARCHIVE_GC_BATCH`, and `DENKIT_ARCHIVE_REBUILD_TIMEOUT` (bounds one
blocking rebuild; keep it at or below `DENKIT_HTTP_WRITE_TIMEOUT`).

Archive generation and rebuilds stage the parent tree and output tree in
`TMPDIR`, which is the disk-backed `${SCRATCH_VOLUME_NAME}` volume mounted at
`/scratch` (games can be multiple GB, so this deliberately avoids the
RAM-backed `/tmp` tmpfs). Make sure the volume's disk has room for roughly
two uncompressed copies of the largest hosted game. A download that hits an
evicted archive blocks while the chain is replayed, so clients of
`/builds/{id}/download/archive/default` need generous timeouts.

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
- Check `docker compose logs rustfs` for bucket or credential problems.

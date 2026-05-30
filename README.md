# DenKit Stash

DenKit is a self-hosted publishing kit for independent game developers and small studios. DenKit Stash is the backend for build uploads, artifact storage, update metadata, project metadata APIs, and desktop-client compatibility.

The server starts with compatibility for the MIT-licensed [`butler`](https://github.com/itchio/butler) client and its upload/update workflows by implementing server-side behavior for the MIT-licensed itch.io [`wharf`](https://github.com/itchio/wharf) protocol. It stores metadata in PostgreSQL, stores artifacts in MinIO/S3-compatible object storage, and exposes the endpoints needed for push, status, fetch, downloads, and upgrade paths.

## Compatibility Notice

DenKit is independent software and is not an official itch.io service, product, or repository. It implements compatibility behavior for familiar game-publishing workflows and compatible tooling, including the public `butler` client and DenKit's desktop client fork. itch.io, `butler`, and Wharf remain their respective projects; DenKit's Wharf support is an independent server implementation of the open MIT-licensed protocol and compatible file formats.

## Features

- `butler push`, `status`, and `fetch` compatibility
- MIT-licensed itch.io Wharf protocol compatibility
- Wharf build files for `patch/default`, `signature/default`, and `archive/default`
- Game and project metadata endpoints for client and web integrations
- Parent build tracking and channel heads
- Upgrade-path endpoint for client update flows
- Private MinIO/S3 storage with signed upload and download URLs
- API-key authentication with user/admin namespace checks
- Local DDEV environment and Docker production compose files

## Requirements

- Go 1.26.3 or newer
- PostgreSQL for normal deployments
- MinIO or an S3-compatible service
- Docker for the contract test and production compose workflow

## Local Development

The DDEV setup starts PostgreSQL, MinIO, and the Go service with the required environment variables.

```bash
ddev start
ddev exec "go build -o denkit-stash ."
ddev exec "./denkit-stash --create-user=myusername"
```

The `--create-user` command prints an API key. DenKit stores the key hash in PostgreSQL and does not show the key again, so keep the printed value for your client configuration.

Start the server:

```bash
ddev exec "./denkit-stash"
```

Use the printed API key with the butler client:

```bash
export BUTLER_API_SERVER=https://api.denkit-stash.ddev.site
export BUTLER_API_KEY=your-api-key

mkdir -p /tmp/my-game
echo "Hello" >/tmp/my-game/game.txt
butler push /tmp/my-game myusername/my-game:main
butler status myusername/my-game:main
butler fetch myusername/my-game:main /tmp/my-game-out
```

The channel name is the part after `:`. In the example above, `main` is a release channel, not a Git branch.

## Storage And Tokens

DenKit requires PostgreSQL, MinIO/S3 storage, and a DenKit Stash API key before clients can push builds.

Storage setup:

- Set `MINIO_ENDPOINT` to the internal S3 endpoint the server can reach.
- Set `MINIO_PUBLIC_ENDPOINT` to the externally reachable S3 endpoint used in signed upload and download URLs.
- Set `MINIO_BUCKET` to the private bucket DenKit should use for build files.
- Set `MINIO_ACCESS_KEY` and `MINIO_SECRET_KEY` to credentials with read, write, and multipart-upload access for that bucket.

At startup, DenKit checks whether `MINIO_BUCKET` exists. If the configured credentials can create buckets, DenKit creates it automatically. DenKit then removes any bucket policy so objects stay private and are only exposed through signed upload and download URLs. The configured credentials therefore need bucket policy management permission in addition to object read/write permissions.

If you want to create the bucket manually before startup:

```bash
docker compose exec minio sh -c 'mc alias set local http://localhost:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" && mc mb local/denkit-storage'
docker compose exec minio sh -c 'mc alias set local http://localhost:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" && mc anonymous set none local/denkit-storage'
```

The bundled compose setup uses `MINIO_ROOT_USER` and `MINIO_ROOT_PASSWORD` for the MinIO root account. You can use that account for simple private deployments, or create a narrower MinIO access key with bucket create/read/write/multipart and bucket-policy permissions, then put it in `MINIO_ACCESS_KEY` / `MINIO_SECRET_KEY`.

Create a DenKit Stash API key:

```bash
./denkit-stash --create-user=alice
```

Then push with butler:

```bash
export BUTLER_API_KEY=printed-api-key
butler --address=https://api.denkit.example.com push ./build alice/my-game:stable
```

Use the same key for status and fetch:

```bash
butler --address=https://api.denkit.example.com status alice/my-game:stable
butler --address=https://api.denkit.example.com fetch alice/my-game:stable ./install
```

## Configuration

PostgreSQL:

- `POSTGRES_HOST`
- `POSTGRES_PORT`
- `POSTGRES_DB`
- `POSTGRES_USER`
- `POSTGRES_PASSWORD`
- `POSTGRES_SSLMODE`

MinIO/S3:

- `MINIO_ENDPOINT`
- `MINIO_PUBLIC_ENDPOINT`
- `MINIO_ACCESS_KEY`
- `MINIO_SECRET_KEY`
- `MINIO_BUCKET`
- `MINIO_USE_SSL`

Application:

- `PORT`
- `ENABLE_DEV_ENDPOINTS`
- `DENKIT_API_KEY_HASH_SECRET`
- `DENKIT_HTTP_READ_HEADER_TIMEOUT`
- `DENKIT_HTTP_READ_TIMEOUT`
- `DENKIT_HTTP_WRITE_TIMEOUT`
- `DENKIT_HTTP_IDLE_TIMEOUT`
- `DENKIT_MAX_REQUEST_BODY_BYTES`
- `DENKIT_MAX_UPLOAD_SESSION_BYTES`

Production deployments should keep `ENABLE_DEV_ENDPOINTS=false`. When set to `true`, local-only development OAuth helpers and the authenticated `/test/minio` route are registered.

`DENKIT_API_KEY_HASH_SECRET` is required before creating or authenticating users. It is used to store non-reversible HMAC-SHA256 digests of API keys instead of raw bearer credentials. Generate a unique secret per deployment, for example with `openssl rand -hex 32`, and keep it with the rest of the deployment secrets. Existing raw keys from early development databases are converted to digests on startup when this secret is configured.

Clients should send API keys in `Authorization: Bearer <key>` or through the `BUTLER_API_KEY` environment variable used by butler. Query-string API keys are accepted only for butler/Wharf compatibility and should not be used by new integrations, because query strings are routinely captured by proxy and access logs. DenKit redacts request query strings from its own logs.

HTTP timeout values use Go duration syntax such as `5s`, `2m`, or `30m`. DenKit defaults to a 5 second read-header timeout, 30 minute read and write timeouts for large client uploads, and a 2 minute idle timeout. Metadata JSON/form requests are capped at 1 MiB by default, and deferred upload sessions are capped at 50 GiB by default.

## Commands

```bash
make build          # build ./denkit-stash
make test           # run Go tests
make contract-test  # run a black-box butler compatibility test with a downloaded butler binary
make openapi-generate  # regenerate docs/openapi.yaml from Huma route declarations
make openapi-validate  # validate the generated OpenAPI document
make actionlint     # validate the GitHub Actions workflow
make govulncheck    # scan reachable Go code for known vulnerabilities
make verify         # run OpenAPI, workflow, vulnerability, formatting, and Go test gates
make clean          # remove local build and storage artifacts
```

## Maintenance

Dependency and build-tool updates are handled through [.github/dependabot.yml](.github/dependabot.yml). Runtime container images are pinned in [Dockerfile](Dockerfile), [docker-compose.yml](docker-compose.yml), and [scripts/contract-test.sh](scripts/contract-test.sh); when those pins move, update the digest with the version. The butler compatibility test uses a fixed butler release and SHA256 so the contract test is repeatable.

CI is split across [.github/workflows/ci.yml](.github/workflows/ci.yml), [.github/workflows/dependency-review.yml](.github/workflows/dependency-review.yml), and [.github/workflows/docker-publish.yml](.github/workflows/docker-publish.yml). Pull requests run read-only validation and dependency review. Docker publishing only runs on pushes to `development` and version tags.

User management commands:

```bash
./denkit-stash --create-user=alice
./denkit-stash --create-admin=admin
./denkit-stash --ensure-admin=admin --api-key="$DENKIT_STASH_API_KEY"
./denkit-stash --list-users
./denkit-stash --activate-user=alice
./denkit-stash --deactivate-user=alice
```

## API Surface

Generated OpenAPI declarations live in [docs/openapi.yaml](docs/openapi.yaml). The document is generated from the Huma route declarations in [http_api.go](http_api.go), so route behavior and API documentation have the same source of truth.

Regenerate and validate the document after changing routes, request bodies, response shapes, or authentication behavior:

```bash
make openapi-generate
make openapi-validate
```

The [CI workflow](.github/workflows/ci.yml) also regenerates the document and fails if [docs/openapi.yaml](docs/openapi.yaml) is out of date.

Core API:

```text
GET  /profile
GET  /profile/games
GET  /games/{id}
GET  /games/{id}/uploads
POST /games/{id}/download-sessions
GET  /uploads/{id}
GET  /uploads/{id}/builds
GET  /builds/{id}
GET  /builds/{buildId}/download/{type}/{subType}
GET  /builds/{installedBuildId}/upgrade-paths/{targetBuildId}
GET  /{namespace}/{game}/{channel}/archive/default
```

Wharf API:

```text
GET  /wharf/status
GET  /wharf/channels
GET  /wharf/channels/{channel}
GET  /wharf/builds
POST /wharf/builds
GET  /wharf/builds/{id}/files
POST /wharf/builds/{id}/files
POST /wharf/builds/{buildId}/files/{fileId}
GET  /wharf/builds/{buildId}/files/{fileId}/download
POST /wharf/upload-sessions/{id}
PUT  /wharf/upload-sessions/{id}
```

## Data Model

The service creates and updates its schema at startup. The main tables are:

- `users`
- `games`
- `uploads`
- `channels`
- `builds`
- `build_files`
- `upload_sessions`

PostgreSQL schema setup lives in the Go database layer.

## Deployment

Copy [.env.example](.env.example) to `.env`, set deployment-specific values, then run:

```bash
./deploy.sh
```

See [DEPLOYMENT.md](DEPLOYMENT.md), [docker-compose.yml](docker-compose.yml), and [deploy.sh](deploy.sh) for the Docker Compose and Traefik deployment notes.

GitHub Actions builds `linux/amd64` and `linux/arm64` images on native hosted runners and publishes a multi-platform Stash image to GitHub Container Registry:

```text
ghcr.io/<owner>/<repo>
```

## License

MIT. See [LICENSE](LICENSE).

## Acknowledgements

DenKit Stash builds on the open publishing workflow pioneered by itch.io's MIT-licensed [`butler`](https://github.com/itchio/butler) client and MIT-licensed [`wharf`](https://github.com/itchio/wharf) protocol implementation. DenKit is independent software and is not affiliated with or endorsed by itch.io.

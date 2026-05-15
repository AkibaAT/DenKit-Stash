#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK_DIR="${WORK_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/denkit-contract.XXXXXX")}"
SERVER_PORT="${SERVER_PORT:-18080}"
SERVER_URL="${SERVER_URL:-http://127.0.0.1:${SERVER_PORT}}"
MINIO_PORT="${MINIO_PORT:-19000}"
MINIO_CONSOLE_PORT="${MINIO_CONSOLE_PORT:-19001}"
MINIO_CONTAINER="${MINIO_CONTAINER:-denkit-contract-minio-$$}"
MINIO_BUCKET="${MINIO_BUCKET:-denkit-contract-$$}"
MINIO_ACCESS_KEY="${MINIO_ACCESS_KEY:-ddevminio}"
MINIO_SECRET_KEY="${MINIO_SECRET_KEY:-ddevminio}"
POSTGRES_PORT="${POSTGRES_PORT:-15432}"
POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-denkit-contract-postgres-$$}"
POSTGRES_DB="${POSTGRES_DB:-denkit_contract}"
POSTGRES_USER="${POSTGRES_USER:-denkit}"
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-denkit}"
SERVER_BIN="${SERVER_BIN:-${WORK_DIR}/denkit-stash}"
BUTLER_BIN="${BUTLER_BIN:-${WORK_DIR}/butler}"
BUTLER_VERSION="${BUTLER_VERSION:-15.27.0}"
SERVER_PID=""

cleanup() {
	if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
		kill "${SERVER_PID}" 2>/dev/null || true
		wait "${SERVER_PID}" 2>/dev/null || true
	fi
	docker stop "${MINIO_CONTAINER}" >/dev/null 2>&1 || true
	docker stop "${POSTGRES_CONTAINER}" >/dev/null 2>&1 || true
	if [[ "${KEEP_WORK_DIR:-}" != "1" ]]; then
		rm -rf "${WORK_DIR}"
	fi
}
trap cleanup EXIT

require_command() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "missing required command: $1" >&2
		exit 1
	fi
}

wait_for_http() {
	local url="$1"
	for _ in $(seq 1 60); do
		if curl -fsS "${url}" >/dev/null 2>&1; then
			return 0
		fi
		sleep 0.5
	done
	echo "timed out waiting for ${url}" >&2
	return 1
}

wait_for_postgres() {
	for _ in $(seq 1 60); do
		if docker exec "${POSTGRES_CONTAINER}" pg_isready -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" >/dev/null 2>&1 &&
			docker exec "${POSTGRES_CONTAINER}" psql -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" -Atc 'select 1;' >/dev/null 2>&1; then
			return 0
		fi
		sleep 0.5
	done
	echo "timed out waiting for PostgreSQL" >&2
	return 1
}

require_command curl
require_command docker
require_command go
require_command unzip

mkdir -p "${WORK_DIR}"
echo "work dir: ${WORK_DIR}"

echo "building server"
(cd "${ROOT_DIR}" && go build -o "${SERVER_BIN}" .)

if [[ ! -x "${BUTLER_BIN}" ]]; then
	case "$(uname -m)" in
		x86_64 | amd64)
			BUTLER_CHANNEL="linux-amd64"
			BUTLER_SHA256="${BUTLER_SHA256:-b9f0d6eef33036031cafaf59939d9f3bc724e2b674c510d4a9cf8a9f1008e299}"
			;;
		aarch64 | arm64)
			BUTLER_CHANNEL="linux-arm64"
			BUTLER_SHA256="${BUTLER_SHA256:-63c43e90d7836138cafcbf9d395d81989c4065ecf00f0239d0732bc938e46032}"
			;;
		*)
			echo "unsupported architecture for automatic butler download: $(uname -m)" >&2
			echo "set BUTLER_BIN to an existing butler binary" >&2
			exit 1
			;;
	esac

	echo "downloading butler ${BUTLER_VERSION} ${BUTLER_CHANNEL}"
	curl -fsSL "https://broth.itch.zone/butler/${BUTLER_CHANNEL}/${BUTLER_VERSION}/archive/default" -o "${WORK_DIR}/butler.zip"
	printf '%s  %s\n' "${BUTLER_SHA256}" "${WORK_DIR}/butler.zip" | sha256sum -c -
	unzip -q "${WORK_DIR}/butler.zip" -d "${WORK_DIR}/butler-bin"
	BUTLER_BIN="${WORK_DIR}/butler-bin/butler"
	chmod +x "${BUTLER_BIN}"
fi

echo "starting MinIO"
docker run --rm -d \
	--name "${MINIO_CONTAINER}" \
	-p "127.0.0.1:${MINIO_PORT}:9000" \
	-p "127.0.0.1:${MINIO_CONSOLE_PORT}:9001" \
	-e "MINIO_ROOT_USER=${MINIO_ACCESS_KEY}" \
	-e "MINIO_ROOT_PASSWORD=${MINIO_SECRET_KEY}" \
	quay.io/minio/minio:RELEASE.2025-09-07T16-13-09Z@sha256:14cea493d9a34af32f524e538b8346cf79f3321eff8e708c1e2960462bd8936e server /data --console-address :9001 >/dev/null
wait_for_http "http://127.0.0.1:${MINIO_PORT}/minio/health/live"

echo "starting PostgreSQL"
docker run --rm -d \
	--name "${POSTGRES_CONTAINER}" \
	-p "127.0.0.1:${POSTGRES_PORT}:5432" \
	-e "POSTGRES_DB=${POSTGRES_DB}" \
	-e "POSTGRES_USER=${POSTGRES_USER}" \
	-e "POSTGRES_PASSWORD=${POSTGRES_PASSWORD}" \
	postgres:18.4-alpine3.23@sha256:38346350acb3e824c6948e6df7ff4759f48ba09d8ef4cb8d1b6e6db120f56872 >/dev/null
wait_for_postgres

SERVER_ENV=(
	"POSTGRES_HOST=127.0.0.1"
	"POSTGRES_PORT=${POSTGRES_PORT}"
	"POSTGRES_DB=${POSTGRES_DB}"
	"POSTGRES_USER=${POSTGRES_USER}"
	"POSTGRES_PASSWORD=${POSTGRES_PASSWORD}"
	"POSTGRES_SSLMODE=disable"
	"MINIO_ENDPOINT=127.0.0.1:${MINIO_PORT}"
	"MINIO_ACCESS_KEY=${MINIO_ACCESS_KEY}"
	"MINIO_SECRET_KEY=${MINIO_SECRET_KEY}"
	"MINIO_BUCKET=${MINIO_BUCKET}"
	"MINIO_USE_SSL=false"
	"DENKIT_API_KEY_HASH_SECRET=denkit-contract-api-key-hash-secret"
)

echo "starting server"
env "${SERVER_ENV[@]}" "${SERVER_BIN}" -port "${SERVER_PORT}" >"${WORK_DIR}/server.log" 2>&1 &
SERVER_PID="$!"
wait_for_http "${SERVER_URL}/"

CREATE_USER_OUTPUT="$(env "${SERVER_ENV[@]}" "${SERVER_BIN}" -create-user=testuser)"
API_KEY="$(printf '%s\n' "${CREATE_USER_OUTPUT}" | awk '/API key:/ {print $NF}')"
if [[ -z "${API_KEY}" ]]; then
	echo "could not parse API key from create-user output:" >&2
	printf '%s\n' "${CREATE_USER_OUTPUT}" >&2
	exit 1
fi

GAME_DIR="${WORK_DIR}/game"
OUT1_DIR="${WORK_DIR}/out1"
OUT2_DIR="${WORK_DIR}/out2"
PATCH_OUT_DIR="${WORK_DIR}/patch-out"
STAGE_DIR="${WORK_DIR}/stage"
mkdir -p "${GAME_DIR}" "${OUT1_DIR}" "${OUT2_DIR}" "${PATCH_OUT_DIR}" "${STAGE_DIR}"

echo "pushing first build"
printf 'Hello World v1\n' >"${GAME_DIR}/game.txt"
BUTLER_API_KEY="${API_KEY}" "${BUTLER_BIN}" --address="${SERVER_URL}" --assume-yes push "${GAME_DIR}" testuser/test-game:main

echo "fetching first build"
BUTLER_API_KEY="${API_KEY}" "${BUTLER_BIN}" --address="${SERVER_URL}" fetch testuser/test-game:main "${OUT1_DIR}"
grep -qx 'Hello World v1' "${OUT1_DIR}/game.txt"

echo "pushing second build"
printf 'Hello World v2\n' >"${GAME_DIR}/game.txt"
printf 'Second file\n' >"${GAME_DIR}/extra.txt"
SECOND_PUSH_OUTPUT="$(BUTLER_API_KEY="${API_KEY}" "${BUTLER_BIN}" --address="${SERVER_URL}" --assume-yes push "${GAME_DIR}" testuser/test-game:main)"
printf '%s\n' "${SECOND_PUSH_OUTPUT}"
grep -q 'last build is 1' <<<"${SECOND_PUSH_OUTPUT}"

echo "checking status"
STATUS_OUTPUT="$(BUTLER_API_KEY="${API_KEY}" "${BUTLER_BIN}" --address="${SERVER_URL}" status testuser/test-game:main)"
printf '%s\n' "${STATUS_OUTPUT}"
grep -q '#2 (from #1)' <<<"${STATUS_OUTPUT}"

echo "fetching second build"
BUTLER_API_KEY="${API_KEY}" "${BUTLER_BIN}" --address="${SERVER_URL}" fetch testuser/test-game:main "${OUT2_DIR}"
grep -qx 'Hello World v2' "${OUT2_DIR}/game.txt"
grep -qx 'Second file' "${OUT2_DIR}/extra.txt"

echo "checking upgrade path"
UPGRADE_PATH_JSON="$(curl -fsS -H "Authorization: ${API_KEY}" "${SERVER_URL}/builds/1/upgrade-paths/2")"
grep -q '"id":2' <<<"${UPGRADE_PATH_JSON}"
grep -q '"type":"patch"' <<<"${UPGRADE_PATH_JSON}"

echo "checking download session"
DOWNLOAD_SESSION_JSON="$(curl -fsS -X POST -H "Authorization: ${API_KEY}" "${SERVER_URL}/games/1/download-sessions")"
grep -q '"uuid":"' <<<"${DOWNLOAD_SESSION_JSON}"

echo "checking build downloads and patch application"
curl -fsSL -H "Authorization: ${API_KEY}" -o "${WORK_DIR}/build2.patch.pwr" "${SERVER_URL}/builds/2/download/patch/default"
curl -fsSL -H "Authorization: ${API_KEY}" -o "${WORK_DIR}/build2.signature.pws" "${SERVER_URL}/builds/2/download/signature/default"
"${BUTLER_BIN}" --assume-yes apply --staging-dir "${STAGE_DIR}" --dir "${PATCH_OUT_DIR}" --signature "${WORK_DIR}/build2.signature.pws" "${WORK_DIR}/build2.patch.pwr" "${OUT1_DIR}"
grep -qx 'Hello World v2' "${PATCH_OUT_DIR}/game.txt"
grep -qx 'Second file' "${PATCH_OUT_DIR}/extra.txt"

echo "checking archive download"
curl -fsSL -H "Authorization: ${API_KEY}" -o "${WORK_DIR}/build2.zip" "${SERVER_URL}/builds/2/download/archive/default"
unzip -l "${WORK_DIR}/build2.zip" | grep -q 'game.txt'
unzip -l "${WORK_DIR}/build2.zip" | grep -q 'extra.txt'

echo "checking database state"
UPLOAD_SIZE="$(docker exec "${POSTGRES_CONTAINER}" psql -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" -Atc 'select size from uploads where id = 1;')"
if [[ "${UPLOAD_SIZE}" -le 0 ]]; then
	echo "expected upload size to be nonzero, got ${UPLOAD_SIZE}" >&2
	exit 1
fi
BUILD_CHAIN="$(docker exec "${POSTGRES_CONTAINER}" psql -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" -Atc "select id || ':' || coalesce(parent_build_id, 0) || ':' || state from builds order by id;")"
grep -qx '1:0:completed' <<<"${BUILD_CHAIN}"
grep -qx '2:1:completed' <<<"${BUILD_CHAIN}"

echo "contract test passed"

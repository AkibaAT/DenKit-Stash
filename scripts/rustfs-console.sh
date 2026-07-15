#!/usr/bin/env bash
set -euo pipefail

if [ -f .env ]; then
    source .env
fi

cat <<EOF
RustFS console access

The compose file binds the console to localhost only.

Remote tunnel:
  ssh -L 9001:localhost:9001 user@your-server

Local URL:
  http://localhost:9001

Credentials:
  username: ${RUSTFS_ACCESS_KEY:-admin}
  password: ${RUSTFS_SECRET_KEY:-check .env}

Useful commands:
  docker run --rm --network denkit-network \\
    -e AWS_ACCESS_KEY_ID="\$S3_ACCESS_KEY" \\
    -e AWS_SECRET_ACCESS_KEY="\$S3_SECRET_KEY" \\
    -e AWS_DEFAULT_REGION="\${S3_REGION:-us-east-1}" \\
    amazon/aws-cli s3api head-bucket \\
    --endpoint-url http://rustfs:9000 \\
    --bucket "\$S3_BUCKET"

  docker run --rm --network denkit-network \\
    -e AWS_ACCESS_KEY_ID="\$S3_ACCESS_KEY" \\
    -e AWS_SECRET_ACCESS_KEY="\$S3_SECRET_KEY" \\
    -e AWS_DEFAULT_REGION="\${S3_REGION:-us-east-1}" \\
    amazon/aws-cli s3 ls \\
    --endpoint-url http://rustfs:9000
EOF

#!/bin/sh
# Dev build + run: never touches the installed release.
#
#   ./dev.sh              build web + binary, run on :20129 with ~/.kinerouter-dev
#   ./dev.sh --no-web     skip the web build (backend-only iteration)
#
# The version carries a -dev suffix (git short sha), so /api/version reports
# channel "dev" and the dashboard never shows an update prompt. A release build
# keeps a plain X.Y.Z and does check GitHub for a newer tag.
set -eu

cd "$(dirname "$0")"

PORT=${PORT:-20129}
DATA_DIR=${DATA_DIR:-$HOME/.kinerouter-dev}
REV=$(git rev-parse --short HEAD 2>/dev/null || echo local)

if [ "${1:-}" != "--no-web" ]; then
	npm --prefix web-app run build
fi

go build -ldflags "-X github.com/keyzakyy-dev/kinerouter/internal/gateway.Version=$REV-dev" \
	-o ../kinerouter-dev ./cmd/kinerouter

echo "dev build $REV-dev → http://127.0.0.1:$PORT  (data: $DATA_DIR)"
PORT=$PORT DATA_DIR=$DATA_DIR exec ../kinerouter-dev

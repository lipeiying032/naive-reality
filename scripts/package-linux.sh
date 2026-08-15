#!/bin/sh
# packaging script (Linux): assemble the server release tarball.
# Usage: bash scripts/package-linux.sh [naive-binary]
set -e
ROOT=$(cd "$(dirname "$0")/.." && pwd)
OUT="$ROOT/release-linux"
NAIVE=${1:-}

rm -rf "$OUT"
mkdir -p "$OUT"

cd "$ROOT/frontend"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o "$OUT/naivereal-frontend" .
cd "$ROOT"

if [ -n "$NAIVE" ] && [ -f "$NAIVE" ]; then
  cp "$NAIVE" "$OUT/naive"
else
  echo "NOTE: naive binary not provided; copy it into the tarball yourself"
fi

cp "$ROOT/frontend/frontend.toml.example" "$OUT/"
cp "$ROOT/LICENSE-Go" "$ROOT/NOTICE.md" "$ROOT/README.md" "$OUT/"
mkdir -p "$OUT/deploy"
cp "$ROOT/deploy/naivereal.service" "$ROOT/deploy/naivereal-frontend.service" "$ROOT/deploy/install.sh" "$OUT/deploy/"
mkdir -p "$OUT/docs"
cp "$ROOT/docs/deploy.md" "$ROOT/docs/protocol.md" "$OUT/docs/"

tar cJf "$ROOT/naivereal-linux-x64.tar.xz" -C "$ROOT/release-linux" .
echo "release: $ROOT/naivereal-linux-x64.tar.xz"
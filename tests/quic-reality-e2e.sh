#!/bin/sh
# CI e2e: REALITY-over-QUIC full chain with the PATCHED kernel.
#  patched naive client (quic:// + reality flags) -> h3frontend (mode=reality)
#  -> official naive server -> internet.
# The h3frontend presents an operator-owned self-signed certificate (h3_cert/
# h3_key) so no dest certificate fetch is needed; the patched client skips
# CertificateVerify and sends the REALITY auth payload in the ClientHello
# random field, which the h3frontend precheck verifies.
# Usage: bash tests/quic-reality-e2e.sh <dir-with-patched-naive>
set -eu

NAIVE_DIR=${1:-/tmp/kernel}
NAIVE="$NAIVE_DIR/naive"
H3=${NAIVEREAL_H3FRONTEND_BIN:-/tmp/naivereal-h3frontend}
PIDS=""

cleanup() {
  rc=$?
  trap - EXIT INT TERM
  for pid in $PIDS; do
    kill "$pid" 2>/dev/null || true
  done
  for pid in $PIDS; do
    wait "$pid" 2>/dev/null || true
  done
  for entry in "client:/tmp/quic-cli.log" "h3:/tmp/quic-h3.log" "server:/tmp/quic-srv.log"; do
    name=${entry%%:*}
    path=${entry#*:}
    echo "--- $name log ---"
    if [ -f "$path" ]; then
      tail -30 "$path"
    else
      echo "(missing)"
    fi
  done
  exit "$rc"
}
trap cleanup EXIT INT TERM

if [ -z "${NAIVEREAL_H3FRONTEND_BIN:-}" ]; then
  cd h3frontend
  go build -o "$H3" .
  cd ..
elif [ ! -x "$H3" ]; then
  echo "NAIVEREAL_H3FRONTEND_BIN is not executable: $H3" >&2
  exit 1
fi

# 1. local self-signed cert for example.test
mkdir -p /tmp/certs
if [ ! -f /tmp/certs/server.pem ]; then
  openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
    -subj "/CN=example.test" -addext "subjectAltName=DNS:example.test" \
    -keyout /tmp/certs/server-key.pem -out /tmp/certs/server.pem >/dev/null 2>&1
fi

# 2. REALITY keypair
if [ -n "${NAIVEREAL_TEST_PRIVATE_KEY:-}" ] || [ -n "${NAIVEREAL_TEST_PUBLIC_KEY:-}" ]; then
  PRIV=${NAIVEREAL_TEST_PRIVATE_KEY:-}
  PUB=${NAIVEREAL_TEST_PUBLIC_KEY:-}
else
  KEYS=$("$H3" genkey)
  PRIV=$(printf '%s\n' "$KEYS" | awk '$1 == "Private" && $2 == "key:" { print $3; exit }')
  PUB=$(printf '%s\n' "$KEYS" | awk '$1 == "Public" && $2 == "key:" { print $3; exit }')
fi
if [ -z "$PRIV" ] || [ -z "$PUB" ]; then
  echo "failed to obtain a complete REALITY test keypair" >&2
  exit 1
fi

# 3. official naive server (the patched kernel doubles as server; reality off)
"$NAIVE" --listen=http://user:pass@127.0.0.1:18080 --log >/tmp/quic-srv.log 2>&1 &
SRV=$!
PIDS="$PIDS $SRV"

# 4. h3frontend REALITY-over-QUIC mode
cat > /tmp/h3frontend.toml <<EOF
listen = "127.0.0.1:18444"
mode = "reality"
[reality]
private_key = "$PRIV"
short_ids = ["a1b2c3d4e5f60718"]
server_names = ["example.test"]
dest = "127.0.0.1:1444"
dest_server_name = "example.test"
h3_cert = "/tmp/certs/server.pem"
h3_key = "/tmp/certs/server-key.pem"
[upstream]
addr = "127.0.0.1:18080"
EOF
"$H3" /tmp/h3frontend.toml >/tmp/quic-h3.log 2>&1 &
H3P=$!
PIDS="$PIDS $H3P"
sleep 2

# 5. patched client kernel with quic:// + REALITY flags
"$NAIVE" --listen=socks://127.0.0.1:11081 \
  --proxy=quic://user:pass@example.test:18444 \
  --host-resolver-rules="MAP example.test 127.0.0.1" \
  --reality-server-name=example.test \
  --reality-public-key="$PUB" \
  --reality-short-id=a1b2c3d4e5f60718 \
  --log >/tmp/quic-cli.log 2>&1 &
CLI=$!
PIDS="$PIDS $CLI"
sleep 4

CODE=$(curl --connect-timeout 10 --max-time 30 -sS -o /dev/null -w '%{http_code}' \
  --socks5-hostname 127.0.0.1:11081 https://api.github.com/zen)
echo "quic reality e2e http_code=$CODE"
[ "$CODE" = "200" ]

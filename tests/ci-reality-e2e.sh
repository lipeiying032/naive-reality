#!/bin/sh
# CI e2e: REALITY full chain with the PATCHED kernel.
#  patched naive client (reality flags) -> naivereal-frontend (reality mode)
#  -> official naive server -> internet.
# The REALITY target is a local TLS server (tlsserve) standing in for a real
# website; the fork uses its ServerHello as the handshake template.
# Usage: bash tests/ci-reality-e2e.sh <dir-with-patched-naive>
set -eu

NAIVE_DIR=${1:-/tmp/kernel}
NAIVE="$NAIVE_DIR/naive"
FRONTEND=${NAIVEREAL_FRONTEND_BIN:-/tmp/naivereal-frontend}
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
  for entry in "client:/tmp/cli.log" "frontend:/tmp/fe.log" "server:/tmp/srv.log" "target:/tmp/target.log"; do
    name=${entry%%:*}
    path=${entry#*:}
    echo "--- $name log ---"
    if [ -f "$path" ]; then
      tail -20 "$path"
    else
      echo "(missing)"
    fi
  done
  exit "$rc"
}
trap cleanup EXIT INT TERM

if [ -z "${NAIVEREAL_FRONTEND_BIN:-}" ]; then
  cd frontend
  go build -o "$FRONTEND" .
  cd ..
elif [ ! -x "$FRONTEND" ]; then
  echo "NAIVEREAL_FRONTEND_BIN is not executable: $FRONTEND" >&2
  exit 1
fi

"$FRONTEND" gencert -hosts example.test -out /tmp/certs
if [ -n "${NAIVEREAL_TEST_PRIVATE_KEY:-}" ] || [ -n "${NAIVEREAL_TEST_PUBLIC_KEY:-}" ]; then
  PRIV=${NAIVEREAL_TEST_PRIVATE_KEY:-}
  PUB=${NAIVEREAL_TEST_PUBLIC_KEY:-}
else
  KEYS=$("$FRONTEND" genkey)
  PRIV=$(printf '%s\n' "$KEYS" | awk '$1 == "Private" && $2 == "key:" { print $3; exit }')
  PUB=$(printf '%s\n' "$KEYS" | awk '$1 == "Public" && $2 == "key:" { print $3; exit }')
fi
if [ -z "$PRIV" ] || [ -z "$PUB" ]; then
  echo "failed to obtain a complete REALITY test keypair" >&2
  exit 1
fi

# 1. local TLS target site
"$FRONTEND" tlsserve -cert /tmp/certs/server.pem -key /tmp/certs/server-key.pem -listen 127.0.0.1:1443 >/tmp/target.log 2>&1 &
TARGET=$!
PIDS="$PIDS $TARGET"

# 2. official naive server (the patched kernel doubles as server; reality off)
"$NAIVE" --listen=http://user:pass@127.0.0.1:18080 --log >/tmp/srv.log 2>&1 &
SRV=$!
PIDS="$PIDS $SRV"

# 3. REALITY frontend
cat > /tmp/frontend.toml <<EOF
[inbound]
listen = "127.0.0.1:18443"
mode = "reality"
[inbound.reality]
private_key = "$PRIV"
short_ids = ["a1b2c3d4e5f60718"]
server_names = ["example.test"]
target = "127.0.0.1:1443"
[upstream]
addr = "127.0.0.1:18080"
EOF
"$FRONTEND" /tmp/frontend.toml >/tmp/fe.log 2>&1 &
FE=$!
PIDS="$PIDS $FE"
sleep 2

# 4. patched client kernel with REALITY flags
"$NAIVE" --listen=socks://127.0.0.1:11080 \
  --proxy=https://user:pass@example.test:18443 \
  --host-resolver-rules="MAP example.test 127.0.0.1" \
  --reality-server-name=example.test \
  --reality-public-key="$PUB" \
  --reality-short-id=a1b2c3d4e5f60718 \
  --log >/tmp/cli.log 2>&1 &
CLI=$!
PIDS="$PIDS $CLI"
sleep 4

CODE=$(curl --connect-timeout 10 --max-time 30 -sS -o /dev/null -w '%{http_code}' \
  --socks5-hostname 127.0.0.1:11080 https://api.github.com/zen)
echo "reality e2e http_code=$CODE"
[ "$CODE" = "200" ]

#!/bin/sh
# CI e2e: REALITY full chain with the PATCHED kernel.
#  patched naive client (reality flags) -> naivereal-frontend (reality mode)
#  -> official naive server -> internet.
# The REALITY target is a local TLS server (tlsserve) standing in for a real
# website; the fork uses its ServerHello as the handshake template.
# Usage: bash tests/ci-reality-e2e.sh <dir-with-patched-naive>
set -e

NAIVE_DIR=${1:-/tmp/kernel}
NAIVE="$NAIVE_DIR/naive"

cd frontend
go build -o /tmp/naivereal-frontend .
cd ..

/tmp/naivereal-frontend gencert -hosts example.test -out /tmp/certs
KEYS=$(/tmp/naivereal-frontend genkey)
PRIV=$(echo "$KEYS" | grep "Private key:" | cut -d" " -f3)
PUB=$(echo "$KEYS" | grep "Public key:" | cut -d" " -f3)

# 1. local TLS target site
/tmp/naivereal-frontend tlsserve -cert /tmp/certs/server.pem -key /tmp/certs/server-key.pem -listen 127.0.0.1:1443 >/tmp/target.log 2>&1 &
TARGET=$!

# 2. official naive server (the patched kernel doubles as server; reality off)
"$NAIVE" --listen=http://user:pass@127.0.0.1:18080 --log >/tmp/srv.log 2>&1 &
SRV=$!

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
/tmp/naivereal-frontend /tmp/frontend.toml >/tmp/fe.log 2>&1 &
FE=$!
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
sleep 4

CODE=$(curl -sS -o /dev/null -w '%{http_code}' --socks5-hostname 127.0.0.1:11080 https://api.github.com/zen)
echo "reality e2e http_code=$CODE"
kill $CLI $FE $SRV $TARGET 2>/dev/null || true
sleep 1
echo "--- client log ---"; tail -8 /tmp/cli.log
echo "--- frontend log ---"; tail -5 /tmp/fe.log
[ "$CODE" = "200" ]
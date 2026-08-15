#!/bin/sh
# CI e2e: official naive server + naivereal-frontend (tls mode) + official
# naive client, then fetch https through the whole chain.
# Runs on ubuntu runners. Cert trust: installs the generated CA into the
# system store (Chromium-based naive reads system roots on Linux).
set -e

cd frontend
go build -o /tmp/naivereal-frontend .
cd ..

TAG=$(curl -s https://api.github.com/repos/klzgrad/naiveproxy/releases/latest | grep -o '"tag_name": *"[^"]*"' | cut -d'"' -f4)
echo "naive tag: $TAG"
curl -sL -o /tmp/naive.tar.xz "https://github.com/klzgrad/naiveproxy/releases/download/$TAG/naiveproxy-$TAG-linux-x64.tar.xz"
tar xf /tmp/naive.tar.xz -C /tmp
NAIVE=$(ls -d /tmp/naiveproxy-$TAG-linux-x64 | head -1)/naive

/tmp/naivereal-frontend gencert -hosts naive.test -out /tmp/certs
sudo cp /tmp/certs/ca.pem /usr/local/share/ca-certificates/naivereal-test-ca.crt
sudo update-ca-certificates

"$NAIVE" --listen=http://user:pass@127.0.0.1:18080 --log >/tmp/naive-srv.log 2>&1 &
SRV=$!
cat > /tmp/frontend.toml <<EOF
[inbound]
listen = "127.0.0.1:18443"
mode = "tls"
[inbound.tls]
cert = "/tmp/certs/server.pem"
key = "/tmp/certs/server-key.pem"
[upstream]
addr = "127.0.0.1:18080"
EOF
/tmp/naivereal-frontend /tmp/frontend.toml >/tmp/naivereal-fe.log 2>&1 &
FE=$!
sleep 1

SSL_CERT_FILE=/tmp/certs/ca.pem "$NAIVE" --listen=socks://127.0.0.1:11080 \
  --proxy=https://user:pass@naive.test:18443 --host-resolver-rules="MAP naive.test 127.0.0.1" --log >/tmp/naive-cli.log 2>&1 &
CLI=$!
sleep 4

CODE=$(curl -sS -o /dev/null -w '%{http_code}' --socks5-hostname 127.0.0.1:11080 https://api.github.com/zen)
echo "e2e http_code=$CODE"
kill $CLI $FE $SRV 2>/dev/null || true
sleep 1
echo "--- client log ---"; tail -5 /tmp/naive-cli.log
echo "--- server log ---"; tail -5 /tmp/naive-srv.log
[ "$CODE" = "200" ]
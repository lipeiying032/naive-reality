#!/usr/bin/env bash
# Full-chain test using the built Chromium kernel and only loopback targets.
# Certificate trust is installed and removed on an ephemeral Linux CI runner.
# Do not run this script against a deployment host.
set -euo pipefail
if [[ ${GITHUB_ACTIONS:-} != true || ${NAIVEREAL_ALLOW_CI_TRUST_INSTALL:-} != 1 ]]; then
  echo 'This test requires an ephemeral GitHub Actions runner with explicit test-CA installation enabled.' >&2
  exit 2
fi
repo=$(cd "$(dirname "$0")/.." && pwd)
kernel=$(cd "$1" && pwd)/naive
test_dir=$(mktemp -d)
pids=()
ca_installed=0
ca_path="/usr/local/share/ca-certificates/naivereal-h3-$(basename "$test_dir").crt"
cleanup() {
  rc=$?
  trap - EXIT
  for pid in "${pids[@]}"; do kill "$pid" 2>/dev/null || true; done
  for pid in "${pids[@]}"; do wait "$pid" 2>/dev/null || true; done
  if [[ $ca_installed == 1 ]]; then
    sudo rm -f "$ca_path"
    sudo update-ca-certificates >/dev/null
  fi
  if [[ $rc != 0 ]]; then
    for file in "$test_dir"/*.log; do [[ ! -f $file ]] || tail -30 "$file"; done
  fi
  rm -rf "$test_dir"
  exit "$rc"
}
trap cleanup EXIT

(cd "$repo/h3frontend" && go build -o "$test_dir/h3frontend" .)
(cd "$repo/frontend" && go build -o "$test_dir/frontend" .)
"$test_dir/frontend" gencert -hosts site.test -out "$test_dir/certs"
sudo cp "$test_dir/certs/ca.pem" "$ca_path"
ca_installed=1
sudo update-ca-certificates >/dev/null
mkdir "$test_dir/site"
printf 'native-h3 full-chain payload\n' > "$test_dir/site/index.html"
read -r proxy_port h3_port socks_port target_port < <(python3 - <<'PY'
import socket
sockets = [socket.socket() for _ in range(4)]
for s in sockets: s.bind(('127.0.0.1', 0))
print(*(s.getsockname()[1] for s in sockets))
for s in sockets: s.close()
PY
)
"$kernel" "--listen=http://user:test-password@127.0.0.1:$proxy_port" --log > "$test_dir/upstream.log" 2>&1 &
pids+=("$!")
python3 -m http.server "$target_port" --bind 127.0.0.1 --directory "$test_dir/site" > "$test_dir/target.log" 2>&1 &
pids+=("$!")
cat > "$test_dir/h3frontend.toml" <<EOF
mode = "origin"
listen = "127.0.0.1:$h3_port"
[tls]
cert = "$test_dir/certs/server.pem"
key = "$test_dir/certs/server-key.pem"
[origin]
web_root = "$test_dir/site"
username = "user"
password = "test-password"
tcp_listen = "127.0.0.1:$h3_port"
[upstream]
addr = "127.0.0.1:$proxy_port"
EOF
"$test_dir/h3frontend" check "$test_dir/h3frontend.toml"
"$test_dir/h3frontend" "$test_dir/h3frontend.toml" > "$test_dir/h3.log" 2>&1 &
pids+=("$!")
"$kernel" "--listen=socks://127.0.0.1:$socks_port" \
  "--proxy=quic://user:test-password@site.test:$h3_port" \
  '--host-resolver-rules=MAP site.test 127.0.0.1' --log > "$test_dir/client.log" 2>&1 &
pids+=("$!")

success=0
for attempt in {1..20}; do
  if curl --noproxy '' --connect-timeout 1 --max-time 4 --fail --silent --show-error \
    --socks5-hostname "127.0.0.1:$socks_port" "http://127.0.0.1:$target_port/" > "$test_dir/response"; then
    success=1
    break
  fi
  sleep 0.2
done
[[ $success == 1 ]]
cmp "$test_dir/site/index.html" "$test_dir/response"
curl --noproxy '*' --connect-timeout 2 --max-time 5 --fail --silent --show-error \
  --cacert "$test_dir/certs/ca.pem" --resolve "site.test:$h3_port:127.0.0.1" \
  "https://site.test:$h3_port/" > "$test_dir/website"
cmp "$test_dir/site/index.html" "$test_dir/website"
echo 'native-h3: verified TLS website and full Chromium -> H3 -> naive CONNECT chain'

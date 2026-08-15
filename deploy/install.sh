#!/bin/sh
# Install naivereal (official naive server + REALITY frontend) on Linux.
# Usage: sudo ./install.sh [dir_with_binaries]
set -e

DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
SRC=${1:-$DIR}
DEST=/usr/local/bin
ETC=/etc/naivereal
SERVICE_USER=naivereal

if ! id "$SERVICE_USER" >/dev/null 2>&1; then
  useradd --system --home-dir /var/lib/naivereal --create-home --shell /usr/sbin/nologin "$SERVICE_USER"
fi

install -m 0755 "$SRC/naive" "$DEST/naive"
install -m 0755 "$SRC/naivereal-frontend" "$DEST/naivereal-frontend"

install -d -m 0750 -o root -g "$SERVICE_USER" "$ETC"
if [ ! -f "$ETC/frontend.toml" ]; then
  FRONTEND_TEMPLATE=
  for candidate in "$DIR/frontend.toml.example" "$DIR/../frontend.toml.example" "$DIR/../frontend/frontend.toml.example"; do
    if [ -f "$candidate" ]; then
      FRONTEND_TEMPLATE=$candidate
      break
    fi
  done
  if [ -z "$FRONTEND_TEMPLATE" ]; then
    echo "frontend.toml.example not found" >&2
    exit 1
  fi
  install -m 0640 -o root -g "$SERVICE_USER" "$FRONTEND_TEMPLATE" "$ETC/frontend.toml"
  echo "Created $ETC/frontend.toml - edit it before starting (run: naivereal-frontend genkey)"
fi
if [ ! -f "$ETC/naive.json" ]; then
  install -m 0640 -o root -g "$SERVICE_USER" "$DIR/naive.json.example" "$ETC/naive.json"
  echo "Created $ETC/naive.json - replace the placeholder credentials before starting"
fi
chown root:"$SERVICE_USER" "$ETC/frontend.toml" "$ETC/naive.json"
chmod 0640 "$ETC/frontend.toml" "$ETC/naive.json"

install -m 0644 "$DIR/naivereal.service" /etc/systemd/system/naivereal.service
install -m 0644 "$DIR/naivereal-frontend.service" /etc/systemd/system/naivereal-frontend.service
systemctl daemon-reload

cat <<EOF
Next steps:
1. Generate keys:  /usr/local/bin/naivereal-frontend genkey
2. Edit /etc/naivereal/frontend.toml (private_key, short_ids, server_names, target)
3. Edit /etc/naivereal/naive.json and replace the placeholder credentials
4. systemctl enable --now naivereal naivereal-frontend
EOF

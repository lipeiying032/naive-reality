#!/bin/sh
# Install naivereal (official naive server + REALITY frontend) on Linux.
# Usage: sudo ./install.sh [dir_with_binaries]
set -e

DIR=$(dirname "$0")
SRC=${1:-$DIR}
DEST=/usr/local/bin
ETC=/etc/naivereal

install -m 0755 "$SRC/naive" "$DEST/naive"
install -m 0755 "$SRC/naivereal-frontend" "$DEST/naivereal-frontend"

mkdir -p "$ETC"
if [ ! -f "$ETC/frontend.toml" ]; then
  cp "$DIR/frontend.toml.example" "$ETC/frontend.toml"
  echo "Created $ETC/frontend.toml - edit it before starting (run: naivereal-frontend genkey)"
fi

install -m 0644 "$DIR/naivereal.service" /etc/systemd/system/naivereal.service
install -m 0644 "$DIR/naivereal-frontend.service" /etc/systemd/system/naivereal-frontend.service
systemctl daemon-reload

cat <<EOF
Next steps:
1. Generate keys:  /usr/local/bin/naivereal-frontend genkey
2. Edit /etc/naivereal/frontend.toml (private_key, short_ids, server_names, target)
3. Edit /etc/systemd/system/naivereal.service and set your naive user:pass
4. systemctl enable --now naivereal naivereal-frontend
EOF
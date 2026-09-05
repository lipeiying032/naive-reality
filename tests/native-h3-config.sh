#!/usr/bin/env bash
# Test a built native-h3 kernel. A timeout is a failure, not a rejection.
set -euo pipefail
kernel=$(cd "$(dirname "$1")" && pwd)/$(basename "$1")
python3 - "$kernel" <<'PY'
import json
from pathlib import Path
import subprocess
import sys
import tempfile

with tempfile.TemporaryDirectory() as tmp:
    config = Path(tmp) / "config.json"
    for key in ("reality", "reality-server-name", "reality-public-key", "reality-short-id", "quic", "quic-bbr-profile", "quic-disable-socket-recv-optimization"):
        config.write_text(json.dumps({key: {}}))
        for option in (str(config), "--" + key + "=invalid"):
            result = subprocess.run([sys.argv[1], option], capture_output=True, text=True, timeout=5)
            if result.returncode == 0 or "Native H3 build does not support REALITY or custom QUIC options" not in result.stderr:
                raise SystemExit("native-h3 did not explicitly reject option: " + key)
print("native-h3 rejected all legacy JSON and CLI options")
PY

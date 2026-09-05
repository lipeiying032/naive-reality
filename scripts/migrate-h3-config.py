#!/usr/bin/env python3
"""Create new standard-TLS H3 configs without modifying the old files.

Requires Python 3.11+. The owned hostname, certificate, key and website directory
are explicit inputs: a third-party REALITY target cannot supply these values.
No network calls, certificate issuance, deployment, or service changes are made.
"""

import argparse
import json
import os
from pathlib import Path
import re
import sys
import tomllib
from urllib.parse import quote, unquote, urlsplit


def migrate(args):
    server = tomllib.loads(args.server.read_text())
    client = json.loads(args.client.read_text())
    if server.get("mode") != "reality":
        raise ValueError("source server must explicitly use mode=reality")
    if not isinstance(client, dict) or not isinstance(client.get("proxy"), str):
        raise ValueError("source client must have one string proxy URL")
    proxy = urlsplit(client["proxy"])
    if proxy.scheme not in ("quic", "https") or proxy.username is None or proxy.password is None:
        raise ValueError("source proxy must contain an explicit username and password")
    username, password = unquote(proxy.username), unquote(proxy.password)
    if not username or not password or ":" in username or any(ord(c) < 32 or ord(c) == 127 for c in username + password):
        raise ValueError("source proxy credentials are not valid HTTP Basic credentials")
    hostname = args.hostname.encode("idna").decode("ascii").lower()
    if len(hostname) > 253 or not all(re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", label) for label in hostname.split(".")):
        raise ValueError("hostname must be an owned DNS name, without a scheme or port")
    # Preserve the explicitly configured listener/port, never the target SNI.
    listen = server.get("listen", "0.0.0.0:443")
    match = re.fullmatch(r"(?:\[[^\]]+\]|[^:]+):(\d+)", listen)
    if match is None or not 1 <= int(match[1]) <= 65535:
        raise ValueError("source listen address must contain a nonzero UDP port")
    # The public proxy port can differ from the local listener (port mapping).
    port = args.port if args.port is not None else (proxy.port or 443)
    if not 1 <= port <= 65535:
        raise ValueError("public proxy port must be in 1..65535")
    upstream = server.get("upstream", {}).get("addr", "127.0.0.1:18080")
    # Keep local/client preferences, but remove old identity, routing and
    # transport overrides that can change the endpoint or the native handshake.
    removed = sorted(key for key in client if key.startswith("reality") or key.startswith("quic") or key in ("host-resolver-rules", "no-post-quantum"))
    migrated = {key: value for key, value in client.items() if key not in removed}
    migrated["proxy"] = f"quic://{quote(username, safe='')}:{quote(password, safe='')}@{hostname}:{port}"
    q = json.dumps
    lines = [
        "# Generated migration. Verify the owned certificate and upstream credentials.",
        'mode = "origin"', f"listen = {q(listen)}", "", "[tls]",
        f"cert = {q(str(args.cert.resolve()))}", f"key = {q(str(args.key.resolve()))}", "", "[origin]",
        f"web_root = {q(str(args.web_root.resolve()))}", f"username = {q(username)}", f"password = {q(password)}",
    ]
    if args.tcp_listen:
        lines.append(f"tcp_listen = {q(args.tcp_listen)}")
    lines.extend(["", "[upstream]", f"addr = {q(upstream)}", ""])
    text = "\n".join(lines)
    tomllib.loads(text)  # Check serialization before writing any output.
    os.mkdir(args.out_dir, 0o700)  # Exclusive: never overwrite a prior migration.
    outputs = {
        "h3frontend.toml": text,
        "config.json": json.dumps(migrated, ensure_ascii=False, indent=2) + "\n",
        "MIGRATION.txt": (
            "New standard-TLS H3 configuration; original files were not changed.\n"
            "Use a native-h3 kernel. Verify that the certificate covers the new hostname.\n"
            "The local naive upstream must use the same username/password as origin.\n"
            "Legacy server REALITY and QUIC tuning sections were not carried over.\n"
            "Removed client fields: " + ", ".join(removed) + "\n"
            "No certificate, DNS, firewall, trust-store or running-service changes were made.\n"
            "Validate locally with: h3frontend check <new h3frontend.toml>\n"
        ),
    }
    for name, content in outputs.items():
        fd = os.open(args.out_dir / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            f.write(content)
    print(f"Created new configs in {args.out_dir}; original configs were not changed")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("server", "client", "cert", "key", "web-root", "out-dir"):
        parser.add_argument("--" + name, type=Path, required=True)
    parser.add_argument("--hostname", required=True)
    parser.add_argument("--port", type=int, help="public UDP port; defaults to the old client proxy port")
    parser.add_argument("--tcp-listen", help="optional TCP website listen address; no listener is started")
    args = parser.parse_args()
    try:
        migrate(args)
    except (OSError, ValueError, TypeError, AttributeError) as exc:
        # JSON/TOML parser errors can contain input data; don't print credentials.
        if isinstance(exc, (json.JSONDecodeError, tomllib.TOMLDecodeError)):
            print("migration: invalid source configuration syntax", file=sys.stderr)
        else:
            print(f"migration: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())

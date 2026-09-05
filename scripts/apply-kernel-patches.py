#!/usr/bin/env python3
"""Apply one explicit kernel profile to the pinned, clean upstream checkout.

Both CI and local builds use the manifest. The native-h3 profile changes only
configuration rejection; QUICHE, BoringSSL, and net's QUIC plumbing stay upstream.
This script never fetches, commits, builds, publishes, or resets a checkout.
"""

import argparse
import json
from pathlib import Path
import subprocess
import sys


REPO = Path(__file__).resolve().parent.parent


def git(tree, *args, **kwargs):
    return subprocess.run(
        ["git", "-C", str(tree), *args], check=True, text=True, **kwargs
    )


def main():
    manifest = json.loads((REPO / "patches/manifest.json").read_text())
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("upstream", type=Path)
    parser.add_argument("--profile", default=manifest["default_profile"], choices=manifest["profiles"])
    args = parser.parse_args()
    tree = args.upstream.resolve()
    head = git(tree, "rev-parse", "HEAD", capture_output=True).stdout.strip()
    if head != manifest["upstream_commit"]:
        parser.error(f"upstream HEAD {head} is not the pinned commit")
    version = (tree / "CHROMIUM_VERSION").read_text().strip()
    if version != (REPO / "CHROMIUM_VERSION").read_text().strip():
        parser.error("CHROMIUM_VERSION mismatch")
    status = git(tree, "status", "--porcelain", "--untracked-files=normal", capture_output=True).stdout
    if status:
        parser.error("upstream checkout must be clean; use a separate checkout")

    entries = {patch["file"]: patch for patch in manifest["patches"]}
    for name in manifest["profiles"][args.profile]["patches"]:
        patch = entries[name]
        directory = patch["directory"]
        options = (
            "--ignore-space-change", "--whitespace=nowarn",
            f"--directory={directory}", str(REPO / "patches" / name),
        )
        git(tree, "apply", "--check", *options)
        git(tree, "apply", *options)
        print(f"Applied {name}", flush=True)

    git(tree, "diff", "--check")
    if args.profile == "native-h3":
        # An allowlist covers *every* change, including newly added files. This
        # fails if future edits accidentally pull a transport patch into here.
        changed = set(git(tree, "diff", "--name-only", "HEAD", capture_output=True).stdout.splitlines())
        changed.update(git(tree, "ls-files", "--others", "--exclude-standard", capture_output=True).stdout.splitlines())
        if changed != {"src/net/tools/naive/naive_config.cc"}:
            raise RuntimeError(f"native-h3 changed unexpected files: {sorted(changed)}")
        print("Verified: only naive_config.cc differs; network stack unchanged")


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError, subprocess.CalledProcessError) as exc:
        print(f"apply-kernel-patches: {exc}", file=sys.stderr)
        sys.exit(1)

#!/usr/bin/env bash
# Build the native naive H3 server from a QUICHE checkout.
#
# The server is a QUICHE backend: it reuses QUICHE's own HTTP/3 stack so the
# server half of the connection advertises QUICHE's transport parameters and
# connection-id behaviour rather than quic-go's. See docs/native-h3-spike.md.
#
# This script prepares a checkout and builds //quiche:naivereal_h3_server. It does
# not vendor QUICHE: the tree is large and its revision is already pinned by
# upstream naiveproxy, so it is fetched and patched here instead.
#
# Usage:
#   h3native/build.sh [--dir PATH] [--print-revision] [--print-binary]
#
#   --dir PATH        use (or create) this QUICHE checkout. Defaults to the
#                     QUICHE_DIR environment variable, or a "quiche" directory
#                     beside this repository. Pass the checkout you already use
#                     for the kernel to avoid a second copy.
#   --print-binary    print the built binary's path and exit, so CI can locate
#                     the artifact without duplicating the checkout layout.
#   --print-revision  print the pinned QUICHE revision and exit.
set -euo pipefail

script_dir=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$script_dir/.." && pwd)

workdir=""
print_binary=""

while [ $# -gt 0 ]; do
  case $1 in
    --dir) workdir=$2; shift 2 ;;
    --print-binary) print_binary=1; shift ;;
    --print-revision) print_revision=1; shift ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

# The QUICHE revision is whatever upstream naiveproxy pins, so read it from there
# rather than duplicating the hash: a hardcoded copy would drift silently when
# CHROMIUM_VERSION moves.
#
# naiveproxy tags are per-release and the repository rebases to a new root commit
# each time, so the stable handle is the commit that the kernel workflow already
# pins. Keep this in step with NAIVE_UPSTREAM_COMMIT in
# .github/workflows/build-kernel.yml.
upstream_commit=${NAIVE_UPSTREAM_COMMIT:-3ba967e2d36cc133a896e81a36257ad4c6ea20f4}
deps_url="https://raw.githubusercontent.com/klzgrad/naiveproxy/${upstream_commit}/src/DEPS"
deps=$(curl -fsSL "$deps_url") || {
  echo "cannot fetch $deps_url to read the pinned QUICHE revision" >&2
  exit 1
}

# Guard against the pin moving without this script noticing.
chromium_version=$(cat "$repo/CHROMIUM_VERSION")
upstream_chromium=$(printf '%s\n' "$deps" | sed -n 's/.*"chromium_version": "\([^"]*\)".*/\1/p' | head -1)
if [ -n "$upstream_chromium" ] && [ "$upstream_chromium" != "$chromium_version" ]; then
  echo "upstream commit $upstream_commit is Chromium $upstream_chromium," >&2
  echo "but CHROMIUM_VERSION here says $chromium_version; update the pin." >&2
  exit 1
fi
quiche_revision=$(printf '%s\n' "$deps" | sed -n "s/.*'quiche_revision': '\([0-9a-f]*\)'.*/\1/p" | head -1)
if [ -z "$quiche_revision" ]; then
  echo "no quiche_revision found in $deps_url" >&2
  exit 1
fi

if [ -n "${print_revision:-}" ]; then
  echo "$quiche_revision"
  exit 0
fi

# One canonical location for the checkout, so --print-binary and the build agree.
if [ -z "$workdir" ]; then
  workdir=${QUICHE_DIR:-$(cd "$repo/.." && pwd)/quiche}
fi

if [ -n "$print_binary" ]; then
  echo "$workdir/bazel-bin/quiche/naivereal_h3_server"
  exit 0
fi

echo "Chromium $chromium_version (upstream $upstream_commit) pins QUICHE $quiche_revision"

# The package root inside the QUICHE repository is quiche/, and that is where the
# server sources must live: they include QUICHE's own headers as "quic/...", which
# only resolves inside that package.
pkg="$workdir/quiche"

if [ ! -d "$workdir/.git" ]; then
  echo "Cloning QUICHE into $workdir ..."
  mkdir -p "$(dirname "$workdir")"
  git clone --quiet --filter=blob:none https://quiche.googlesource.com/quiche "$workdir"
fi

echo "Checking out $quiche_revision ..."
git -C "$workdir" fetch --quiet origin "$quiche_revision" 2>/dev/null || true
git -C "$workdir" checkout --quiet "$quiche_revision"

# Start from a clean tree so a re-run is deterministic.
git -C "$workdir" checkout --quiet -- .
git -C "$workdir" clean -qfd -e bazel- -e 'bazel-*'

echo "Installing the server sources ..."
cp "$script_dir"/naive_*.cc "$script_dir"/naive_*.h "$pkg/"

echo "Applying QUICHE-tree patches ..."
for patch in "$script_dir"/patches/[0-9]*.patch; do
  echo "  $(basename "$patch")"
  git -C "$workdir" apply --verbose "$patch"
done

# QUICHE pins its Bazel version in .bazelversion, which bazelisk honours. A bare
# bazel works too if it happens to be the pinned version.
if command -v bazelisk >/dev/null 2>&1; then
  bazel_cmd=bazelisk
elif [ -x /opt/bin/bazelisk ]; then
  bazel_cmd=/opt/bin/bazelisk
elif command -v bazel >/dev/null 2>&1; then
  bazel_cmd=bazel
else
  echo "neither bazelisk nor bazel is available; install bazelisk" >&2
  echo "(QUICHE pins its own Bazel version in .bazelversion)" >&2
  exit 1
fi

echo
echo "Building //quiche:naivereal_h3_server with $bazel_cmd ..."
(cd "$workdir" && "$bazel_cmd" build //quiche:naivereal_h3_server)

binary=$(cd "$workdir" && "$bazel_cmd" info bazel-bin)/quiche/naivereal_h3_server
echo
echo "Built $binary"
echo
echo "Run it with:"
echo "  $binary --port=8443 \\"
echo "    --certificate_file=/path/fullchain.pem --key_file=/path/privkey.pem \\"
echo "    --web_root=/var/www/site --username=user --password=pass \\"
echo "    --upstream_addr=127.0.0.1:18080"
echo
echo "Congestion control is tunable without changing the wire shape; see"
echo "--bbr_cwnd_gain, --max_congestion_window and the pacing flags in --help."

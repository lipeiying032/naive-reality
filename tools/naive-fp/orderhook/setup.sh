#!/usr/bin/env bash
# Materialise the quic-go module that tools/naive-fp builds against.
#
# tools/naive-fp needs the peer's transport parameters *in wire order*, which
# stock quic-go discards while parsing (see tools/naive-fp/README.md). The two
# patches here add that capture hook to the exact apernet/quic-go revision the
# rest of this repository already pins.
#
# The result is written to tools/naive-fp/orderhook/quic-go, which is what
# tools/naive-fp/go.mod replaces the module with. That directory is generated and
# is not committed: the patches are the source of truth.
#
# Idempotent: re-running rebuilds the tree from the pinned revision.
#
# Why the commit and not a release tag: the pinned version is a pseudo-version
# (v0.61.1-0.20260806010916-184d081eef3e) over a fork branch, and apernet/quic-go
# publishes no matching release. Upstream's v0.61.0 tag also cannot be used
# directly, because the fork renames the module away from upstream's path and its
# own `.../internal/...` imports follow that rename; starting from the tag leaves
# those imports resolving against upstream, which refuses them as internal.
set -euo pipefail

# Resolve this script's directory first; a relative argument would otherwise be
# taken relative to the wrong base.
script_dir=$(cd "$(dirname "$0")" && pwd)
tool=$(cd "$script_dir/.." && pwd)
dest="$tool/orderhook/quic-go"
patchdir="$tool/orderhook"

# The revision tools/naive-fp/go.mod requires. Keep in step with h3frontend.
module_version=$(sed -n 's|^require github.com/apernet/quic-go \(v[^ ]*\)$|\1|p' "$tool/go.mod")
if [ -z "$module_version" ]; then
  echo "cannot read the pinned quic-go version from tools/naive-fp/go.mod" >&2
  exit 1
fi

# A pseudo-version looks like v0.61.1-0.20260806010916-184d081eef3e: a base tag,
# a timestamp, and the commit.
commit=${module_version##*-}
case $commit in
  *[!0-9a-f]* | '') echo "cannot derive a commit from $module_version" >&2; exit 1 ;;
esac
if [ ${#commit} -lt 12 ]; then
  echo "derived commit '$commit' looks too short" >&2
  exit 1
fi

# The commit lives on this branch. GitHub will not serve arbitrary unreferenced
# SHAs, so fetch the branch and check the commit out from it.
fork_branch=v0.61.0-mod-rename

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Fetching apernet/quic-go $fork_branch ..."
git init --quiet "$tmp/quic-go"
git -C "$tmp/quic-go" remote add origin https://github.com/apernet/quic-go.git

# Fail with an explanation rather than a bare git error: this branch is the one
# the requested commit lives on, and if it disappears the pinned version in
# go.mod has to be re-derived.
if ! git -C "$tmp/quic-go" ls-remote --exit-code --heads origin "$fork_branch" >/dev/null 2>&1; then
  echo "apernet/quic-go no longer publishes branch '$fork_branch';" >&2
  echo "re-derive the pinned version in tools/naive-fp/go.mod and update fork_branch here." >&2
  exit 1
fi

git -C "$tmp/quic-go" fetch --quiet --depth 50 origin "$fork_branch"
if ! git -C "$tmp/quic-go" cat-file -e "$commit^{commit}" 2>/dev/null; then
  echo "commit $commit is not reachable within 50 commits of $fork_branch;" >&2
  echo "increase the fetch depth or re-derive the pinned version." >&2
  exit 1
fi
git -C "$tmp/quic-go" checkout --quiet "$commit"

echo "Applying capture-hook patches ..."
for patch in "$patchdir"/[0-9]*.patch; do
  echo "  $(basename "$patch")"
  git -C "$tmp/quic-go" apply --verbose "$patch"
done

# Record what this tree was built from, so it is self-describing.
echo "$module_version" > "$tmp/quic-go/.naivefp-base-version"
rm -rf "$tmp/quic-go/.git"

mkdir -p "$(dirname "$dest")"
rm -rf "$dest"
mv "$tmp/quic-go" "$dest"
chmod -R u+w "$dest"

echo "Wrote $dest (commit $commit)"

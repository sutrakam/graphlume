#!/usr/bin/env bash
#
# build-mcpb.sh — build the MCP bundles (.mcpb) from the release archives.
#
# An .mcpb file is a ZIP archive holding manifest.json and the server it
# launches. A bundle of type "binary" carries one executable, so there is one
# bundle per platform, each built from the archive the release already
# publishes for that platform. Nothing is compiled here: the binary inside a
# bundle is byte for byte the binary inside the matching archive.
#
#   graphlume-mac.zip                        -> graphlume_<tag>_darwin_universal.mcpb
#   graphlume_<tag>_windows_amd64.zip        -> graphlume_<tag>_windows_amd64.mcpb
#   graphlume_<tag>_linux_amd64.tar.gz       -> graphlume_<tag>_linux_amd64.mcpb
#   graphlume_<tag>_linux_arm64.tar.gz       -> graphlume_<tag>_linux_arm64.mcpb
#
# It also writes MCPB_SHA256SUMS beside them, in `sha256sum` format.
#
# Usage:
#   scripts/release/build-mcpb.sh <version> <dist-dir> <out-dir>
#
#   <version>   the release tag, with or without the leading "v"
#   <dist-dir>  the directory holding the release archives
#   <out-dir>   where the bundles are written (created if missing)
#
# The manifest comes from packaging/mcpb/manifest.json. Three fields are set
# per bundle: version, compatibility.platforms (the one platform the bundle
# runs on) and server.entry_point (graphlume.exe on Windows).
set -euo pipefail

usage() { sed -n '2,/^set -euo/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'; }

if [ "$#" -ne 3 ]; then
  usage
  exit 2
fi

for tool in jq zip unzip tar; do
  command -v "$tool" >/dev/null 2>&1 || { echo "error: $tool is required" >&2; exit 1; }
done

ver="${1#v}"
# The bundle manifest requires a semantic version.
case "$ver" in
  [0-9]*.[0-9]*.[0-9]*) ;;
  *)
    # If a major floating tag like '1' is passed, resolve to the current semver tag at HEAD
    resolved="$(git tag --points-at HEAD "v*.*.*" 2>/dev/null | head -n1 || git describe --tags --match="v[0-9]*.[0-9]*.[0-9]*" 2>/dev/null || echo "")"
    if [[ "${resolved#v}" =~ ^[0-9]+\.[0-9]+\.[0-9]+ ]]; then
      ver="${resolved#v}"
    else
      echo "error: '$ver' is not a release version (want e.g. v1.2.3)" >&2
      exit 2
    fi
    ;;
esac
tag="v${ver}"
dist="$(cd "$2" && pwd)"
mkdir -p "$3"
out="$(cd "$3" && pwd)"
root="$(cd "$(dirname "$0")/../.." && pwd)"
manifest="$root/packaging/mcpb/manifest.json"

if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$@"; }
else
  sha256() { shasum -a 256 "$@"; }
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# build <archive> <platform> <binary> <suffix>
#   archive   file name inside <dist-dir>
#   platform  the manifest's platform name: darwin, win32 or linux
#   binary    the executable's name inside the archive
#   suffix    the bundle's file name suffix, e.g. linux_amd64
build() {
  local archive="$dist/$1" platform="$2" binary="$3" suffix="$4"
  local stage="$work/$suffix" bundle="$out/graphlume_${tag}_${suffix}.mcpb"

  [ -f "$archive" ] || { echo "error: missing release archive: $archive" >&2; exit 1; }
  mkdir -p "$stage/unpacked" "$stage/bundle/server"
  case "$archive" in
    *.zip) unzip -q "$archive" -d "$stage/unpacked" ;;
    *)     tar -xzf "$archive" -C "$stage/unpacked" ;;
  esac
  [ -f "$stage/unpacked/$binary" ] || { echo "error: $1 holds no $binary" >&2; exit 1; }

  cp "$stage/unpacked/$binary" "$stage/bundle/server/$binary"
  chmod 0755 "$stage/bundle/server/$binary"
  cp "$stage/unpacked/LICENSE" "$stage/unpacked/NOTICE" "$stage/bundle/"

  jq --arg v "$ver" --arg p "$platform" --arg e "server/$binary" \
    '.version = $v | .compatibility.platforms = [$p] | .server.entry_point = $e' \
    "$manifest" > "$stage/bundle/manifest.json"

  # -D leaves out directory entries. The reference unpacker (`mcpb unpack`,
  # @anthropic-ai/mcpb 2.1.2) opens every entry as a file and fails on a
  # "server/" entry, so a bundle zipped without -D does not install.
  rm -f "$bundle"
  (cd "$stage/bundle" && zip -q -9 -X -D -r "$bundle" .)
  echo "built $(basename "$bundle")"
}

build "graphlume-mac.zip"                    darwin graphlume     darwin_universal
build "graphlume_${tag}_windows_amd64.zip"   win32  graphlume.exe windows_amd64
build "graphlume_${tag}_linux_amd64.tar.gz"  linux  graphlume     linux_amd64
build "graphlume_${tag}_linux_arm64.tar.gz"  linux  graphlume     linux_arm64

(cd "$out" && sha256 graphlume_"${tag}"_*.mcpb > MCPB_SHA256SUMS)
cat "$out/MCPB_SHA256SUMS"

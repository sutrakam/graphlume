#!/usr/bin/env bash
#
# sbom.sh — write a CycloneDX SBOM for the binary inside each release archive.
#
# One SBOM per platform, because the dependency set is not the same on each:
# the Windows binary links modules the Linux one does not. Each SBOM is read
# out of the built binary itself (the module list the Go linker embeds, the
# same data `go version -m` prints), so it describes what shipped rather than
# what go.mod allows.
#
#   graphlume_<version>_<os>_<arch>.tar.gz|zip  ->  graphlume_<version>_<os>_<arch>.cdx.json
#
# The generator is cyclonedx-gomod, built from source at a pinned version by
# the Go toolchain. No account, token or hosted service is involved.
#
# Usage:
#   scripts/release/sbom.sh <version> <dist-dir> <out-dir>
#
#   <version>   the version in the archive names, exactly as published
#               (v1.2.3)
#   <dist-dir>  the directory holding the release archives
#   <out-dir>   where the SBOMs are written (created if missing)
#
# Environment:
#   CYCLONEDX_GOMOD  path to an existing cyclonedx-gomod binary; when unset the
#                    pinned version below is installed into a temporary GOBIN
set -euo pipefail

CYCLONEDX_GOMOD_VERSION="v1.12.0"

usage() { sed -n '2,/^set -euo/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'; }

if [ "$#" -ne 3 ]; then
  usage
  exit 2
fi

for tool in go unzip tar; do
  command -v "$tool" >/dev/null 2>&1 || { echo "error: $tool is required" >&2; exit 1; }
done

version="$1"
dist="$(cd "$2" && pwd)"
mkdir -p "$3"
out="$(cd "$3" && pwd)"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

gomod="${CYCLONEDX_GOMOD:-}"
if [ -z "$gomod" ]; then
  echo "installing cyclonedx-gomod ${CYCLONEDX_GOMOD_VERSION}"
  # GOOS/GOARCH are cleared so the tool is built for this machine even when the
  # caller's environment is set up for cross-compiling.
  GOBIN="$work/bin" GOOS="" GOARCH="" GOFLAGS="" \
    go install "github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@${CYCLONEDX_GOMOD_VERSION}"
  gomod="$work/bin/cyclonedx-gomod"
fi

count=0
for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64; do
  os="${target%/*}"
  arch="${target#*/}"
  name="graphlume_${version}_${os}_${arch}"
  binary="graphlume"
  mkdir -p "$work/$name"
  if [ "$os" = "windows" ]; then
    binary="graphlume.exe"
    archive="$dist/$name.zip"
    [ -f "$archive" ] || { echo "error: missing release archive: $archive" >&2; exit 1; }
    unzip -q "$archive" "$binary" -d "$work/$name"
  else
    archive="$dist/$name.tar.gz"
    [ -f "$archive" ] || { echo "error: missing release archive: $archive" >&2; exit 1; }
    tar -xzf "$archive" -C "$work/$name" "$binary"
  fi
  # `go version -m`, which the generator runs, refuses a file that is not
  # executable; unzip does not always restore the bit.
  chmod 0755 "$work/$name/$binary"

  # GOOS and GOARCH are what the generator writes into each component's
  # package URL. Left alone it would stamp this machine's platform on every
  # SBOM, including the Windows and macOS ones. -std adds the Go standard
  # library as a component, so a scanner can match the toolchain version too.
  GOOS="$os" GOARCH="$arch" "$gomod" bin -json -std \
    -version "$version" \
    -output "$out/$name.cdx.json" \
    "$work/$name/$binary"
  echo "wrote $name.cdx.json"
  count=$((count + 1))
done

echo "${count} SBOMs in ${out}"

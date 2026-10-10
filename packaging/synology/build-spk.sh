#!/bin/sh
# Wraps one linux binary in a DSM 7 package. GoReleaser calls this as a
# post-build hook for each target; see .goreleaser.yaml.
#
#   build-spk.sh <binary> <goarch> <major.minor.patch>
set -eu

if [ $# -ne 3 ]; then
    echo "usage: $0 <binary> <goarch> <major.minor.patch>" >&2
    exit 2
fi
BINARY=$1
GOARCH=$2
BASE_VERSION=$3

cd "$(dirname "$0")/../.."
SRC=packaging/synology

case "$GOARCH" in
    amd64) arch=x86_64 ;;
    arm64) arch=armv8 ;;
    *) echo "build-spk: no DSM arch for GOARCH=$GOARCH, skipping" >&2; exit 0 ;;
esac

# DSM wants versions like 1.2.3-45; the commit count keeps snapshots ordered
DSM_VERSION="$BASE_VERSION-$(git rev-list --count HEAD)"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/package/libexec" "$WORK/spk"

cp "$BINARY" "$WORK/package/libexec/fling"
cp -R "$SRC/package/bin" "$WORK/package/"

# keep macOS tar from adding ._ AppleDouble files
COPYFILE_DISABLE=1 tar -czf "$WORK/spk/package.tgz" -C "$WORK/package" .

cat > "$WORK/spk/INFO" <<INFO
package="fileflinger"
version="$DSM_VERSION"
os_min_ver="7.0-40000"
arch="$arch"
displayname="fileflinger"
description="Quickly host files from the command line"
maintainer="LtHummus"
maintainer_url="https://github.com/LtHummus/fileflinger"
support_url="https://github.com/LtHummus/fileflinger/issues"
INFO

cp -R "$SRC/conf" "$SRC/scripts" "$SRC/WIZARD_UIFILES" \
    "$SRC/PACKAGE_ICON.PNG" "$SRC/PACKAGE_ICON_256.PNG" "$WORK/spk/"

mkdir -p dist
out="dist/fileflinger_${DSM_VERSION}_dsm7_$arch.spk"
COPYFILE_DISABLE=1 tar -cf "$out" -C "$WORK/spk" \
    INFO package.tgz conf scripts WIZARD_UIFILES PACKAGE_ICON.PNG PACKAGE_ICON_256.PNG
echo "built $out"

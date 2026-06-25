#!/bin/bash
set -euo pipefail

cd "$(dirname "$(dirname "$(readlink -f "$0")")")"
ver="0.0.0~$(date -u +%Y%m%d%H%M%S)-1"
stage="$(mktemp -d)"
chmod 755 "$stage"

# TODO(bassosimone): correct only for amd64 and arm64
arch="$(go env GOARCH)"

set -x

# Build the binary.
install -d "$stage/usr/bin"
go build -ldflags="-s -w" -o "$stage/usr/bin/sonda" .
chmod 755 "$stage/usr/bin/sonda"

# Install systemd units.
install -d "$stage/lib/systemd/system"
install -m 644 dist/systemd/sonda-scan.service "$stage/lib/systemd/system/"
install -m 644 dist/systemd/sonda-scan.timer "$stage/lib/systemd/system/"

# Install copyright.
install -d "$stage/usr/share/doc/sonda"
install -m 644 dist/debian/copyright "$stage/usr/share/doc/sonda/"

# Install control file with substitutions.
install -d "$stage/DEBIAN"
sed -e "s/@VERSION@/$ver/g" -e "s/@ARCH@/$arch/g" \
    dist/debian/control > "$stage/DEBIAN/control"

# Install maintainer scripts.
install -m 755 dist/debian/postinst "$stage/DEBIAN/"
install -m 755 dist/debian/postrm "$stage/DEBIAN/"

# Generate md5sums.
( cd "$stage" && find . -type f -not -path './DEBIAN/*' -printf '%P\n' \
  | xargs -r md5sum > DEBIAN/md5sums )
chmod 644 "$stage/DEBIAN/md5sums"

dpkg-deb --root-owner-group --build "$stage" "sonda_${ver}_${arch}.deb"

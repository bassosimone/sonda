#!/bin/bash
set -euo pipefail

cd "$(dirname "$(dirname "$(readlink -f "$0")")")"
ver="$(git describe --tags | sed 's/^v//')~$(date -u +%Y%m%d%H%M%S)-1"
stage="$(mktemp -d)"
chmod 755 "$stage"

# TODO(bassosimone): correct only for amd64 and arm64
arch="$(go env GOARCH)"

set -x

# Build the binary.
install -d "$stage/usr/bin"
ldflags_buildcfg="github.com/bassosimone/sonda/internal/buildcfg"
go build -ldflags="-s -w -X $ldflags_buildcfg.Version=$ver" -o "$stage/usr/bin/sonda" .
chmod 755 "$stage/usr/bin/sonda"

# Install manpage.
install -d "$stage/usr/share/man/man1"
sed -e "s/@VERSION@/$ver/g" -e "s/@DATE@/$(date -u +%Y-%m-%d)/g" \
    man/sonda.1 > "$stage/usr/share/man/man1/sonda.1"
gzip -9n "$stage/usr/share/man/man1/sonda.1"
chmod 644 "$stage/usr/share/man/man1/sonda.1.gz"

# Install systemd units.
install -d "$stage/lib/systemd/system"
install -m 644 dist/systemd/sonda-scan.service "$stage/lib/systemd/system/"
install -m 644 dist/systemd/sonda-scan.timer "$stage/lib/systemd/system/"

# Install scan config file.
install -d "$stage/etc/sonda/scan"
install -m 644 etc/sonda/scan/default.yml "$stage/etc/sonda/scan/"

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

# Declare conffiles so dpkg preserves local edits on upgrade.
cat > "$stage/DEBIAN/conffiles" <<'CONFFILES'
/etc/sonda/scan/default.yml
CONFFILES
chmod 644 "$stage/DEBIAN/conffiles"

# Generate md5sums of every shipped file (everything outside DEBIAN/).
# Paths are filesystem-relative without a leading slash, per dpkg format.
( cd "$stage" && find . -type f -not -path './DEBIAN/*' -printf '%P\n' \
  | xargs -r md5sum > DEBIAN/md5sums )
chmod 644 "$stage/DEBIAN/md5sums"

dpkg-deb --root-owner-group --build "$stage" "sonda_${ver}_${arch}.deb"

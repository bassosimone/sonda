#!/bin/bash
# Build deps: git, go, objdump (computes the libc6 dependency), dpkg-deb.
set -euo pipefail

# Debian policy wants 0755 directories; `install -d` applies the build
# user's umask to the intermediate directories it creates.
umask 022

cd "$(dirname "$(dirname "$(readlink -f "$0")")")"
ver="$(git describe --tags | sed 's/^v//')~$(date -u +%Y%m%d%H%M%S)-1"
stage="$(mktemp -d)"
chmod 755 "$stage"

# TODO(bassosimone): correct only for amd64 and arm64
arch="$(go env GOARCH)"

set -x

# Build the binary.
#
# -buildmode=pie yields a PIE so the kernel can randomize the load
# address (ASLR); sonda runs unattended as a network client, so opt
# into hardening.
install -d "$stage/usr/bin"
ldflags_buildcfg="github.com/bassosimone/sonda/internal/buildcfg"
go build -buildmode=pie -ldflags="-s -w -X $ldflags_buildcfg.Version=$ver" -o "$stage/usr/bin/sonda" .
chmod 755 "$stage/usr/bin/sonda"

# Compute the libc6 version the binary actually requires: the highest
# GLIBC_x.y symbol version it references. This mirrors what
# dpkg-shlibdeps derives for real Debian packages.
libc_ver="$(objdump -T "$stage/usr/bin/sonda" \
    | grep -oE 'GLIBC_[0-9.]+' | sed 's/^GLIBC_//' | sort -uV | tail -1)"

# Install manpage.
install -d "$stage/usr/share/man/man1"
sed -e "s/@VERSION@/$ver/g" -e "s/@DATE@/$(date -u +%Y-%m-%d)/g" \
    dist/unix/usr/share/man/man1/sonda.1 > "$stage/usr/share/man/man1/sonda.1"
gzip -9n "$stage/usr/share/man/man1/sonda.1"
chmod 644 "$stage/usr/share/man/man1/sonda.1.gz"

# Install systemd units.
install -d "$stage/lib/systemd/system"
install -m 644 dist/unix/lib/systemd/system/sonda-scan.service "$stage/lib/systemd/system/"
install -m 644 dist/unix/lib/systemd/system/sonda-scan.timer "$stage/lib/systemd/system/"

# Install scan config file.
install -d "$stage/etc/sonda/scan"
install -m 644 dist/unix/etc/sonda/scan/default.yml "$stage/etc/sonda/scan/"

# Install copyright.
install -d "$stage/usr/share/doc/sonda"
install -m 644 dist/debian/copyright "$stage/usr/share/doc/sonda/"

# Install lintian overrides.
install -d "$stage/usr/share/lintian/overrides"
install -m 644 dist/debian/lintian-overrides "$stage/usr/share/lintian/overrides/sonda"

# Install control file with substitutions.
#
# Note: binary control files do not allow comments: strip them.
install -d "$stage/DEBIAN"
sed -e "s/@VERSION@/$ver/g" -e "s/@ARCH@/$arch/g" \
    -e "s/@LIBC@/$libc_ver/g" -e '/^#/d' \
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

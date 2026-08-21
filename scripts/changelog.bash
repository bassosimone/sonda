#!/bin/bash
set -euo pipefail

cd "$(dirname "$(dirname "$(readlink -f "$0")")")"

tag="$(git describe --tags --abbrev=0)"

git log --reverse \
    --pretty=tformat:'- %s https://github.com/bassosimone/sonda/commit/%H' \
    "$tag..HEAD"

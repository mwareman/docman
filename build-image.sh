#!/usr/bin/env bash
# Build the multi-architecture DocMan image (amd64, arm64, arm/v7, 386) as one
# image, tag it docman:<version> and docman:latest, and write the archives to dist/.
#
# Usage: ./build-image.sh [version] [--no-archives]
#   version        defaults to the newest release in NOTES.md
#   --no-archives  build and load the image only; leave dist/ alone
set -euo pipefail
cd "$(dirname "$0")"

PLATFORMS="linux/amd64,linux/arm64,linux/arm/v7,linux/386"
VERSION=""
ARCHIVES=1
for arg in "$@"; do
  case "$arg" in
    --no-archives) ARCHIVES=0 ;;
    -h|--help) sed -n '2,8p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    -*) echo "unknown option: $arg" >&2; exit 2 ;;
    *) VERSION="$arg" ;;
  esac
done

if [ -z "$VERSION" ]; then
  VERSION=$(grep -m1 -oE '^\| [0-9]+(\.[0-9]+)+ \|' NOTES.md | grep -oE '[0-9]+(\.[0-9]+)+' || true)
  [ -n "$VERSION" ] || { echo "could not read the version from NOTES.md; pass it as an argument" >&2; exit 1; }
fi
case "$VERSION" in
  1.*) ;;
  *) echo "refusing to build $VERSION: DocMan stays on 1.x until the owner says otherwise" >&2; exit 1 ;;
esac

echo "Building docman:$VERSION for $PLATFORMS"
# --load needs the containerd image store (Docker 25+) to keep every platform in
# one local image.
docker buildx build --platform "$PLATFORMS" \
  --build-arg "VERSION=$VERSION" \
  -t "docman:$VERSION" -t docman:latest --load .

if [ "$ARCHIVES" = 1 ]; then
  mkdir -p dist
  # Single-architecture archives load on any Docker version.
  for pair in amd64:linux/amd64 arm64:linux/arm64 armv7:linux/arm/v7 386:linux/386; do
    name=${pair%%:*}
    platform=${pair#*:}
    docker save --platform "$platform" "docman:$VERSION" docman:latest | gzip -9 > "dist/docman-$VERSION-$name.tar.gz"
  done
  # All four in one archive; the receiving host needs Docker 25+ with containerd.
  docker save "docman:$VERSION" docman:latest | gzip -9 > "dist/docman-$VERSION-multiarch.tar.gz"
  ls -l dist/docman-"$VERSION"-*.tar.gz
fi

echo "Done: docman:$VERSION and docman:latest"

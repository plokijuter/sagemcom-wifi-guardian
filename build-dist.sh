#!/bin/sh
# Builds the release binaries into dist/ (not committed; attach them to a GitHub release).
set -e
cd "$(dirname "$0")"
rm -rf dist && mkdir dist
for t in windows/amd64 linux/amd64 linux/arm64 darwin/arm64; do
	os=${t%/*}; arch=${t#*/}; ext=; [ "$os" = windows ] && ext=.exe
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags="-s -w" -o "dist/sagemcom-wifi-guardian-$os-$arch$ext" .
done
cd dist && sha256sum sagemcom-wifi-guardian-* > SHA256SUMS.txt && cat SHA256SUMS.txt

#!/bin/bash
set -euo pipefail

VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo "dev")}
BUILD_TIME=$(date -u '+%Y-%m-%dT%H:%M:%SZ')

LDFLAGS="-w -s -X main.Version=$VERSION -X main.BuildTime=$BUILD_TIME"

mkdir -p ./bin

# CGO_ENABLED=0 ensures fully static binaries for distroless/static containers
echo "Building binaries (version=$VERSION, build_time=$BUILD_TIME)..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$LDFLAGS" -o ./bin/substrate-bootstrap-linux-amd64 ./cmd/bootstrap
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="$LDFLAGS" -o ./bin/substrate-bootstrap-linux-arm64 ./cmd/bootstrap
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="$LDFLAGS" -o ./bin/substrate-bootstrap-darwin-arm64 ./cmd/bootstrap

chmod +x ./bin/substrate-bootstrap-*

echo "Build completed!"

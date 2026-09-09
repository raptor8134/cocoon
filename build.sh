#!/bin/sh
# Stop at the first failing target instead of silently continuing to the next.
set -e

# CLI generator (native)
go build -ldflags="-s -w" -o cocoon ./cmd/cocoon              # linux
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o cocoon.exe ./cmd/cocoon  # windows

# PWA (wasm core + web UI)
./scripts/build-web.sh

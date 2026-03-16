#!/bin/sh
#go build  -ldflags="-s -w" ./cmd/cocoon          # linux
#GOOS=windows GOARCH=amd64 CGO_ENABLED=1 \
#  CC=x86_64-w64-mingw32-gcc \
#  CXX=x86_64-w64-mingw32-g++ \
#  go build -ldflags="-s -w" ./cmd/cocoon/        # windows
go run cogentcore.org/core build web -debug       # wasm

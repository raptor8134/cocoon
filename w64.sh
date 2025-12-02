#!/bin/sh
GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=winegcc go build ./cmd/cocoon/

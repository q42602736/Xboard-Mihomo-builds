#!/bin/sh

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$SCRIPT_DIR"

mkdir -p dist

OUTPUT="dist/nexgen-client"

env GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$OUTPUT" .

echo "已生成 Linux amd64 二进制: $OUTPUT"

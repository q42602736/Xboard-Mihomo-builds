#!/bin/sh

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$SCRIPT_DIR"

mkdir -p dist

OUTPUT="dist/nexgen-client"
TMP_OUTPUT="${OUTPUT}.tmp"

: "${GOMAXPROCS:=2}"
: "${GOFLAGS:=-p=1}"
export GOMAXPROCS GOFLAGS

rm -f "$TMP_OUTPUT"

echo "开始生成 Linux amd64 二进制: $OUTPUT"
echo "构建限制: GOMAXPROCS=$GOMAXPROCS GOFLAGS=$GOFLAGS"

env GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$TMP_OUTPUT" .
mv "$TMP_OUTPUT" "$OUTPUT"

echo "已生成 Linux amd64 二进制: $OUTPUT"

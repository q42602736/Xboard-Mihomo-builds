#!/bin/sh

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$SCRIPT_DIR"

if [ ! -f .env ]; then
  echo "未找到 .env，请先上传并配置 .env 文件" >&2
  exit 1
fi

if [ -n "${APP_BINARY:-}" ]; then
  BINARY="$APP_BINARY"
else
  for candidate in ./dist/nexgen-client ./nexgen-client ./dist/nexgen-client-linux-amd64 ./nexgen-client-linux-amd64 ./xboard-config-server ./config-server; do
    if [ -f "$candidate" ]; then
      BINARY="$candidate"
      break
    fi
  done
fi

if [ -z "${BINARY:-}" ]; then
  echo "未找到可执行文件，请上传 ./dist/nexgen-client、./nexgen-client、./dist/nexgen-client-linux-amd64、./nexgen-client-linux-amd64、./xboard-config-server 或 ./config-server" >&2
  exit 1
fi

if [ ! -x "$BINARY" ]; then
  chmod +x "$BINARY"
fi

mkdir -p data

exec "$BINARY"

#!/bin/sh

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$SCRIPT_DIR"

ACTION=${1:-status}

case "$ACTION" in
  start)
    pm2 start ecosystem.config.js --only nexgen-client
    ;;
  restart)
    pm2 restart nexgen-client || pm2 start ecosystem.config.js --only nexgen-client
    ;;
  stop)
    pm2 stop nexgen-client
    ;;
  delete)
    pm2 delete nexgen-client
    ;;
  status)
    pm2 status nexgen-client
    ;;
  logs)
    pm2 logs nexgen-client
    ;;
  save)
    pm2 save
    ;;
  *)
    echo "用法: $0 {start|restart|stop|delete|status|logs|save}" >&2
    exit 1
    ;;
esac

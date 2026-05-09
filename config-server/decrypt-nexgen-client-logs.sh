#!/bin/sh
set -eu

CLIENT_DIR="/Users/caolin/Desktop/projects/Xboard-Mihomo_sub"
SCRIPT_PATH="$0"

DECRYPT_LOGS_COMMAND_NAME="$SCRIPT_PATH" exec "$CLIENT_DIR/decrypt_logs.sh" "$@"

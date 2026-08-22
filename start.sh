#!/usr/bin/env bash
#
# Build and run the bot. Ctrl+C stops it.
#
# Run from anywhere -- it cds to the repo first, because the bot reads
# config.env from the working directory and DATA_DIR defaults to it too.
#
set -euo pipefail

cd "$(dirname "$(readlink -f "$0")")"

if [[ ! -f config.env ]]; then
    echo "start.sh: config.env not found in $(pwd)" >&2
    echo "  cp config.env.example config.env   and fill it in" >&2
    exit 1
fi

go build -o nzbget-bot ./cmd/nzbget-bot

# exec, so Ctrl+C goes straight to the bot: it traps SIGINT and shuts its
# database and Telegram session down on the way out.
exec ./nzbget-bot

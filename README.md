# nzbget-usenet-bot-go

Telegram bot for NZBGet + NZBHydra search/grab/status workflows.

## Features
- `/search` with optional sorting (`--mx` / `--mn`) and pagination buttons.
- `/grab` to add NZBs from IDs or replied `.nzb` file.
- `/status` with live task stats, ETA, health, and quick `/cancel <id>`.
- `/server` for bot/server stats.

## Requirements
- Go 1.24+
- Telegram Bot Token + API credentials (API_ID/API_HASH)
- Running NZBGet instance (with XML-RPC enabled)
- Optional NZBHydra internal API setup

The bot talks MTProto through [gotd](https://github.com/gotd/td), so it needs
`API_ID`/`API_HASH` as well as the bot token. SQLite is the pure-Go
`modernc.org/sqlite`, so there is nothing to link against and no cgo.

## Installation
1. Clone repo.
2. Create/edit `config.env` (or copy from `config.env.example`).
3. Build:

```bash
go build -o bin/nzbget-bot ./cmd/nzbget-bot
```

`./start.sh` does this for you before launching.

## Configuration (`config.env`)
Required:
- `API_ID`
- `API_HASH`
- `BOT_TOKEN`
- `ADMINS` (JSON list of user IDs)
- `AUTH_CHATS` (JSON list of chat IDs)
- `NZBGET_BASE` (NZBGet XML-RPC URL)

Optional:
- `DOWNLOADER_NAME` (default: `NZBGet`)
- `SEARCH_RESULTS_PER_PAGE` (default: `5`)
- `AUTOREDACT` (default: `0` = off). Auto-redact search results after N seconds
- `DATA_DIR` (default: working directory). Where `log.txt`, `bot.db` and `session.json` are written
- `NZBHYDRA_API`
- `NZBHYDRA_API_URL`
- `NZBHYDRA_URL`

Example:
```env
AUTH_CHATS=[-1001234567890]
ADMINS=[123456789]
API_ID="12345678"
API_HASH="your_api_hash_here"
BOT_TOKEN="123456789:your_bot_token_here"
NZBGET_BASE="https://user:password@example.com/xmlrpc"
NZBHYDRA_API="your_nzbhydra_api_key"
NZBHYDRA_API_URL="https://user:password@example.com/api"
NZBHYDRA_URL="https://example.com/getnzb/api/{guid}?apikey={api_key}"
DOWNLOADER_NAME="NZBGet"
SEARCH_RESULTS_PER_PAGE="5"
# Auto-redact search results after N seconds. 0 disables.
AUTOREDACT="0"
```

## Run
```bash
./start.sh    # builds, then runs in the foreground. Ctrl+C stops it.
```
`start.sh` can be run from any directory; it cds to the repo first, since the
bot reads `config.env` from the working directory.

Any setting can be overridden for a single run without editing `config.env` —
the environment wins over the file:
```bash
BOT_TOKEN="123:abc" DATA_DIR=/tmp/bot-test ./start.sh
```

To launch directly instead:
```bash
go run ./cmd/nzbget-bot
```

## Commands
- `/help` - Show command list
- `/search <query> [--mx|--mn]` - Search NZBs
- `/grab <encrypted-id(s)>` - Add NZB(s)
- `/status` - Live NZBGet queue status
- `/cancel <gid>` - Cancel task(s)
- `/server` - Bot + system stats
- `/logs` - Send bot logs (admin only)
- `/clear` - Wipe NZBGet queue + history (admin only)
- `/auth` - Authorize chat/user (admin only)
- `/unauth` - Remove authorization (admin only)

### BotFather command menu

To populate the command menu in Telegram clients, send `/setcommands` to [@BotFather](https://t.me/BotFather), pick the bot, and paste:

```text
help - Show the bot commands
search - [query] search NZBs. Flags: --mx largest first, --mn smallest first
grab - [ID-1] [ID-2]... add NZBs from search results, or reply to a .nzb file
status - Live NZBGet queue status
cancel - [ID-1] [ID-2]... cancel queued downloads
server - NZBGet and system stats
logs - Bot log file (admin only)
clear - Wipe the NZBGet queue and history (admin only)
auth - [id] authorize a user or chat (admin only)
unauth - [id] remove authorization (admin only)
```

The admin commands are listed for completeness; non-admins are rejected either way. To keep them out of the public menu, set the shorter list for the "All private chats" scope and the full list for your own chat only.

## Data files
The bot writes `log.txt`, `bot.db` (SQLite) and `session.json` to `DATA_DIR`, which defaults
to the working directory it was started from — the same place `config.env` is
read from. Set `DATA_DIR` to keep them somewhere fixed regardless of where the
bot is launched.

`bot.db` stores chats authorized with `/auth` so they survive a restart. IDs
listed in `AUTH_CHATS` are seeded from `config.env` on every start, so
`/unauth` on one of those only lasts until the next restart — remove it from
`config.env` to make that permanent.

`session.json` holds the MTProto login and is created on first start; the old
Pyrogram `bot.session` is not read and can be deleted.

## Notes
- Restart bot after changing `config.env`.
- Keep `config.env` out of git (contains secrets).

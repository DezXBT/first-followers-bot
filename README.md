<!-- discord bot, twitter followers tracker, x tracker, first followers, go bot, graphql api, username history, smart followers, frontrun -->
# 🏆 First Followers Bot

[![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go)](https://go.dev/)
[![Discord](https://img.shields.io/badge/Discord-bot-5865F2?logo=discord)](https://discord.com/developers)
[![License](https://img.shields.io/badge/License-MIT-green)](LICENSE)

A Discord bot that tracks the earliest followers of any Twitter/X account, checks username & bio history, and identifies smart followers — all without the official Twitter API. Built in Go using Twitter's internal GraphQL API + Frontrun.

---

## 📑 Table of Contents

- [Features](#-features)
- [Commands](#-commands)
- [Embed Previews](#-embed-previews)
- [Prerequisites](#-prerequisites)
- [Installation](#-installation)
- [Configuration](#-configuration)
- [Running](#-running)
- [Architecture](#-architecture)
- [How It Works](#-how-it-works)
- [Security](#-security)
- [FAQ](#-faq)
- [License](#-license)

---

## ✨ Features

- **Deep Crawl Followers** — Paginates through all followers (up to 10k) to find the earliest 20
- **Username History** — Fetches all historical usernames via Frontrun API
- **Bio History** — Tracks bio changes over time (latest 5 shown)
- **Smart Followers** — Identifies high-value followers ranked by their own follower count
- **Profile Overview** — Followers count, account location, source, creation date
- **X GraphQL API** — Uses Twitter's internal API with cookie authentication (no official API key needed)
- **Cookie Pool Rotation** — Round-robin rotation across multiple X sessions with auth error fallback
- **Transaction ID Generation** — Generates valid `X-Client-Transaction-Id` headers for API requests
- **Parallel Fetching** — `.cek` fetches all data sources in parallel for fast response times
- **Progress Updates** — Real-time status updates during long-running `.first` crawls
- **Cooldown System** — Configurable per-user cooldown to prevent abuse
- **Rich Embeds** — Gold-themed embeds with requester avatar in footer, clickable profile links
- **Guild & Channel Allowlists** — Restrict bot to specific servers and channels

---

## 📋 Commands

| Command | Description |
|---------|-------------|
| `.first <handle>` | Deep crawl all followers, find the earliest ~20 |
| `.cek <handle>` | Full profile check: username history, bio changes, smart followers, profile stats |

### `.first <handle>`

Crawls all followers of the target account (newest → oldest), deduplicates, and returns the 20 oldest followers. Shows:

- Target's display name, @handle, follower count, and profile picture
- Top 20 earliest followers with links to their profiles
- Crawling time

**Example:** `.first elonmusk`

### `.cek <handle>`

Parallel fetches data from Frontrun API + X GraphQL. Shows:

- 📊 Followers count
- 🧠 Smart followers count (high-value accounts that follow them)
- 🌍 Account location (based in)
- 📱 Source (Twitter client used)
- 📅 Account creation date
- 🔄 Total username changes
- 📝 Bio changes count
- 📋 Full username history with timestamps
- 📝 Latest 5 bio changes
- 🏆 Top smart followers ranked by their follower count

**Example:** `.cek elonmusk`

---

## 🖼 Embed Previews

Both commands return a gold-themed Discord embed with:

- **Thumbnail** — Target's profile picture (400×400)
- **Title** — Command-specific (🏆 for `.first`, 📋 for `.cek`)
- **Description** — Profile stats and data
- **Fields** — Formatted results with clickable links
- **Footer** — Requester's Discord avatar + "Requested by {name} • {X}m cooldown | Today at {time}"

---

## 🔧 Prerequisites

- **Go 1.21+** installed ([download](https://go.dev/dl/))
- **Discord Bot Token** from [Discord Developer Portal](https://discord.com/developers/applications)
- **Twitter/X Cookies** (`auth_token` and `ct0`) from a logged-in browser session
- **Frontrun Session Token** (for `.cek` command features)

### Getting Twitter Cookies

1. Log in to [x.com](https://x.com) in your browser
2. Open DevTools → Application → Cookies → `https://x.com`
3. Copy `auth_token` and `ct0` values

### Getting Frontrun Token

1. Log in to [frontrun.pro](https://frontrun.pro)
2. Open DevTools → Application → Cookies → `https://frontrun.pro`
3. Copy the `__Secure-frontrun.session_token` value

---

## 📦 Installation

### Clone and build

```bash
git clone https://github.com/DezXBT/first-followers-bot.git
cd first-followers-bot
go mod tidy
cp .env.example .env
# Edit .env with your credentials (see Configuration below)
go build -o first-followers .
```

### Run

```bash
./first-followers
```

### Run in background (screen)

```bash
screen -dmS followers ./first-followers
# Reattach: screen -r followers
# Detach: Ctrl+A, D
```

---

## ⚙️ Configuration

All configuration is done via environment variables (`.env` file). See [`.env.example`](.env.example) for a complete template.

### Discord

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `DISCORD_BOT_TOKEN` | ✅ | — | Discord bot token |
| `ALLOWED_GUILD_IDS` | ❌ | *(all)* | Comma-separated guild IDs (deny-all if empty) |
| `DISCORD_CHANNEL_IDS` | ❌ | *(all)* | Global channel allowlist |
| `FIRST_CHANNEL_IDS` | ❌ | *(all)* | Channel allowlist for `.first` only |
| `CHECK_CHANNEL_IDS` | ❌ | *(all)* | Channel allowlist for `.cek` only |

### Twitter/X Cookies

| Variable | Required | Description |
|----------|----------|-------------|
| `X_AUTH_TOKEN` | ✅* | Single `auth_token` cookie |
| `X_CT0` | ✅* | Single `ct0` cookie |
| `X_AUTH_TOKENS` | ✅* | Comma-separated `auth_token`s for pool rotation |
| `X_CT0S` | ✅* | Comma-separated `ct0`s for pool rotation |

> Use either single (`X_AUTH_TOKEN`/`X_CT0`) **or** pool (`X_AUTH_TOKENS`/`X_CT0S`), not both. Pool entries must have equal counts.

### Frontrun API

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `FRONTRUN_SESSION_TOKEN` | ❌ | — | Frontrun session token |
| `FRONTRUN_BASE_URL` | ❌ | `https://loadbalance.frontrun.pro` | API base URL |
| `FRONTRUN_CLIENT_VERSION` | ❌ | `0.0.216` | Client version header |
| `FRONTRUN_CLIENT_LANGUAGE` | ❌ | `EN_US` | Client language header |

### Behavior

| Variable | Default | Description |
|----------|---------|-------------|
| `BOT_PREFIX` | `.first` | Command prefix for first followers |
| `CHECK_PREFIX` | `.cek` | Command prefix for username check |
| `DEEP_PAGE_SIZE` | `50` | Followers per API page |
| `DEEP_MAX_PAGES` | `200` | Max pages to crawl (50 × 200 = 10k followers max) |
| `DEEP_DELAY_MS` | `1500` | Delay between API calls (ms) — increase if rate-limited |
| `PROGRESS_UPDATE_MS` | `15000` | Progress update interval (ms) |
| `FIRST_COOLDOWN_MS` | `90000` | Cooldown per user (ms) — default 90s |
| `TIMEZONE` | `Asia/Jakarta` | Timezone for embed timestamps |
| `LOG_LEVEL` | `info` | Logging level |

---

## 🏗 Architecture

```
first-followers-bot/
├── main.go           # Entry point, config loading, bot startup
├── config.go         # .env loading via godotenv, validation
├── cookie_pool.go    # Round-robin cookie rotation with auth fallback
├── transaction.go    # X-Client-Transaction-Id generator (reverse-engineered)
├── twitter.go        # Twitter internal GraphQL API client
├── frontrun.go       # Frontrun API client (username/bio history, smart followers)
├── discord.go        # Discord bot handlers, embed builders, command routing
├── .env.example      # Configuration template
└── go.mod            # Go module definition
```

### Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/bwmarrin/discordgo` | Discord API wrapper |
| `github.com/joho/godotenv` | `.env` file loading |

---

## 🔄 How It Works

### `.first` Flow

1. User sends `.first @handle` in Discord
2. Bot checks guild/channel allowlists and per-user cooldown
3. Sends "🔍 Analyzing @handle..." placeholder message
4. Fetches user info via `UserByScreenName` GraphQL endpoint
5. Calculates total pages: `ceil(followers_count / page_size)`
6. Deep crawls followers with cursor pagination (newest first, with configurable delay)
7. Sends progress updates every 15s during crawl
8. Deduplicates followers and extracts the 20 oldest
9. Builds gold embed with target profile picture + oldest 20 followers
10. Edits placeholder message with final result

### `.cek` Flow

1. User sends `.cek @handle` in Discord
2. Sends "🔍 Checking @handle..." placeholder message
3. **Parallel fetch** (all at once):
   - Username history from Frontrun API
   - Bio history from Frontrun API
   - Smart followers from Frontrun API
   - User info from Frontrun API (v3)
   - Profile data from X GraphQL API
4. Merges data from all sources
5. Builds comprehensive embed with all stats, history, and top smart followers
6. Edits placeholder message with final result

### Cookie Pool

- Multiple X sessions rotate round-robin on each request
- If a request returns 401/403, that cookie is skipped and the next one is tried
- Cookies are stored in memory only (loaded from env vars at startup)

---

## 🔒 Security

- **No secrets in code** — All credentials loaded from environment variables
- **Guild allowlist** — If `ALLOWED_GUILD_IDS` is set, messages from other servers are ignored
- **Channel allowlists** — Global and per-command channel restrictions
- **Per-user cooldown** — Configurable cooldown on `.first` to prevent abuse
- **Cookie rotation** — Multiple X sessions rotate; auth errors trigger automatic fallback
- **In-memory cookies** — Cookies are never written to disk by the bot

---

## ❓ FAQ

**Q: Why does the Followers endpoint hash change?**
A: Twitter periodically updates their internal GraphQL endpoint hashes. The bot tries multiple known hashes automatically. If all fail, update the `followersHashes` slice in `twitter.go`.

**Q: Do I need a Twitter API key?**
A: No. This bot uses Twitter's internal GraphQL API with cookie authentication. You only need `auth_token` and `ct0` cookies from a logged-in browser session.

**Q: Why does `.first` take so long?**
A: Deep crawling all followers requires many paginated API calls with delays to avoid rate limiting. For accounts with many followers, this can take several minutes. Progress updates are sent during the crawl.

**Q: Can I use multiple X accounts?**
A: Yes. Set `X_AUTH_TOKENS` and `X_CT0S` with comma-separated values. The bot rotates through them round-robin and automatically skips accounts that get auth errors.

**Q: What are "Smart Followers"?**
A: Smart Followers are accounts that follow the target and have a high follower count themselves — indicating high-value or influential followers. The `.cek` command shows the top ones ranked by their follower count.

**Q: What happens if Frontrun API is down?**
A: The `.cek` command gracefully handles failures — sections that fail are skipped, and whatever data was successfully fetched is still displayed. The `.first` command doesn't depend on Frontrun at all.

**Q: How do I avoid rate limits?**
A: Increase `DEEP_DELAY_MS` (default 1500ms). The bot also rotates cookies and uses `X-Client-Transaction-Id` headers to reduce rate limit triggers.

---

## 📄 License

MIT

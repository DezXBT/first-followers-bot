<!-- discord bot, twitter followers tracker, x tracker, first followers, go bot, graphql api, username history -->
# 🏆 First Followers Bot

[![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go)](https://go.dev/)
[![Discord](https://img.shields.io/badge/Discord-bot-5865F2?logo=discord)](https://discord.com/developers)
[![License](https://img.shields.io/badge/License-MIT-green)](LICENSE)

A Discord bot that tracks the earliest followers of any Twitter/X account and fetches username history. Built in Go using Twitter's internal GraphQL API.

---

## 📑 Table of Contents

- [Features](#-features)
- [Commands](#-commands)
- [Prerequisites](#-prerequisites)
- [Installation](#-installation)
- [Configuration](#-configuration)
- [Running](#-running)
- [Docker](#-docker)
- [Architecture](#-architecture)
- [Security](#-security)
- [FAQ](#-faq)

---

## ✨ Features

- **Deep Crawl Followers** — Paginates through all followers to find the earliest 20
- **Username History** — Fetches historical usernames via Frontrun API
- **X GraphQL API** — Uses Twitter's internal API with cookie authentication (no official API key needed)
- **Cookie Pool Rotation** — Round-robin rotation across multiple X sessions with auth error fallback
- **Transaction ID Generation** — Generates valid `X-Client-Transaction-Id` headers
- **Progress Updates** — Real-time status updates during long-running crawls
- **Cooldown System** — 90-second cooldown on `.first` to prevent abuse
- **Guild & Channel Allowlists** — Restrict bot to specific servers and channels

---

## 📋 Commands

| Command | Description |
|---------|-------------|
| `.first <handle>` | Deep crawl followers, find the earliest ~20 |
| `.cek <handle>` | Username history via Frontrun API + X GraphQL profile info |

---

## 🔧 Prerequisites

- **Go 1.21+** installed
- **Discord Bot Token** from [Discord Developer Portal](https://discord.com/developers/applications)
- **Twitter/X Cookies** (`auth_token` and `ct0`) from a logged-in session
- **Frontrun Session Token** (optional, for `.cek` command)

---

## 📦 Installation

### Step 1: Clone the repository

```bash
git clone https://github.com/your-org/first-followers-bot.git
cd first-followers-bot
```

### Step 2: Install dependencies

```bash
go mod tidy
```

### Step 3: Create your configuration

```bash
cp .env.example .env
```

### Step 4: Edit `.env` with your credentials

```env
DISCORD_BOT_TOKEN=your_discord_bot_token
X_AUTH_TOKEN=your_twitter_auth_token
X_CT0=your_twitter_ct0
```

### Step 5: Build

```bash
go build -o first-followers-bot .
```

### Step 6: Run

```bash
./first-followers-bot
```

---

## ⚙️ Configuration

All configuration is done via environment variables (`.env` file).

### Discord

| Variable | Required | Description |
|----------|----------|-------------|
| `DISCORD_BOT_TOKEN` | ✅ | Discord bot token |
| `ALLOWED_GUILD_IDS` | ❌ | Comma-separated guild IDs (deny-all if empty) |
| `DISCORD_CHANNEL_IDS` | ❌ | Global channel allowlist |
| `FIRST_CHANNEL_IDS` | ❌ | Channel allowlist for `.first` |
| `CHECK_CHANNEL_IDS` | ❌ | Channel allowlist for `.cek` |

### Twitter/X Cookies

| Variable | Required | Description |
|----------|----------|-------------|
| `X_AUTH_TOKEN` | ✅* | Single auth_token cookie |
| `X_CT0` | ✅* | Single ct0 cookie |
| `X_AUTH_TOKENS` | ✅* | Comma-separated auth_tokens for pool |
| `X_CT0S` | ✅* | Comma-separated ct0s for pool |

*Use either single or pool variants, not both.

### Frontrun API

| Variable | Required | Description |
|----------|----------|-------------|
| `FRONTRUN_SESSION_TOKEN` | ❌ | Frontrun session token |
| `FRONTRUN_BASE_URL` | ❌ | API base URL (default: `https://loadbalance.frontrun.pro`) |
| `FRONTRUN_CLIENT_VERSION` | ❌ | Client version header (default: `0.0.216`) |
| `FRONTRUN_CLIENT_LANGUAGE` | ❌ | Client language header (default: `EN_US`) |

### Behavior

| Variable | Default | Description |
|----------|---------|-------------|
| `BOT_PREFIX` | `.first` | Command prefix for first followers |
| `CHECK_PREFIX` | `.cek` | Command prefix for username check |
| `DEEP_PAGE_SIZE` | `50` | Followers per page |
| `DEEP_MAX_PAGES` | `200` | Max pages to crawl |
| `DEEP_DELAY_MS` | `1500` | Delay between API calls (ms) |
| `PROGRESS_UPDATE_MS` | `15000` | Progress update interval (ms) |
| `FIRST_COOLDOWN_MS` | `90000` | Cooldown for `.first` command (ms) |
| `TIMEZONE` | `Asia/Jakarta` | Timezone for embed footers |
| `LOG_LEVEL` | `info` | Logging level |

---

## 🐳 Docker

```bash
docker build -t first-followers-bot .
docker run --env-file .env first-followers-bot
```

---

## 🏗 Architecture

```
├── main.go           # Entry point, config loading, bot startup
├── config.go         # .env loading via godotenv, validation
├── cookie_pool.go    # Round-robin cookie rotation with auth fallback
├── transaction.go    # X-Client-Transaction-Id generator (reverse-engineered)
├── twitter.go        # Twitter internal GraphQL API client
├── frontrun.go       # Frontrun API client for username history
├── discord.go        # Discord bot handlers, embed builders, command routing
├── .env.example      # Configuration template
└── .gitignore        # Git ignore rules
```

### Request Flow

1. User sends `.first @handle` in Discord
2. Bot checks guild/channel allowlists and cooldown
3. Fetches user info via `UserByScreenName` GraphQL endpoint
4. Deep crawls followers with cursor pagination (newest first)
5. Deduplicates and extracts the 20 oldest followers
6. Builds gold embed with results

---

## 🔒 Security

- **Guild Allowlist**: If `ALLOWED_GUILD_IDS` is set, the bot ignores messages from other servers
- **Channel Allowlists**: Global and per-command channel restrictions
- **Cooldown**: 90-second cooldown on `.first` per user to prevent abuse
- **Cookie Rotation**: Multiple X sessions rotate on each request; auth errors trigger fallback
- **No Secrets in Code**: All credentials loaded from environment variables

---

## ❓ FAQ

**Q: Why does the Followers endpoint hash change?**
A: Twitter periodically updates their internal GraphQL endpoint hashes. The bot tries multiple known hashes automatically. If all fail, update the `followersHashes` slice in `twitter.go`.

**Q: Do I need a Twitter API key?**
A: No. This bot uses Twitter's internal GraphQL API with cookie authentication. You only need `auth_token` and `ct0` cookies from a logged-in browser session.

**Q: Why does `.first` take so long?**
A: Deep crawling all followers requires many paginated API calls with delays to avoid rate limiting. For accounts with millions of followers, this can take several minutes.

**Q: Can I use multiple X accounts?**
A: Yes. Set `X_AUTH_TOKENS` and `X_CT0S` with comma-separated values. The bot rotates through them round-robin.

---

## 📄 License

MIT

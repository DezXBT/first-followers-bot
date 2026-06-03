package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	DiscordToken      string
	AllowedGuildIDs   []string
	DiscordChannelIDs []string
	FirstChannelIDs   []string
	CheckChannelIDs   []string

	// Twitter cookies
	AuthTokens []string
	Ct0s       []string

	BotPrefix   string
	CheckPrefix string

	// Frontrun
	FrontrunBaseURL        string
	FrontrunSessionToken   string
	FrontrunClientVersion  string
	FrontrunClientLanguage string

	// Deep crawl
	DeepPageSize    int
	DeepMaxPages    int
	DeepDelayMs     int
	ProgressUpdateMs int

	FirstCooldownMs int

	FirstFollowersLimit int

	Timezone string
	LogLevel string
}

func LoadConfig() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		DiscordToken:           os.Getenv("DISCORD_BOT_TOKEN"),
		AllowedGuildIDs:        splitComma(os.Getenv("ALLOWED_GUILD_IDS")),
		DiscordChannelIDs:     splitComma(os.Getenv("DISCORD_CHANNEL_IDS")),
		FirstChannelIDs:       splitComma(os.Getenv("FIRST_CHANNEL_IDS")),
		CheckChannelIDs:       splitComma(os.Getenv("CHECK_CHANNEL_IDS")),
		BotPrefix:              envDefault("BOT_PREFIX", ".first"),
		CheckPrefix:            envDefault("CHECK_PREFIX", ".cek"),
		FrontrunBaseURL:       envDefault("FRONTRUN_BASE_URL", "https://loadbalance.frontrun.pro"),
		FrontrunSessionToken:  os.Getenv("FRONTRUN_SESSION_TOKEN"),
		FrontrunClientVersion: envDefault("FRONTRUN_CLIENT_VERSION", "0.0.216"),
		FrontrunClientLanguage: envDefault("FRONTRUN_CLIENT_LANGUAGE", "EN_US"),
		DeepPageSize:          envInt("DEEP_PAGE_SIZE", 50),
		DeepMaxPages:          envInt("DEEP_MAX_PAGES", 200),
		DeepDelayMs:           envInt("DEEP_DELAY_MS", 1500),
		ProgressUpdateMs:      envInt("PROGRESS_UPDATE_MS", 15000),
		FirstCooldownMs:       envInt("FIRST_COOLDOWN_MS", 90000),
		FirstFollowersLimit:   envInt("FIRST_FOLLOWERS_LIMIT", 20),
		Timezone:              envDefault("TIMEZONE", "Asia/Jakarta"),
		LogLevel:              envDefault("LOG_LEVEL", "info"),
	}

	// Parse X cookies — prefer pool vars, fall back to single vars
	cfg.AuthTokens = splitComma(os.Getenv("X_AUTH_TOKENS"))
	cfg.Ct0s = splitComma(os.Getenv("X_CT0S"))
	if len(cfg.AuthTokens) == 0 {
		if v := os.Getenv("X_AUTH_TOKEN"); v != "" {
			cfg.AuthTokens = []string{v}
		}
	}
	if len(cfg.Ct0s) == 0 {
		if v := os.Getenv("X_CT0"); v != "" {
			cfg.Ct0s = []string{v}
		}
	}

	if cfg.DiscordToken == "" {
		return nil, fmt.Errorf("DISCORD_BOT_TOKEN is required")
	}
	if len(cfg.AuthTokens) == 0 || len(cfg.Ct0s) == 0 {
		return nil, fmt.Errorf("X_AUTH_TOKEN/X_CT0 (or X_AUTH_TOKENS/X_CT0S) required")
	}
	if len(cfg.AuthTokens) != len(cfg.Ct0s) {
		return nil, fmt.Errorf("X_AUTH_TOKENS and X_CT0S must have the same number of entries")
	}

	return cfg, nil
}

func splitComma(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

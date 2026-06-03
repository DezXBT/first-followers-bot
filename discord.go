package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	ColorGold    = 0xFFD700
	ColorBlurple = 0x5865F2
)

type Bot struct {
	session        *discordgo.Session
	config         *Config
	twitter        *TwitterClient
	frontrun       *FrontrunClient
	cookiePool     *CookiePool
	cooldowns      map[string]time.Time
	cooldownMu     sync.Mutex
	timezone       *time.Location
}

func NewBot(cfg *Config) (*Bot, error) {
	dg, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		return nil, fmt.Errorf("create discord session: %w", err)
	}

	cookiePool := NewCookiePool(cfg.AuthTokens, cfg.Ct0s)
	twitter := NewTwitterClient(cookiePool)
	frontrun := NewFrontrunClient(
		cfg.FrontrunBaseURL,
		cfg.FrontrunSessionToken,
		cfg.FrontrunClientVersion,
		cfg.FrontrunClientLanguage,
	)

	tz, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		fmt.Printf("[discord] Warning: invalid timezone %q, using UTC: %v\n", cfg.Timezone, err)
		tz = time.UTC
	}

	b := &Bot{
		session:    dg,
		config:     cfg,
		twitter:    twitter,
		frontrun:   frontrun,
		cookiePool: cookiePool,
		cooldowns:  make(map[string]time.Time),
		timezone:   tz,
	}

	dg.AddHandler(b.messageCreate)
	dg.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages

	return b, nil
}

func (b *Bot) Start() error {
	return b.session.Open()
}

func (b *Bot) Stop() {
	b.session.Close()
}

func (b *Bot) messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.Bot {
		return
	}

	content := strings.TrimSpace(m.Content)

	// Check for command prefix
	var command string
	var args string
	if strings.HasPrefix(content, b.config.BotPrefix) {
		command = "first"
		args = strings.TrimSpace(content[len(b.config.BotPrefix):])
	} else if strings.HasPrefix(content, b.config.CheckPrefix) {
		command = "cek"
		args = strings.TrimSpace(content[len(b.config.CheckPrefix):])
	} else {
		return
	}

	// Guild allowlist check
	if len(b.config.AllowedGuildIDs) > 0 && m.GuildID != "" {
		allowed := false
		for _, gid := range b.config.AllowedGuildIDs {
			if m.GuildID == gid {
				allowed = true
				break
			}
		}
		if !allowed {
			return
		}
	}

	// Channel allowlist check
	if !b.isChannelAllowed(m.ChannelID, command) {
		return
	}

	handle := normalizeHandle(args)
	if handle == "" {
		s.ChannelMessageSend(m.ChannelID, "❌ Please provide a Twitter handle. Usage: `"+b.config.BotPrefix+" <handle>`")
		return
	}

	switch command {
	case "first":
		b.handleFirstCommand(s, m, handle)
	case "cek":
		b.handleCekCommand(s, m, handle)
	}
}

func (b *Bot) isChannelAllowed(channelID, command string) bool {
	// Check global channel allowlist
	if len(b.config.DiscordChannelIDs) > 0 {
		found := false
		for _, cid := range b.config.DiscordChannelIDs {
			if channelID == cid {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Check command-specific channel allowlist
	switch command {
	case "first":
		if len(b.config.FirstChannelIDs) > 0 {
			for _, cid := range b.config.FirstChannelIDs {
				if channelID == cid {
					return true
				}
			}
			return false
		}
	case "cek":
		if len(b.config.CheckChannelIDs) > 0 {
			for _, cid := range b.config.CheckChannelIDs {
				if channelID == cid {
					return true
				}
			}
			return false
		}
	}

	return true
}

func (b *Bot) checkCooldown(userID string) (bool, time.Duration) {
	b.cooldownMu.Lock()
	defer b.cooldownMu.Unlock()

	if last, ok := b.cooldowns[userID]; ok {
		elapsed := time.Since(last)
		cooldown := time.Duration(b.config.FirstCooldownMs) * time.Millisecond
		if elapsed < cooldown {
			return true, cooldown - elapsed
		}
	}
	return false, 0
}

func (b *Bot) setCooldown(userID string) {
	b.cooldownMu.Lock()
	defer b.cooldownMu.Unlock()
	b.cooldowns[userID] = time.Now()
}

// handleFirstCommand handles the .first command — deep crawl followers, find earliest ~20
func (b *Bot) handleFirstCommand(s *discordgo.Session, m *discordgo.MessageCreate, handle string) {
	// Cooldown check
	if onCooldown, remaining := b.checkCooldown(m.Author.ID); onCooldown {
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("⏳ Cooldown active. Please wait %s.", remaining.Round(time.Second)))
		return
	}

	// Send initial "analyzing" message
	msg, err := s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("🔍 Analyzing @%s...", handle))
	if err != nil {
		return
	}

	startTime := time.Now()

	// Progress update goroutine
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Duration(b.config.ProgressUpdateMs) * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				elapsed := time.Since(startTime)
				elapsedSec := int(elapsed.Seconds())
				if elapsedSec >= 60 {
					s.ChannelMessageEdit(m.ChannelID, msg.ID, fmt.Sprintf("⏳ Still working on @%s... This is taking a while due to a large follower count. Please be patient (%ds elapsed).", handle, elapsedSec))
				} else {
					s.ChannelMessageEdit(m.ChannelID, msg.ID, fmt.Sprintf("🔍 Analyzing @%s... (%ds elapsed)", handle, elapsedSec))
				}
			case <-done:
				return
			}
		}
	}()

	// Step 1: Get user info
	user, err := b.twitter.GetUser(handle)
	if err != nil {
		close(done)
		s.ChannelMessageEdit(m.ChannelID, msg.ID, fmt.Sprintf("❌ Failed to find @%s: %v", handle, err))
		b.setCooldown(m.Author.ID)
		return
	}

	// Step 2: Deep crawl followers
	followers, err := b.twitter.GetFollowers(user.ID, b.config.DeepMaxPages, b.config.DeepDelayMs)
	close(done)

	if err != nil {
		s.ChannelMessageEdit(m.ChannelID, msg.ID, fmt.Sprintf("❌ Failed to fetch followers for @%s: %v", handle, err))
		b.setCooldown(m.Author.ID)
		return
	}

	elapsed := time.Since(startTime)

	// Deduplicate followers
	seen := make(map[string]bool)
	var unique []XUser
	for _, f := range followers {
		if !seen[f.ID] {
			seen[f.ID] = true
			unique = append(unique, f)
		}
	}

	// Get the LAST 20 (oldest followers, since X returns newest first)
	var top20 []XUser
	if len(unique) > 20 {
		top20 = unique[len(unique)-20:]
	} else {
		top20 = unique
	}

	// Reverse so oldest is first
	for i, j := 0, len(top20)-1; i < j; i, j = i+1, j-1 {
		top20[i], top20[j] = top20[j], top20[i]
	}

	embed := b.buildFollowersEmbed(handle, user.FollowersCount, top20, elapsed)
	s.ChannelMessageEditEmbed(m.ChannelID, msg.ID, embed)
	b.setCooldown(m.Author.ID)
}

// handleCekCommand handles the .cek command — parallel fetch from Frontrun + X GraphQL
func (b *Bot) handleCekCommand(s *discordgo.Session, m *discordgo.MessageCreate, handle string) {
	msg, err := s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("🔍 Checking @%s...", handle))
	if err != nil {
		return
	}

	// Parallel fetch
	type frResult struct {
		history []UsernameHistoryEntry
		err     error
	}
	type xResult struct {
		about *AboutProfile
		err   error
	}

	frCh := make(chan frResult, 1)
	xCh := make(chan xResult, 1)

	go func() {
		history, err := b.frontrun.GetUsernameHistory(handle)
		frCh <- frResult{history, err}
	}()

	go func() {
		about, err := b.twitter.GetAboutAccount(handle)
		xCh <- xResult{about, err}
	}()

	fr := <-frCh
	xr := <-xCh

	var aboutProfile *AboutProfile
	if xr.err == nil {
		aboutProfile = xr.about
	}

	embed := b.buildUsernameHistoryEmbed(m.Author.Username, fr.history, aboutProfile)
	s.ChannelMessageEditEmbed(m.ChannelID, msg.ID, embed)
}

// buildFollowersEmbed builds the gold embed for the .first command result
func (b *Bot) buildFollowersEmbed(handle string, followersCount int, top20 []XUser, elapsed time.Duration) *discordgo.MessageEmbed {
	description := fmt.Sprintf("**@%s** has **%d** followers\n\n**Oldest %d Followers** (crawled in %s):",
		handle, followersCount, len(top20), elapsed.Round(time.Second))

	var fields []*discordgo.MessageEmbedField
	for i, f := range top20 {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:  fmt.Sprintf("%d. @%s", i+1, f.ScreenName),
			Value: fmt.Sprintf("ID: `%s`", f.ID),
		})
	}

	footer := b.makeFooter()
	return &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("🏆 First Followers of @%s", handle),
		Description: description,
		Color:       ColorGold,
		Fields:      fields,
		Footer:      footer,
	}
}

// buildUsernameHistoryEmbed builds the blurple embed for the .cek command result
func (b *Bot) buildUsernameHistoryEmbed(requestedBy string, history []UsernameHistoryEntry, about *AboutProfile) *discordgo.MessageEmbed {
	var description string

	if about != nil {
		verifiedStr := "❌"
		if about.Verified {
			verifiedStr = "✅"
		}
		description = fmt.Sprintf("**Verified:** %s\n**Followers:** %d\n**Following:** %d\n**Created:** %s\n**Bio:** %s\n\n",
			verifiedStr, about.Followers, about.Following, about.CreatedAt, about.Description)
	}

	description += "**Username History:**\n"
	if len(history) == 0 {
		description += "No username changes found."
	} else {
		sort.Slice(history, func(i, j int) bool {
			return history[i].ChangedAt < history[j].ChangedAt
		})
		for _, h := range history {
			description += fmt.Sprintf("• **@%s** — %s\n", h.Username, h.ChangedAt)
		}
	}

	footer := b.makeFooter()
	return &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("📋 Username History: @%s", requestedBy),
		Description: description,
		Color:       ColorBlurple,
		Footer:      footer,
	}
}

func (b *Bot) makeFooter() *discordgo.MessageEmbedFooter {
	now := time.Now().In(b.timezone)
	return &discordgo.MessageEmbedFooter{
		Text: fmt.Sprintf("X-Tracker-Bot | %s", now.Format("02/01/2006, 15:04:05")),
	}
}

func normalizeHandle(input string) string {
	handle := strings.TrimSpace(input)
	handle = strings.TrimPrefix(handle, "@")
	handle = strings.TrimPrefix(handle, "https://x.com/")
	handle = strings.TrimPrefix(handle, "https://twitter.com/")
	handle = strings.TrimRight(handle, "/")
	// Extract just the handle from URLs with paths
	if parts := strings.Split(handle, "/"); len(parts) > 0 {
		handle = parts[len(parts)-1]
	}
	return strings.TrimSpace(handle)
}

func parseCommand(content, prefix string) (string, bool) {
	if strings.HasPrefix(content, prefix) {
		return strings.TrimSpace(content[len(prefix):]), true
	}
	return "", false
}

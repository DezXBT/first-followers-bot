package main

import (
	"fmt"
	"sort"
	"strconv"
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

	// For .first, extract limit first so we strip the number from args before normalizing the handle
	var handle string
	var limit int
	switch command {
	case "first":
		limit = b.parseLimit(args)
		args = b.stripLimit(args)
		handle = normalizeHandle(args)
	default:
		handle = normalizeHandle(args)
	}
	if handle == "" {
		s.ChannelMessageSend(m.ChannelID, "❌ Please provide a Twitter handle. Usage: `"+b.config.BotPrefix+" <handle> [limit]`")
		return
	}

	switch command {
	case "first":
		limit := limit // capture for goroutine
		go func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("[PANIC] handleFirstCommand: %v\n", r)
					s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("❌ Internal error for @%s. Please try again.", handle))
				}
			}()
			b.handleFirstCommand(s, m, handle, limit)
		}()
	case "cek":
		b.handleCekCommand(s, m, handle)
	}
}

// parseLimit extracts a trailing number from args (e.g. ".first handle 30" → 30)
func (b *Bot) parseLimit(args string) int {
	parts := strings.Fields(args)
	// Scan from end — first number found is the limit
	for i := len(parts) - 1; i >= 0; i-- {
		if n, err := strconv.Atoi(parts[i]); err == nil && n > 0 && n <= 100 {
			return n
		}
	}
	return b.config.FirstFollowersLimit
}

// stripLimit removes the trailing standalone number (limit) from args so normalizeHandle gets a clean handle.
// Only strips tokens that are purely numeric (won't strip numbers embedded in URLs).
func (b *Bot) stripLimit(args string) string {
	parts := strings.Fields(args)
	for i := len(parts) - 1; i >= 0; i-- {
		if _, err := strconv.Atoi(parts[i]); err == nil && !strings.Contains(parts[i], "/") && !strings.Contains(parts[i], ":") {
			parts = append(parts[:i], parts[i+1:]...)
			return strings.TrimSpace(strings.Join(parts, " "))
		}
	}
	return args
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

// handleFirstCommand handles the .first command — deep crawl followers, find earliest N
func (b *Bot) handleFirstCommand(s *discordgo.Session, m *discordgo.MessageCreate, handle string, limit int) {
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

	// Step 2: Deep crawl ALL followers (X returns newest first, so last page = first followers)
	// Auto-calculate pages from follower count: ceil(followers_count / 50)
	totalPages := (user.FollowersCount + 49) / 50
	if totalPages < 1 {
		totalPages = 1
	}
	followers, err := b.twitter.GetFollowers(user.ID, totalPages, b.config.DeepDelayMs)
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

	// Get the LAST N (oldest followers, since X returns newest first)
	var topN []XUser
	if len(unique) > limit {
		topN = unique[len(unique)-limit:]
	} else {
		topN = unique
	}

	// Reverse so oldest is first
	for i, j := 0, len(topN)-1; i < j; i, j = i+1, j-1 {
		topN[i], topN[j] = topN[j], topN[i]
	}

	requester := m.Author.Username
	if m.Member != nil && m.Member.Nick != "" {
		requester = m.Member.Nick
	}
	embed := b.buildFollowersEmbed(handle, user.Name, user.FollowersCount, user.ProfileImageURL, topN, elapsed, requester, m.Author.AvatarURL("400x400"))
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
	type frHistoryResult struct {
		history []UsernameHistoryEntry
		err     error
	}
	type frBioResult struct {
		bioHistory []BioHistoryEntry
		err        error
	}
	type frSmartResult struct {
		smartFollowers []SmartFollower
		err            error
	}
	type frInfoResult struct {
		followers int
		err       error
	}
	type xResult struct {
		about *AboutProfile
		err   error
	}

	frHistoryCh := make(chan frHistoryResult, 1)
	frBioCh := make(chan frBioResult, 1)
	frSmartCh := make(chan frSmartResult, 1)
	frInfoCh := make(chan frInfoResult, 1)
	xCh := make(chan xResult, 1)

	go func() {
		history, err := b.frontrun.GetUsernameHistory(handle)
		frHistoryCh <- frHistoryResult{history, err}
	}()
	go func() {
		bioHistory, err := b.frontrun.GetBioHistory(handle)
		frBioCh <- frBioResult{bioHistory, err}
	}()
	go func() {
		smartFollowers, err := b.frontrun.GetSmartFollowers(handle)
		frSmartCh <- frSmartResult{smartFollowers, err}
	}()
	go func() {
		info, err := b.frontrun.GetUserInfo(handle)
		followers := 0
		if err == nil {
			if data, ok := info["data"].(map[string]interface{}); ok {
				if f, ok := data["followersCount"].(float64); ok {
					followers = int(f)
				}
			}
		}
		frInfoCh <- frInfoResult{followers, err}
	}()
	go func() {
		about, err := b.twitter.GetAboutAccount(handle)
		xCh <- xResult{about, err}
	}()

	frHistory := <-frHistoryCh
	frBio := <-frBioCh
	frSmart := <-frSmartCh
	frInfo := <-frInfoCh
	xr := <-xCh

	var aboutProfile *AboutProfile
	if xr.err == nil {
		aboutProfile = xr.about
		aboutProfile.Followers = frInfo.followers
	}

	fmt.Printf("[cek] history err=%v bio err=%v smart err=%v (smart count=%d) info err=%v (followers=%d) x err=%v\n",
		frHistory.err, frBio.err, frSmart.err, len(frSmart.smartFollowers), frInfo.err, frInfo.followers, xr.err)

	// Get requester for footer
	requester := m.Author.Username
	if m.Member != nil && m.Member.Nick != "" {
		requester = m.Member.Nick
	}
	embed := b.buildUsernameHistoryEmbed(requester, handle, frHistory.history, frBio.bioHistory, frSmart.smartFollowers, aboutProfile, m.Author.AvatarURL("400x400"))
	if _, err := s.ChannelMessageEditEmbed(m.ChannelID, msg.ID, embed); err != nil {
		fmt.Printf("[cek] ERROR edit embed: %v\n", err)
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("❌ Error: %v", err))
	}
}

// buildFollowersEmbed builds the gold embed for the .first command result
func (b *Bot) buildFollowersEmbed(handle string, name string, followersCount int, profileImageURL string, topN []XUser, elapsed time.Duration, requestedBy string, requesterAvatarURL string) *discordgo.MessageEmbed {
	description := fmt.Sprintf("%s ([@%s](https://x.com/%s)) — %s followers\n\nOldest %d followers:",
		name, handle, handle, formatNumber(followersCount), len(topN))

	var fields []*discordgo.MessageEmbedField
	for i, f := range topN {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("%d.", i+1),
			Value:  fmt.Sprintf("%s ([@%s](https://x.com/%s)) — %s followers",
				f.Name, f.ScreenName, f.ScreenName, formatNumber(f.FollowersCount)),
		})
	}

	footer := b.makeFooter(requestedBy, requesterAvatarURL)
	embed := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("🏆 %s", name),
		Description: description,
		Color:       ColorGold,
		Fields:      fields,
		Footer:      footer,
	}

	if profileImageURL != "" {
		// Replace _normal with _400x400 for better embed display
		hires := strings.Replace(profileImageURL, "_normal", "_400x400", 1)
		fmt.Printf("[embed] profile image: %s -> %s\n", profileImageURL, hires)
		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: hires}
	} else {
		fmt.Printf("[embed] profile image URL is EMPTY for @%s\n", handle)
	}

	return embed
}

// buildUsernameHistoryEmbed builds the embed for the .cek command result
func (b *Bot) buildUsernameHistoryEmbed(requestedBy string, handle string, history []UsernameHistoryEntry, bioHistory []BioHistoryEntry, smartFollowers []SmartFollower, about *AboutProfile, requesterAvatarURL string) *discordgo.MessageEmbed {
	var description string
	fmtNum := func(n int) string {
		if n == 0 {
			return "0"
		}
		s := fmt.Sprintf("%d", n)
		var result []byte
		for i, c := range s {
			if i > 0 && (len(s)-i)%3 == 0 {
				result = append(result, ',')
			}
			result = append(result, byte(c))
		}
		return string(result)
	}

	// Try to parse date strings into Discord timestamps
	toDiscordTS := func(dateStr string) string {
		// Try various date formats
		formats := []string{
			time.RubyDate, // "Sat May 01 12:55:14 +0000 2021"
			"January 2, 2006 3:04 PM",
			"January 2, 2006",
			"2006-01-02T15:04:05Z",
			"2006-01-02 15:04:05",
		}
		for _, f := range formats {
			t, err := time.Parse(f, dateStr)
			if err == nil {
				return fmt.Sprintf("<t:%d:f>", t.Unix())
			}
		}
		return dateStr // fallback
	}
	toDiscordDate := func(dateStr string) string {
		formats := []string{
			time.RubyDate,
			"January 2, 2006 3:04 PM",
			"January 2, 2006",
			"2006-01-02T15:04:05Z",
			"2006-01-02 15:04:05",
		}
		for _, f := range formats {
			t, err := time.Parse(f, dateStr)
			if err == nil {
				return fmt.Sprintf("<t:%d:D>", t.Unix())
			}
		}
		return dateStr
	}

	// Profile stats section
	description += fmt.Sprintf("**[@%s](https://x.com/%s)**\n\n", handle, handle)

	if about != nil {
		description += fmt.Sprintf("📊 Followers: **%s**\n", fmtNum(about.Followers))
		description += fmt.Sprintf("🧠 Smart Followers: **%s**\n", fmtNum(len(smartFollowers)))
		if about.AccountBasedIn != "" {
			description += fmt.Sprintf("🌍 Account Based In: **%s**\n", about.AccountBasedIn)
		}
		if about.Source != "" {
			description += fmt.Sprintf("📱 Source: **%s**\n", about.Source)
		}
		if about.CreatedAt != "" {
			description += fmt.Sprintf("📅 Created: %s\n", toDiscordDate(about.CreatedAt))
		}
		description += fmt.Sprintf("🔄 Username Changes: **%d**\n", about.UsernameChanges)
		description += fmt.Sprintf("📝 Bio Changes: **%d**\n\n", len(bioHistory))
	}

	// Username History
	description += "**📋 Username History**\n"
	if len(history) == 0 {
		description += "No username changes found.\n"
	} else {
		sort.Slice(history, func(i, j int) bool {
			return history[i].ChangedAt < history[j].ChangedAt
		})
		for i, h := range history {
			ts := toDiscordTS(h.ChangedAt)
			description += fmt.Sprintf("%d. **@%s** — %s\n", i+1, h.OldUsername, ts)
		}
	}

	// Bio History (last 5)
	if len(bioHistory) > 0 {
		description += "\n**📝 Bio History (latest 5)**\n"
		sort.Slice(bioHistory, func(i, j int) bool {
			return bioHistory[i].LastChecked > bioHistory[j].LastChecked
		})
		maxBio := 5
		if len(bioHistory) < maxBio {
			maxBio = len(bioHistory)
		}
		for i, bh := range bioHistory[:maxBio] {
			bio := bh.Bio
			if len(bio) > 120 {
				bio = bio[:117] + "..."
			}
			if bio == "" {
				bio = "*empty*"
			}
			ts := toDiscordTS(bh.LastChecked)
			description += fmt.Sprintf("%d. %s — %s\n", i+1, bio, ts)
		}
	}

	// Top Smart Followers (by SF count)
	if len(smartFollowers) > 0 {
		description += "\n**🏆 Top Smart Followers (by SF)**\n"
		sort.Slice(smartFollowers, func(i, j int) bool {
			return smartFollowers[i].SmartFollowersCount > smartFollowers[j].SmartFollowersCount
		})
		maxSF := 5
		if len(smartFollowers) < maxSF {
			maxSF = len(smartFollowers)
		}
		for i, sf := range smartFollowers[:maxSF] {
			description += fmt.Sprintf("%d. **@%s** | %s SF | %s followers\n",
				i+1, sf.Twitter, fmtNum(sf.SmartFollowersCount), fmtNum(sf.FollowersCount))
		}
	}

	title := fmt.Sprintf("📋 Username Check: @%s", handle)
	if about != nil && about.Name != "" {
		title = fmt.Sprintf("📋 Username Check: %s", about.Name)
	}
	footer := b.makeFooter(requestedBy, requesterAvatarURL)
	embed := &discordgo.MessageEmbed{
		Title:       title,
		Description: description,
		Color:       ColorGold,
		Footer:      footer,
	}
	if about != nil && about.AvatarURL != "" {
		hires := strings.Replace(about.AvatarURL, "_normal", "_400x400", 1)
		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: hires}
	}
	return embed
}

func (b *Bot) makeFooter(requestedBy string, avatarURL string) *discordgo.MessageEmbedFooter {
	now := time.Now().In(b.timezone)
	text := fmt.Sprintf("Requested by %s • %dm cooldown | Today at %s", requestedBy, b.config.FirstCooldownMs/60000, now.Format("15:04"))
	if requestedBy == "" {
		text = now.Format("02/01/2006, 15:04")
	}
	footer := &discordgo.MessageEmbedFooter{
		Text: text,
	}
	if avatarURL != "" {
		footer.IconURL = avatarURL
	}
	return footer
}

func normalizeHandle(input string) string {
	handle := strings.TrimSpace(input)
	handle = strings.TrimPrefix(handle, "@")
	handle = strings.TrimPrefix(handle, "https://x.com/")
	handle = strings.TrimPrefix(handle, "https://twitter.com/")
	// Strip query params first (?s=21, ?ref=xxx, etc)
	if idx := strings.Index(handle, "?"); idx != -1 {
		handle = handle[:idx]
	}
	handle = strings.TrimRight(handle, "/")
	// Extract just the handle from URLs with paths (e.g. /status/123)
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

func formatNumber(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

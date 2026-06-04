package main

import (
	"fmt"
	"regexp"
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

	// maxFirstLimit caps how many followers ".first" can request.
	maxFirstLimit = 100
	// discordDescBudget is a safe ceiling for embed description length (hard limit is 4096).
	discordDescBudget = 3900
)

type Bot struct {
	session    *discordgo.Session
	config     *Config
	twitter    *TwitterClient
	frontrun   *FrontrunClient
	cookiePool *CookiePool
	cooldowns  map[string]time.Time
	cooldownMu sync.Mutex
	timezone   *time.Location
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

	// Check for command prefix (require a word boundary so ".firstxyz" doesn't match ".first")
	var command string
	var args string
	if rest, ok := matchPrefix(content, b.config.BotPrefix); ok {
		command = "first"
		args = rest
	} else if rest, ok := matchPrefix(content, b.config.CheckPrefix); ok {
		command = "cek"
		args = rest
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

	// For .first, extract the optional limit and strip it from args before normalizing the handle
	var handle string
	var limit int
	switch command {
	case "first":
		var cleanArgs string
		cleanArgs, limit = b.parseFirstArgs(args)
		handle = normalizeHandle(cleanArgs)
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

// parseFirstArgs splits ".first" arguments into a clean handle source and a follower limit.
// A token is treated as the limit only when it is purely numeric AND there is at least one
// other (non-numeric) token to serve as the handle — so a numeric-only handle like
// ".first 12345" is kept as the handle instead of being eaten as a limit.
// The chosen limit is clamped to [1, maxFirstLimit]; out-of-range numbers fall back to the default.
func (b *Bot) parseFirstArgs(args string) (handle string, limit int) {
	parts := strings.Fields(args)
	limit = b.config.FirstFollowersLimit

	// Only look for a limit when there's more than one token (a handle plus a number).
	if len(parts) > 1 {
		for i := len(parts) - 1; i >= 0; i-- {
			n, err := strconv.Atoi(parts[i])
			if err != nil {
				continue // not a pure integer (URLs, handles, etc.)
			}
			if n >= 1 && n <= maxFirstLimit {
				limit = n
			}
			// Strip the numeric token regardless of range so it never leaks into the handle.
			parts = append(parts[:i], parts[i+1:]...)
			break
		}
	}

	return strings.TrimSpace(strings.Join(parts, " ")), limit
}

// matchPrefix reports whether content invokes the given command prefix, requiring the prefix to
// be followed by whitespace or end-of-string (so ".firstxyz" does not trigger ".first").
// It returns the trimmed argument string that follows the prefix.
func matchPrefix(content, prefix string) (string, bool) {
	if content == prefix {
		return "", true
	}
	if strings.HasPrefix(content, prefix) {
		rest := content[len(prefix):]
		if r := []rune(rest)[0]; r == ' ' || r == '\t' || r == '\n' {
			return strings.TrimSpace(rest), true
		}
	}
	return "", false
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
	// Cooldown check — set immediately on entry so a slow crawl can't be spammed into
	// many concurrent expensive crawls before it finishes.
	if onCooldown, remaining := b.checkCooldown(m.Author.ID); onCooldown {
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("⏳ Cooldown active. Please wait %s.", remaining.Round(time.Second)))
		return
	}
	b.setCooldown(m.Author.ID)

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
		return
	}

	// Step 2: Deep crawl followers (X returns newest first, so the last reachable page = first followers).
	// Auto-calculate pages from follower count: ceil(followers_count / 50), capped by DeepMaxPages so
	// huge accounts don't trigger an effectively endless, rate-limited crawl.
	totalPages := (user.FollowersCount + 49) / 50
	if totalPages < 1 {
		totalPages = 1
	}
	if b.config.DeepMaxPages > 0 && totalPages > b.config.DeepMaxPages {
		totalPages = b.config.DeepMaxPages
	}
	followers, err := b.twitter.GetFollowers(user.ID, totalPages, b.config.DeepDelayMs)
	close(done)

	if err != nil {
		s.ChannelMessageEdit(m.ChannelID, msg.ID, fmt.Sprintf("❌ Failed to fetch followers for @%s: %v", handle, err))
		return
	}

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
	embeds := b.buildFollowersEmbeds(handle, user.Name, user.FollowersCount, user.ProfileImageURL, topN, requester, m.Author.AvatarURL("400x400"))
	// Edit the original message with the first page; send any remaining pages as follow-up
	// messages (a single message caps all embeds at 6000 chars, so long lists need separate messages).
	if _, err := s.ChannelMessageEditEmbed(m.ChannelID, msg.ID, embeds[0]); err != nil {
		fmt.Printf("[first] ERROR edit embed for @%s: %v\n", handle, err)
		s.ChannelMessageEdit(m.ChannelID, msg.ID, fmt.Sprintf("❌ Could not render result for @%s: %v", handle, err))
		return
	}
	for _, e := range embeds[1:] {
		if _, err := s.ChannelMessageSendEmbed(m.ChannelID, e); err != nil {
			fmt.Printf("[first] ERROR send continuation embed for @%s: %v\n", handle, err)
		}
	}
}

// handleCekCommand handles the .cek command — parallel fetch from Frontrun + X GraphQL
func (b *Bot) handleCekCommand(s *discordgo.Session, m *discordgo.MessageCreate, handle string) {
	// discordgo dispatches handlers on their own goroutine; an unrecovered panic here would take
	// down the whole bot, so guard it the same way handleFirstCommand does.
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("[PANIC] handleCekCommand: %v\n", r)
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("❌ Internal error for @%s. Please try again.", handle))
		}
	}()

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

// buildFollowersEmbed builds the gold embed for the .first command result.
// The follower list is rendered as a numbered, clickable list inside the embed description rather
// than as embed fields: Discord caps embeds at 25 fields, so any N > 25 would be rejected with
// HTTP 400. Because each embed description is also capped at 4096 chars, the list is paginated
// across multiple embeds (sent as separate messages) so large limits like ".first 100" show fully.
func (b *Bot) buildFollowersEmbeds(handle string, name string, followersCount int, profileImageURL string, topN []XUser, requestedBy string, requesterAvatarURL string) []*discordgo.MessageEmbed {
	header := fmt.Sprintf("%s ([@%s](https://x.com/%s)) — %s followers\n\nFirst %d followers:\n",
		name, handle, handle, formatNumber(followersCount), len(topN))

	// Split entries into chunks that each stay within the per-embed description budget.
	chunks := []*strings.Builder{{}}
	chunks[0].WriteString(header)
	for i, f := range topN {
		line := fmt.Sprintf("%d. %s ([@%s](https://x.com/%s)) — %s followers\n",
			i+1, f.Name, f.ScreenName, f.ScreenName, formatNumber(f.FollowersCount))
		cur := chunks[len(chunks)-1]
		if cur.Len()+len(line) > discordDescBudget {
			cur = &strings.Builder{}
			chunks = append(chunks, cur)
		}
		cur.WriteString(line)
	}

	footer := b.makeFooter(requestedBy, requesterAvatarURL, true)
	total := len(chunks)
	embeds := make([]*discordgo.MessageEmbed, 0, total)
	for idx, c := range chunks {
		title := fmt.Sprintf("🏆 %s", name)
		if total > 1 {
			title = fmt.Sprintf("🏆 %s (%d/%d)", name, idx+1, total)
		}
		e := &discordgo.MessageEmbed{
			Title:       title,
			Description: c.String(),
			Color:       ColorGold,
		}
		// Footer (requester + cooldown) only on the last page; thumbnail only on the first.
		if idx == total-1 {
			e.Footer = footer
		}
		if idx == 0 && profileImageURL != "" {
			hires := strings.Replace(profileImageURL, "_normal", "_400x400", 1)
			e.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: hires}
		}
		embeds = append(embeds, e)
	}
	return embeds
}

// dateFormats lists the timestamp layouts the Frontrun/X APIs return, tried in order.
// RFC3339Nano comes first because bio timestamps look like "2026-03-19T17:02:09.904115+00:00"
// (fractional seconds + numeric offset) — Go's parser also handles the no-fractional variant.
var dateFormats = []string{
	time.RFC3339Nano,
	time.RFC3339,
	time.RubyDate, // "Sat May 01 12:55:14 +0000 2021"
	"January 2, 2006 3:04 PM",
	"January 2, 2006",
	"2006-01-02T15:04:05Z",
	"2006-01-02 15:04:05",
}

// parseFlexTime tries each known layout and returns the first successful parse.
func parseFlexTime(s string) (time.Time, bool) {
	for _, f := range dateFormats {
		if t, err := time.Parse(f, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
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

	// Parse flexible date strings into Discord timestamps (auto-localized per viewer).
	toDiscordTS := func(dateStr string) string {
		if t, ok := parseFlexTime(dateStr); ok {
			return fmt.Sprintf("<t:%d:f>", t.Unix())
		}
		return dateStr // fallback: show raw
	}
	toDiscordDate := func(dateStr string) string {
		if t, ok := parseFlexTime(dateStr); ok {
			return fmt.Sprintf("<t:%d:D>", t.Unix())
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
			// Rune-safe truncation so emojis/multibyte chars in bios aren't corrupted.
			if r := []rune(bio); len(r) > 120 {
				bio = string(r[:117]) + "..."
			}
			if bio == "" {
				bio = "*empty*"
			} else {
				bio = linkifyMentions(bio) // make @accounts mentioned in the bio clickable
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
			description += fmt.Sprintf("%d. **[@%s](https://x.com/%s)** | %s SF | %s followers\n",
				i+1, sf.Twitter, sf.Twitter, fmtNum(sf.SmartFollowersCount), fmtNum(sf.FollowersCount))
		}
	}

	title := fmt.Sprintf("📋 Username Check: @%s", handle)
	if about != nil && about.Name != "" {
		title = fmt.Sprintf("📋 Username Check: %s", about.Name)
	}
	footer := b.makeFooter(requestedBy, requesterAvatarURL, false)
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

// makeFooter builds the embed footer. withCooldown controls whether the cooldown notice is shown —
// only the .first command has a cooldown, so .cek passes false.
func (b *Bot) makeFooter(requestedBy string, avatarURL string, withCooldown bool) *discordgo.MessageEmbedFooter {
	now := time.Now().In(b.timezone)
	var text string
	switch {
	case requestedBy == "":
		text = now.Format("02/01/2006, 15:04")
	case withCooldown:
		text = fmt.Sprintf("Requested by %s • %dm cooldown | Today at %s", requestedBy, b.config.FirstCooldownMs/60000, now.Format("15:04"))
	default:
		text = fmt.Sprintf("Requested by %s | Today at %s", requestedBy, now.Format("15:04"))
	}
	footer := &discordgo.MessageEmbedFooter{
		Text: text,
	}
	if avatarURL != "" {
		footer.IconURL = avatarURL
	}
	return footer
}

// linkifyMentions converts @handle mentions in free text into clickable x.com markdown links.
// It only matches an @ at the start of the string or preceded by a non-handle character, so
// things like email addresses aren't turned into links.
var mentionRe = regexp.MustCompile(`(^|[^0-9A-Za-z_])@(\w{1,15})`)

func linkifyMentions(s string) string {
	return mentionRe.ReplaceAllString(s, `${1}[@${2}](https://x.com/${2})`)
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

package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	fmt.Println("[main] Loading configuration...")
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[main] Config error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("[main] Config loaded: %d cookie(s), timezone=%s\n", len(cfg.AuthTokens), cfg.Timezone)

	fmt.Println("[main] Initializing X-Client-Transaction-Id generator...")
	if err := Init(); err != nil {
		fmt.Fprintf(os.Stderr, "[main] Warning: Transaction ID generator init failed: %v\n", err)
		fmt.Println("[main] Continuing without transaction IDs (requests may be rate-limited)")
	}

	fmt.Println("[main] Creating Discord bot...")
	bot, err := NewBot(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[main] Bot creation error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("[main] Starting Discord connection...")
	if err := bot.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "[main] Failed to start bot: %v\n", err)
		os.Exit(1)
	}
	defer bot.Stop()

	fmt.Println("[main] Bot is running! Press Ctrl+C to stop.")

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM)
	<-sc

	fmt.Println("[main] Shutting down...")
}

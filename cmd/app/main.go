package main

import (
	"log"
	"strings"

	"p2p/internal/config"
	"p2p/internal/peer"
	"p2p/internal/tracker"
)

func main() {
	cfg := config.Load()

	mode := strings.ToLower(cfg.AppMode)
	switch mode {
	case "tracker":
		if err := tracker.Run(cfg); err != nil {
			log.Fatalf("tracker failed: %v", err)
		}
	case "peer":
		if err := peer.Run(cfg); err != nil {
			log.Fatalf("peer failed: %v", err)
		}
	default:
		log.Fatalf("unknown APP_MODE=%s, expected tracker or peer", cfg.AppMode)
	}
}

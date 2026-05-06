package tracker

import (
	"log"
	"time"

	"github.com/gofiber/fiber/v3"

	"p2p/internal/config"
	"p2p/internal/models"
)

func Run(cfg config.Config) error {
	store := NewStore()
	app := fiber.New()

	app.Get("/healthz", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok", "mode": "tracker"})
	})

	api := app.Group("/api/v1")
	api.Use(authMiddleware(cfg.TrackerToken))

	api.Post("/peers/register", func(c fiber.Ctx) error {
		var req models.PeerRegisterRequest
		if err := c.Bind().Body(&req); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		if req.PeerID == "" || req.PeerBaseURL == "" {
			return fiber.NewError(fiber.StatusBadRequest, "peer_id and peer_base_url are required")
		}
		store.RegisterPeer(req.PeerID, req.PeerBaseURL)
		return c.JSON(fiber.Map{"status": "registered"})
	})

	api.Post("/peers/heartbeat", func(c fiber.Ctx) error {
		var req models.PeerRegisterRequest
		if err := c.Bind().Body(&req); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		if req.PeerID == "" || req.PeerBaseURL == "" {
			return fiber.NewError(fiber.StatusBadRequest, "peer_id and peer_base_url are required")
		}
		store.RegisterPeer(req.PeerID, req.PeerBaseURL)
		return c.JSON(fiber.Map{"status": "alive"})
	})

	api.Post("/files/announce", func(c fiber.Ctx) error {
		var req models.AnnounceRequest
		if err := c.Bind().Body(&req); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		if req.PeerID == "" || req.PeerBaseURL == "" || req.File.ID == "" {
			return fiber.NewError(fiber.StatusBadRequest, "peer and file metadata are required")
		}
		store.Announce(req)
		return c.JSON(fiber.Map{"status": "announced"})
	})

	api.Get("/files", func(c fiber.Ctx) error {
		return c.JSON(store.ListFiles())
	})

	api.Get("/files/:id", func(c fiber.Ctx) error {
		fileID := c.Params("id")
		details, ok := store.GetFileDetails(fileID)
		if !ok {
			return fiber.NewError(fiber.StatusNotFound, "file not found")
		}
		return c.JSON(details)
	})

	go func() {
		ticker := time.NewTicker(cfg.TrackerCleanupTick)
		defer ticker.Stop()
		for range ticker.C {
			store.CleanupStalePeers(cfg.TrackerCleanupTTL)
		}
	}()

	log.Printf("tracker listening on :%s", cfg.Port)
	return app.Listen(":" + cfg.Port)
}

func authMiddleware(token string) fiber.Handler {
	return func(c fiber.Ctx) error {
		header := c.Get("X-API-Token")
		if header != token {
			return fiber.NewError(fiber.StatusUnauthorized, "invalid token")
		}
		return c.Next()
	}
}

package peer

import (
	"context"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"p2p/internal/config"
	"p2p/internal/models"
)

func Run(cfg config.Config) error {
	storage := NewStorage(cfg)
	if err := storage.InitDirs(); err != nil {
		return err
	}

	trackerClient := NewTrackerClient(cfg)
	downloader := NewDownloader(cfg)

	if err := trackerClient.RegisterPeer(context.Background()); err != nil {
		log.Printf("tracker register warning: %v", err)
	}

	local, err := storage.ListLocalFiles()
	if err == nil {
		for _, meta := range local {
			if err := trackerClient.AnnounceFile(context.Background(), meta); err != nil {
				log.Printf("announce warning for %s: %v", meta.ID, err)
			}
		}
	}

	go heartbeatLoop(cfg, trackerClient)

	app := fiber.New()

	app.Get("/healthz", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok", "mode": "peer", "peer_id": cfg.PeerID})
	})

	app.Get("/", func(c fiber.Ctx) error {
		localFiles, _ := storage.ListLocalFiles()
		networkFiles, _ := trackerClient.ListFiles(c.Context())
		return c.Type("html").SendString(buildHTML(cfg.PeerID, localFiles, networkFiles))
	})

	app.Get("/api/v1/files/local", func(c fiber.Ctx) error {
		localFiles, err := storage.ListLocalFiles()
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		return c.JSON(localFiles)
	})

	app.Get("/api/v1/files/network", func(c fiber.Ctx) error {
		files, err := trackerClient.ListFiles(c.Context())
		if err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		return c.JSON(files)
	})

	app.Post("/api/v1/upload", func(c fiber.Ctx) error {
		fileHeader, err := c.FormFile("file")
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "expected multipart file field 'file'")
		}

		meta, err := storage.SaveUploadedFile(fileHeader)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		if err := trackerClient.AnnounceFile(c.Context(), meta); err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		return c.JSON(fiber.Map{"status": "uploaded", "file": meta})
	})

	app.Post("/api/v1/download/:id", func(c fiber.Ctx) error {
		fileID := c.Params("id")
		details, err := trackerClient.GetFileDetails(c.Context(), fileID)
		if err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		chunks, err := downloader.DownloadFile(c.Context(), details)
		if err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		meta, path, err := storage.SaveDownloadedFile(details.File, chunks)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		if err := trackerClient.AnnounceFile(c.Context(), meta); err != nil {
			log.Printf("announce after download warning: %v", err)
		}
		return c.JSON(fiber.Map{"status": "downloaded", "saved_to": path, "file": meta})
	})

	app.Get("/p2p/chunks/:fileID/:idx", func(c fiber.Ctx) error {
		fileID := c.Params("fileID")
		chunkIdx, err := strconv.Atoi(c.Params("idx"))
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid chunk index")
		}
		data, err := storage.ReadChunk(fileID, chunkIdx)
		if err != nil {
			return fiber.NewError(fiber.StatusNotFound, "chunk not found")
		}
		return c.Type("application/octet-stream").Send(data)
	})

	log.Printf("peer %s listening on :%s", cfg.PeerID, cfg.Port)
	return app.Listen(":" + cfg.Port)
}

func heartbeatLoop(cfg config.Config, trackerClient *TrackerClient) {
	ticker := time.NewTicker(cfg.HeartbeatPeriod)
	defer ticker.Stop()
	for range ticker.C {
		if err := trackerClient.Heartbeat(context.Background()); err != nil {
			log.Printf("heartbeat failed: %v", err)
		}
	}
}

func buildHTML(peerID string, localFiles []models.FileMetadata, networkFiles []models.FileSummary) string {
	type localView struct {
		ID         string
		Name       string
		SizeBytes  int64
		ChunkCount int
	}
	type networkView struct {
		ID         string
		Name       string
		SizeBytes  int64
		ChunkCount int
		PeersCount int
	}

	local := make([]localView, 0, len(localFiles))
	for _, f := range localFiles {
		local = append(local, localView{
			ID:         f.ID,
			Name:       f.Name,
			SizeBytes:  f.SizeBytes,
			ChunkCount: f.ChunkCount,
		})
	}

	network := make([]networkView, 0, len(networkFiles))
	for _, f := range networkFiles {
		network = append(network, networkView{
			ID:         f.ID,
			Name:       f.Name,
			SizeBytes:  f.SizeBytes,
			ChunkCount: f.ChunkCount,
			PeersCount: f.PeersCount,
		})
	}

	sort.Slice(local, func(i, j int) bool { return local[i].Name < local[j].Name })
	sort.Slice(network, func(i, j int) bool { return network[i].Name < network[j].Name })

	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"UTF-8\"><title>P2P Peer</title>")
	b.WriteString("<style>body{font-family:Arial,sans-serif;max-width:1000px;margin:20px auto;padding:0 16px;}table{width:100%;border-collapse:collapse;margin:12px 0;}th,td{border:1px solid #ddd;padding:8px;text-align:left;}th{background:#f3f3f3;}code{font-size:12px;}form{margin:0;}button{padding:6px 10px;}</style>")
	b.WriteString("</head><body>")
	b.WriteString("<h1>P2P File Share Peer</h1>")
	b.WriteString("<p>Peer ID: <b>" + peerID + "</b></p>")

	b.WriteString("<h2>Upload file</h2>")
	b.WriteString("<form action=\"/api/v1/upload\" method=\"post\" enctype=\"multipart/form-data\">")
	b.WriteString("<input type=\"file\" name=\"file\" required>")
	b.WriteString("<button type=\"submit\">Upload</button>")
	b.WriteString("</form>")

	b.WriteString("<h2>Local files</h2><table><tr><th>Name</th><th>Size</th><th>Chunks</th><th>File ID</th></tr>")
	for _, f := range local {
		b.WriteString("<tr><td>" + f.Name + "</td><td>" + strconv.FormatInt(f.SizeBytes, 10) + "</td><td>" + strconv.Itoa(f.ChunkCount) + "</td><td><code>" + f.ID + "</code></td></tr>")
	}
	b.WriteString("</table>")

	b.WriteString("<h2>Network files</h2><table><tr><th>Name</th><th>Size</th><th>Chunks</th><th>Peers</th><th>File ID</th><th>Action</th></tr>")
	for _, f := range network {
		b.WriteString("<tr><td>" + f.Name + "</td><td>" + strconv.FormatInt(f.SizeBytes, 10) + "</td><td>" + strconv.Itoa(f.ChunkCount) + "</td><td>" + strconv.Itoa(f.PeersCount) + "</td><td><code>" + f.ID + "</code></td>")
		b.WriteString("<td><form action=\"/api/v1/download/" + f.ID + "\" method=\"post\"><button type=\"submit\">Download</button></form></td></tr>")
	}
	b.WriteString("</table>")

	b.WriteString("<p>Tip: after download, file is saved into <code>data/downloads</code> and announced to tracker.</p>")
	b.WriteString("</body></html>")
	return b.String()
}

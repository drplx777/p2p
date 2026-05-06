package config

import (
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	AppMode string
	Port    string

	TrackerToken       string
	TrackerURL         string
	TrackerCleanupTTL  time.Duration
	TrackerCleanupTick time.Duration

	PeerID          string
	PublicBaseURL   string
	DataDir         string
	MetaDir         string
	ChunksDir       string
	DownloadsDir    string
	ChunkSizeBytes  int
	HeartbeatPeriod time.Duration
	DownloadTimeout time.Duration

	DatabaseURL    string
	SessionTTL     time.Duration
}

func Load() Config {
	_ = godotenv.Load(".env", "../.env", "../../.env")

	dataDir := getEnv("DATA_DIR", "data")
	port := getEnv("PORT", "8080")
	peerID := getEnv("PEER_ID", "")
	if peerID == "" {
		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "peer"
		}
		peerID = hostname + "-" + port
	}
	return Config{
		AppMode: getEnv("APP_MODE", "peer"),
		Port:    port,

		TrackerToken:       getEnv("TRACKER_TOKEN", "change-me-token"),
		TrackerURL:         getEnv("TRACKER_URL", "http://localhost:7000"),
		TrackerCleanupTTL:  getDurationEnv("TRACKER_CLEANUP_TTL", 90*time.Second),
		TrackerCleanupTick: getDurationEnv("TRACKER_CLEANUP_TICK", 30*time.Second),

		PeerID:          peerID,
		PublicBaseURL:   getEnv("PUBLIC_BASE_URL", "http://localhost:8080"),
		DataDir:         dataDir,
		MetaDir:         getEnv("META_DIR", dataDir+"/meta"),
		ChunksDir:       getEnv("CHUNKS_DIR", dataDir+"/chunks"),
		DownloadsDir:    getEnv("DOWNLOADS_DIR", dataDir+"/downloads"),
		ChunkSizeBytes:  getIntEnv("CHUNK_SIZE_BYTES", 524288),
		HeartbeatPeriod: getDurationEnv("HEARTBEAT_PERIOD", 20*time.Second),
		DownloadTimeout: getDurationEnv("DOWNLOAD_TIMEOUT", 10*time.Second),
		DatabaseURL:     getEnv("DATABASE_URL", "postgres://p2p_user:p2p_password@localhost:5432/p2p_auth?sslmode=disable"),
		SessionTTL:      getDurationEnv("SESSION_TTL", 168*time.Hour),
	}
}

func getEnv(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}

func getIntEnv(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func getDurationEnv(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return d
}

package config

import (
	"os"
	"strconv"
	"time"
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
}

func Load() Config {
	dataDir := getEnv("DATA_DIR", "data")
	return Config{
		AppMode: getEnv("APP_MODE", "peer"),
		Port:    getEnv("PORT", "8080"),

		TrackerToken:       getEnv("TRACKER_TOKEN", "change-me-token"),
		TrackerURL:         getEnv("TRACKER_URL", "http://localhost:7000"),
		TrackerCleanupTTL:  getDurationEnv("TRACKER_CLEANUP_TTL", 90*time.Second),
		TrackerCleanupTick: getDurationEnv("TRACKER_CLEANUP_TICK", 30*time.Second),

		PeerID:          getEnv("PEER_ID", "peer-dev"),
		PublicBaseURL:   getEnv("PUBLIC_BASE_URL", "http://localhost:8080"),
		DataDir:         dataDir,
		MetaDir:         getEnv("META_DIR", dataDir+"/meta"),
		ChunksDir:       getEnv("CHUNKS_DIR", dataDir+"/chunks"),
		DownloadsDir:    getEnv("DOWNLOADS_DIR", dataDir+"/downloads"),
		ChunkSizeBytes:  getIntEnv("CHUNK_SIZE_BYTES", 524288),
		HeartbeatPeriod: getDurationEnv("HEARTBEAT_PERIOD", 20*time.Second),
		DownloadTimeout: getDurationEnv("DOWNLOAD_TIMEOUT", 10*time.Second),
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

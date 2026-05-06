package peer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"p2p/internal/config"
	"p2p/internal/models"
)

type TrackerClient struct {
	cfg    config.Config
	client *http.Client
}

func NewTrackerClient(cfg config.Config) *TrackerClient {
	return &TrackerClient{
		cfg: cfg,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (t *TrackerClient) RegisterPeer(ctx context.Context) error {
	req := models.PeerRegisterRequest{
		PeerID:      t.cfg.PeerID,
		PeerBaseURL: t.cfg.PublicBaseURL,
	}
	return t.postJSON(ctx, "/api/v1/peers/register", req)
}

func (t *TrackerClient) Heartbeat(ctx context.Context) error {
	req := models.PeerRegisterRequest{
		PeerID:      t.cfg.PeerID,
		PeerBaseURL: t.cfg.PublicBaseURL,
	}
	return t.postJSON(ctx, "/api/v1/peers/heartbeat", req)
}

func (t *TrackerClient) AnnounceFile(ctx context.Context, meta models.FileMetadata) error {
	chunks := make([]int, meta.ChunkCount)
	for i := 0; i < meta.ChunkCount; i++ {
		chunks[i] = i
	}
	req := models.AnnounceRequest{
		PeerID:          t.cfg.PeerID,
		PeerBaseURL:     t.cfg.PublicBaseURL,
		File:            meta,
		AvailableChunks: chunks,
	}
	return t.postJSON(ctx, "/api/v1/files/announce", req)
}

func (t *TrackerClient) ListFiles(ctx context.Context) ([]models.FileSummary, error) {
	url := strings.TrimRight(t.cfg.TrackerURL, "/") + "/api/v1/files"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Token", t.cfg.TrackerToken)

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tracker list files error: %s", string(b))
	}
	var out []models.FileSummary
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (t *TrackerClient) GetFileDetails(ctx context.Context, fileID string) (models.FileDetails, error) {
	url := strings.TrimRight(t.cfg.TrackerURL, "/") + "/api/v1/files/" + fileID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return models.FileDetails{}, err
	}
	req.Header.Set("X-API-Token", t.cfg.TrackerToken)

	resp, err := t.client.Do(req)
	if err != nil {
		return models.FileDetails{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return models.FileDetails{}, fmt.Errorf("tracker details error: %s", string(b))
	}
	var out models.FileDetails
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return models.FileDetails{}, err
	}
	return out, nil
}

func (t *TrackerClient) postJSON(ctx context.Context, path string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	url := strings.TrimRight(t.cfg.TrackerURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Token", t.cfg.TrackerToken)

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("tracker error: %s", string(body))
	}
	return nil
}

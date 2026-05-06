package peer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"p2p/internal/config"
	"p2p/internal/models"
)

type Downloader struct {
	cfg    config.Config
	client *http.Client
}

func NewDownloader(cfg config.Config) *Downloader {
	return &Downloader{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.DownloadTimeout,
		},
	}
}

func (d *Downloader) DownloadFile(ctx context.Context, details models.FileDetails) ([][]byte, error) {
	chunks := make([][]byte, details.File.ChunkCount)
	for chunkIdx := 0; chunkIdx < details.File.ChunkCount; chunkIdx++ {
		peers := details.ChunkToPeers[chunkIdx]
		if len(peers) == 0 {
			return nil, fmt.Errorf("chunk %d has no available peers", chunkIdx)
		}

		var chunkData []byte
		var err error
		for _, peerURL := range peers {
			chunkData, err = d.fetchChunk(ctx, peerURL, details.File.ID, chunkIdx)
			if err == nil {
				break
			}
		}
		if err != nil {
			return nil, fmt.Errorf("chunk %d download failed: %w", chunkIdx, err)
		}

		sum := sha256.Sum256(chunkData)
		gotHash := hex.EncodeToString(sum[:])
		wantHash := details.File.ChunkHashes[chunkIdx]
		if gotHash != wantHash {
			return nil, fmt.Errorf("chunk %d hash mismatch", chunkIdx)
		}
		chunks[chunkIdx] = chunkData
	}
	return chunks, nil
}

func (d *Downloader) fetchChunk(ctx context.Context, baseURL, fileID string, chunkIdx int) ([]byte, error) {
	url := strings.TrimRight(baseURL, "/") + "/p2p/chunks/" + fileID + "/" + strconv.Itoa(chunkIdx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("peer status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("empty chunk")
	}
	time.Sleep(10 * time.Millisecond)
	return data, nil
}

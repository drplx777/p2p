package peer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"p2p/internal/config"
	"p2p/internal/models"
)

type Storage struct {
	cfg config.Config
}

func NewStorage(cfg config.Config) *Storage {
	return &Storage{cfg: cfg}
}

func (s *Storage) InitDirs() error {
	dirs := []string{s.cfg.DataDir, s.cfg.MetaDir, s.cfg.ChunksDir, s.cfg.DownloadsDir}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (s *Storage) SaveUploadedFile(fileHeader *multipart.FileHeader) (models.FileMetadata, error) {
	file, err := fileHeader.Open()
	if err != nil {
		return models.FileMetadata{}, err
	}
	defer file.Close()

	raw, err := io.ReadAll(file)
	if err != nil {
		return models.FileMetadata{}, err
	}
	return s.saveRawData(fileHeader.Filename, raw)
}

func (s *Storage) SaveDownloadedFile(meta models.FileMetadata, orderedChunks [][]byte) (models.FileMetadata, string, error) {
	total := 0
	for _, chunk := range orderedChunks {
		total += len(chunk)
	}
	raw := make([]byte, 0, total)
	for _, chunk := range orderedChunks {
		raw = append(raw, chunk...)
	}

	savedMeta, err := s.saveRawData(meta.Name, raw)
	if err != nil {
		return models.FileMetadata{}, "", err
	}
	downloadPath := filepath.Join(s.cfg.DownloadsDir, meta.Name)
	if err := os.WriteFile(downloadPath, raw, 0o644); err != nil {
		return models.FileMetadata{}, "", err
	}
	return savedMeta, downloadPath, nil
}

func (s *Storage) saveRawData(fileName string, raw []byte) (models.FileMetadata, error) {
	hash := sha256.Sum256(raw)
	fileID := hex.EncodeToString(hash[:])
	chunkCount := (len(raw) + s.cfg.ChunkSizeBytes - 1) / s.cfg.ChunkSizeBytes

	chunkHashes := make([]string, 0, chunkCount)
	chunkDir := filepath.Join(s.cfg.ChunksDir, fileID)
	if err := os.MkdirAll(chunkDir, 0o755); err != nil {
		return models.FileMetadata{}, err
	}

	for idx := 0; idx < chunkCount; idx++ {
		start := idx * s.cfg.ChunkSizeBytes
		end := start + s.cfg.ChunkSizeBytes
		if end > len(raw) {
			end = len(raw)
		}
		chunk := raw[start:end]
		sum := sha256.Sum256(chunk)
		chunkHashes = append(chunkHashes, hex.EncodeToString(sum[:]))
		chunkPath := filepath.Join(chunkDir, fmt.Sprintf("%d.chk", idx))
		if err := os.WriteFile(chunkPath, chunk, 0o644); err != nil {
			return models.FileMetadata{}, err
		}
	}

	meta := models.FileMetadata{
		ID:          fileID,
		Name:        fileName,
		SizeBytes:   int64(len(raw)),
		ChunkSize:   s.cfg.ChunkSizeBytes,
		ChunkCount:  chunkCount,
		ChunkHashes: chunkHashes,
	}
	if err := s.saveMeta(meta); err != nil {
		return models.FileMetadata{}, err
	}
	return meta, nil
}

func (s *Storage) saveMeta(meta models.FileMetadata) error {
	path := filepath.Join(s.cfg.MetaDir, meta.ID+".json")
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

func (s *Storage) ListLocalFiles() ([]models.FileMetadata, error) {
	items, err := os.ReadDir(s.cfg.MetaDir)
	if err != nil {
		return nil, err
	}
	out := make([]models.FileMetadata, 0, len(items))
	for _, item := range items {
		if item.IsDir() || filepath.Ext(item.Name()) != ".json" {
			continue
		}
		path := filepath.Join(s.cfg.MetaDir, item.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var meta models.FileMetadata
		if err := json.Unmarshal(raw, &meta); err != nil {
			continue
		}
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (s *Storage) ReadChunk(fileID string, index int) ([]byte, error) {
	path := filepath.Join(s.cfg.ChunksDir, fileID, strconv.Itoa(index)+".chk")
	return os.ReadFile(path)
}

package models

type Peer struct {
	ID        string `json:"id"`
	BaseURL   string `json:"base_url"`
	UpdatedAt int64  `json:"updated_at"`
}

type FileMetadata struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	SizeBytes   int64    `json:"size_bytes"`
	ChunkSize   int      `json:"chunk_size"`
	ChunkCount  int      `json:"chunk_count"`
	ChunkHashes []string `json:"chunk_hashes"`
}

type AnnounceRequest struct {
	PeerID          string       `json:"peer_id"`
	PeerBaseURL     string       `json:"peer_base_url"`
	File            FileMetadata `json:"file"`
	AvailableChunks []int        `json:"available_chunks"`
}

type PeerRegisterRequest struct {
	PeerID      string `json:"peer_id"`
	PeerBaseURL string `json:"peer_base_url"`
}

type FileSummary struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	SizeBytes  int64  `json:"size_bytes"`
	ChunkCount int    `json:"chunk_count"`
	PeersCount int    `json:"peers_count"`
}

type FileDetails struct {
	File         FileMetadata     `json:"file"`
	ChunkToPeers map[int][]string `json:"chunk_to_peers"`
}

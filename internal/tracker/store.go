package tracker

import (
	"sort"
	"sync"
	"time"

	"p2p/internal/models"
)

type fileEntry struct {
	file         models.FileMetadata
	chunkToPeer  map[int]map[string]struct{}
	peerToChunks map[string]map[int]struct{}
}

type Store struct {
	mu      sync.RWMutex
	peers   map[string]models.Peer
	files   map[string]*fileEntry
	peerRef map[string]map[string]struct{}
}

func NewStore() *Store {
	return &Store{
		peers:   make(map[string]models.Peer),
		files:   make(map[string]*fileEntry),
		peerRef: make(map[string]map[string]struct{}),
	}
}

func (s *Store) RegisterPeer(peerID, baseURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.peers[peerID] = models.Peer{
		ID:        peerID,
		BaseURL:   baseURL,
		UpdatedAt: time.Now().Unix(),
	}
}

func (s *Store) Announce(req models.AnnounceRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.peers[req.PeerID] = models.Peer{
		ID:        req.PeerID,
		BaseURL:   req.PeerBaseURL,
		UpdatedAt: time.Now().Unix(),
	}

	entry, ok := s.files[req.File.ID]
	if !ok {
		entry = &fileEntry{
			file:         req.File,
			chunkToPeer:  make(map[int]map[string]struct{}),
			peerToChunks: make(map[string]map[int]struct{}),
		}
		s.files[req.File.ID] = entry
	}

	entry.file = req.File
	entry.peerToChunks[req.PeerID] = make(map[int]struct{})

	for _, chunkIdx := range req.AvailableChunks {
		if _, ok := entry.chunkToPeer[chunkIdx]; !ok {
			entry.chunkToPeer[chunkIdx] = make(map[string]struct{})
		}
		entry.chunkToPeer[chunkIdx][req.PeerID] = struct{}{}
		entry.peerToChunks[req.PeerID][chunkIdx] = struct{}{}
	}

	if _, ok := s.peerRef[req.PeerID]; !ok {
		s.peerRef[req.PeerID] = make(map[string]struct{})
	}
	s.peerRef[req.PeerID][req.File.ID] = struct{}{}
}

func (s *Store) ListFiles() []models.FileSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]models.FileSummary, 0, len(s.files))
	for _, entry := range s.files {
		peerIDs := make(map[string]struct{})
		for _, peers := range entry.chunkToPeer {
			for peerID := range peers {
				peerIDs[peerID] = struct{}{}
			}
		}
		out = append(out, models.FileSummary{
			ID:         entry.file.ID,
			Name:       entry.file.Name,
			SizeBytes:  entry.file.SizeBytes,
			ChunkCount: entry.file.ChunkCount,
			PeersCount: len(peerIDs),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func (s *Store) GetFileDetails(fileID string) (models.FileDetails, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.files[fileID]
	if !ok {
		return models.FileDetails{}, false
	}

	details := models.FileDetails{
		File:         entry.file,
		ChunkToPeers: make(map[int][]string),
	}
	for chunkIdx, peersSet := range entry.chunkToPeer {
		peers := make([]string, 0, len(peersSet))
		for peerID := range peersSet {
			peer, found := s.peers[peerID]
			if !found {
				continue
			}
			peers = append(peers, peer.BaseURL)
		}
		sort.Strings(peers)
		details.ChunkToPeers[chunkIdx] = peers
	}
	return details, true
}

func (s *Store) CleanupStalePeers(ttl time.Duration) {
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	for peerID, peer := range s.peers {
		lastSeen := time.Unix(peer.UpdatedAt, 0)
		if now.Sub(lastSeen) < ttl {
			continue
		}
		delete(s.peers, peerID)

		fileIDs := s.peerRef[peerID]
		for fileID := range fileIDs {
			entry, ok := s.files[fileID]
			if !ok {
				continue
			}
			chunks := entry.peerToChunks[peerID]
			for chunkIdx := range chunks {
				delete(entry.chunkToPeer[chunkIdx], peerID)
				if len(entry.chunkToPeer[chunkIdx]) == 0 {
					delete(entry.chunkToPeer, chunkIdx)
				}
			}
			delete(entry.peerToChunks, peerID)

			if len(entry.peerToChunks) == 0 {
				delete(s.files, fileID)
			}
		}
		delete(s.peerRef, peerID)
	}
}

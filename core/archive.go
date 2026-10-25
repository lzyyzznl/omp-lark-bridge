package core

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ArchiveEntry records when and why a transcript was hidden from the session
// library. Archiving is a soft delete: the transcript file stays untouched.
type ArchiveEntry struct {
	ArchivedAt time.Time `json:"archived_at"`
	Mode       string    `json:"mode,omitempty"` // "manual" | "auto"
}

// ArchiveStore persists the set of archived transcript IDs.
//
// ponytail: a single JSON sidecar, not a directory move — the session dir
// belongs to the omp agent, and moving files there could break its own
// session resume. Losing this file only un-archives everything; no data loss.
type ArchiveStore struct {
	mu   sync.Mutex
	path string
	ids  map[string]ArchiveEntry
}

// NewArchiveStore loads (or initialises) the archive index at path.
func NewArchiveStore(path string) *ArchiveStore {
	s := &ArchiveStore{path: path, ids: map[string]ArchiveEntry{}}
	s.reload()
	return s
}

// reload treats a missing or corrupt index as empty: a broken sidecar must
// degrade to "nothing archived", never to an unusable UI.
func (s *ArchiveStore) reload() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("archive: read index failed, starting empty", "error", err)
		}
		return
	}
	var raw map[string]ArchiveEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		slog.Warn("archive: corrupt index, starting empty", "error", err)
		return
	}
	if raw != nil {
		s.ids = raw
	}
}

func (s *ArchiveStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("mkdir archive: %w", err)
	}
	data, err := json.MarshalIndent(s.ids, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal archive: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".archive-*.tmp")
	if err != nil {
		return fmt.Errorf("create archive temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write archive: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// IsArchived reports whether a transcript is hidden from the library.
func (s *ArchiveStore) IsArchived(id string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.ids[id]
	return ok
}

// Entries returns a copy of all archive entries.
func (s *ArchiveStore) Entries() map[string]ArchiveEntry {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]ArchiveEntry, len(s.ids))
	for k, v := range s.ids {
		out[k] = v
	}
	return out
}

// Archive hides the given transcript IDs.
func (s *ArchiveStore) Archive(ids []string, mode string) error {
	if s == nil {
		return fmt.Errorf("archive store not configured")
	}
	if mode == "" {
		mode = "manual"
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := s.ids[id]; ok {
			continue
		}
		s.ids[id] = ArchiveEntry{ArchivedAt: now, Mode: mode}
		changed = true
	}
	if !changed {
		return nil
	}
	return s.saveLocked()
}

// Restore un-hides the given transcript IDs.
func (s *ArchiveStore) Restore(ids []string) error {
	if s == nil {
		return fmt.Errorf("archive store not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, id := range ids {
		if _, ok := s.ids[id]; ok {
			delete(s.ids, id)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.saveLocked()
}

// Prune drops entries whose transcript file no longer exists.
func (s *ArchiveStore) Prune(exists func(string) bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for id := range s.ids {
		if !exists(id) {
			delete(s.ids, id)
			changed = true
		}
	}
	if changed {
		if err := s.saveLocked(); err != nil {
			slog.Warn("archive: prune failed", "error", err)
		}
	}
}

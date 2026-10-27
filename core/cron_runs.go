package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// CronRun is one execution of a cron job. TranscriptID is empty when the run
// failed before any session was written.
type CronRun struct {
	ID           string    `json:"id"`
	JobID        string    `json:"job_id"`
	StartedAt    time.Time `json:"started_at"`
	DurationMs   int64     `json:"duration_ms"`
	Status       string    `json:"status"` // running | success | failed
	Error        string    `json:"error,omitempty"`
	Trigger      string    `json:"trigger"` // schedule | manual
	TranscriptID string    `json:"transcript_id,omitempty"`
}

// CronRunStore keeps the last N executions per job. Failed runs are the only
// record of an execution that produced no transcript, so they must persist.
type CronRunStore struct {
	mu        sync.Mutex
	path      string
	runs      map[string][]*CronRun
	maxPerJob int
}

// NewCronRunStore loads (or initialises) the run history at path.
func NewCronRunStore(path string) *CronRunStore {
	s := &CronRunStore{path: path, runs: map[string][]*CronRun{}, maxPerJob: 50}
	s.reload()
	return s
}

func (s *CronRunStore) reload() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if !os.IsNotExist(err) {
			slogWarn("cron runs: read failed, starting empty", err)
		}
		return
	}
	var raw map[string][]*CronRun
	if err := json.Unmarshal(data, &raw); err != nil {
		slogWarn("cron runs: corrupt history, starting empty", err)
		return
	}
	if raw != nil {
		s.runs = raw
	}
}

func (s *CronRunStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("mkdir cron runs: %w", err)
	}
	data, err := json.MarshalIndent(s.runs, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".runs-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
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

func newRunID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// Start records a run in progress and returns it.
func (s *CronRunStore) Start(jobID, trigger string) *CronRun {
	run := &CronRun{
		ID:        newRunID(),
		JobID:     jobID,
		StartedAt: time.Now(),
		Status:    "running",
		Trigger:   trigger,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[jobID] = append(s.runs[jobID], run)
	if len(s.runs[jobID]) > s.maxPerJob {
		s.runs[jobID] = s.runs[jobID][len(s.runs[jobID])-s.maxPerJob:]
	}
	if err := s.saveLocked(); err != nil {
		slogWarn("cron runs: persist start failed", err)
	}
	return run
}

// Finish closes a run with its outcome.
func (s *CronRunStore) Finish(jobID, runID string, err error, transcriptID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.runs[jobID] {
		if r.ID != runID {
			continue
		}
		r.DurationMs = time.Since(r.StartedAt).Milliseconds()
		if err != nil {
			r.Status = "failed"
			r.Error = err.Error()
		} else {
			r.Status = "success"
		}
		if transcriptID != "" {
			r.TranscriptID = transcriptID
		}
		break
	}
	if err := s.saveLocked(); err != nil {
		slogWarn("cron runs: persist finish failed", err)
	}
}

// AttachTranscriptID fills the transcript id on the job's currently running run
// as soon as the agent process has produced one. This lets the web UI link into
// an in-flight session instead of waiting for the run to finish.
func (s *CronRunStore) AttachTranscriptID(jobID, transcriptID string) {
	if transcriptID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runs := s.runs[jobID]
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Status == "running" {
			runs[i].TranscriptID = transcriptID
			break
		}
	}
	if err := s.saveLocked(); err != nil {
		slogWarn("cron runs: persist attach transcript failed", err)
	}
}

// List returns a job's runs, newest first.
func (s *CronRunStore) List(jobID string) []*CronRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.runs[jobID]
	out := make([]*CronRun, len(src))
	for i, r := range src {
		c := *r
		out[i] = &c
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// Delete drops a job's history (job removed).
func (s *CronRunStore) Delete(jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runs[jobID]; !ok {
		return
	}
	delete(s.runs, jobID)
	if err := s.saveLocked(); err != nil {
		slogWarn("cron runs: persist delete failed", err)
	}
}

func slogWarn(msg string, err error) {
	slog.Warn(msg, "error", err)
}

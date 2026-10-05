// Copyright 2026 The Atlantis Authors
// SPDX-License-Identifier: Apache-2.0

package jobs

import (
	"container/list"
	"sync"
	"time"

	"github.com/runatlantis/atlantis/server/events/models"
)

// BetaJobStatus describes one observed execution, independently of its output stream.
type BetaJobStatus uint8

const (
	BetaJobUnknown BetaJobStatus = iota
	BetaJobRunning
	BetaJobSucceeded
	BetaJobFailed
)

// BetaJobSnapshot contains only value metadata; it never retains command output or errors.
type BetaJobSnapshot struct {
	JobID        string
	Pull         PullInfo
	Operation    string
	Status       BetaJobStatus
	StartedAt    time.Time
	FinishedAt   time.Time
	PlanStats    models.PlanSuccessStats
	HasPlanStats bool
}

// BetaJobGeneration identifies an observation. Its zero value represents a declined
// observation. Callers must retain it for completion and deferred finalization.
type BetaJobGeneration struct {
	identity *betaJobGenerationIdentity
}

// A nonzero-sized allocation gives each observation a distinct identity, even
// across deletion, eviction, store instances, and reuse of the same job ID.
type betaJobGenerationIdentity struct {
	marker byte
}

type betaJobStatusEntry struct {
	snapshot   BetaJobSnapshot
	generation BetaJobGeneration
	terminal   *list.Element
}

const betaJobStatusLimit = 10_000

// BetaJobStatusStore is a bounded, in-memory store for beta dashboard metadata.
// The zero value is usable. All methods also tolerate a nil store.
type BetaJobStatusStore struct {
	mu       sync.Mutex
	entries  map[string]*betaJobStatusEntry
	terminal list.List // job IDs in order of terminal transition, oldest first
	limit    int
}

func NewBetaJobStatusStore() *BetaJobStatusStore {
	return newBetaJobStatusStore(betaJobStatusLimit)
}

// The capacity is internal: tests may inject a smaller bound without introducing
// a server configuration option.
func newBetaJobStatusStore(limit int) *BetaJobStatusStore {
	return &BetaJobStatusStore{
		entries: make(map[string]*betaJobStatusEntry),
		limit:   limit,
	}
}

// Start records Running and clears any previous result for jobID. At capacity it
// evicts the oldest terminal observation; if all entries are running it declines
// observation by returning the zero token. Execution need not wait for metadata.
func (s *BetaJobStatusStore) Start(jobID string, pull PullInfo, operation string) BetaJobGeneration {
	if s == nil || jobID == "" {
		return BetaJobGeneration{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[string]*betaJobStatusEntry)
		s.limit = betaJobStatusLimit
	}
	if previous, ok := s.entries[jobID]; ok {
		s.remove(jobID, previous)
	} else if len(s.entries) >= s.limit {
		oldest := s.terminal.Front()
		if oldest == nil {
			return BetaJobGeneration{}
		}
		id := oldest.Value.(string)
		s.remove(id, s.entries[id])
	}
	generation := BetaJobGeneration{identity: &betaJobGenerationIdentity{}}
	s.entries[jobID] = &betaJobStatusEntry{
		snapshot: BetaJobSnapshot{
			JobID:     jobID,
			Pull:      pull,
			Operation: operation,
			Status:    BetaJobRunning,
			StartedAt: time.Now(),
		},
		generation: generation,
	}
	return generation
}

// Finish records a successful or failed execution once. Counts are retained only
// for successful plans with explicitly available statistics. Invalid statuses,
// stale tokens, and callbacks for removed observations are ignored.
func (s *BetaJobStatusStore) Finish(jobID string, generation BetaJobGeneration, status BetaJobStatus, stats models.PlanSuccessStats, hasStats bool) {
	if s == nil || (status != BetaJobSucceeded && status != BetaJobFailed) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.match(jobID, generation)
	if entry == nil || entry.snapshot.Status != BetaJobRunning {
		return
	}
	if status == BetaJobSucceeded && entry.snapshot.Operation == "plan" && hasStats {
		entry.snapshot.PlanStats = stats
		entry.snapshot.HasPlanStats = true
	}
	s.complete(jobID, entry, status)
}

// Interrupt marks an unfinished observation Unknown, for example on an abnormal
// exit. It cannot overwrite a completed execution.
func (s *BetaJobStatusStore) Interrupt(jobID string, generation BetaJobGeneration) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.match(jobID, generation)
	if entry != nil && entry.snapshot.Status == BetaJobRunning {
		s.complete(jobID, entry, BetaJobUnknown)
	}
}

// MarkFinalizationFailure is the sole exception to immutable completion: a
// deferred publication/persistence failure can change Succeeded to Failed. It
// clears plan counts and preserves the original execution finish time.
func (s *BetaJobStatusStore) MarkFinalizationFailure(jobID string, generation BetaJobGeneration) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.match(jobID, generation)
	if entry != nil && entry.snapshot.Status == BetaJobSucceeded {
		entry.snapshot.Status = BetaJobFailed
		entry.snapshot.PlanStats = models.PlanSuccessStats{}
		entry.snapshot.HasPlanStats = false
	}
}

// Snapshot returns an independent map of value copies for one dashboard response.
func (s *BetaJobStatusStore) Snapshot() map[string]BetaJobSnapshot {
	result := make(map[string]BetaJobSnapshot)
	if s == nil {
		return result
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, entry := range s.entries {
		result[id] = entry.snapshot
	}
	return result
}

// Remove deletes only entries whose entire project or hook identity matches pull.
// Subsequent callbacks cannot recreate those entries.
func (s *BetaJobStatusStore) Remove(pull PullInfo) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, entry := range s.entries {
		if entry.snapshot.Pull == pull {
			s.remove(id, entry)
		}
	}
}

// Helpers below are called only while mu is held.
func (s *BetaJobStatusStore) match(jobID string, generation BetaJobGeneration) *betaJobStatusEntry {
	entry := s.entries[jobID]
	if generation.identity == nil || entry == nil || entry.generation != generation {
		return nil
	}
	return entry
}

func (s *BetaJobStatusStore) complete(jobID string, entry *betaJobStatusEntry, status BetaJobStatus) {
	entry.snapshot.Status = status
	entry.snapshot.FinishedAt = time.Now()
	entry.terminal = s.terminal.PushBack(jobID)
}

func (s *BetaJobStatusStore) remove(jobID string, entry *betaJobStatusEntry) {
	if entry.terminal != nil {
		s.terminal.Remove(entry.terminal)
	}
	delete(s.entries, jobID)
}

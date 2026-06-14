// SPDX-License-Identifier: Apache-2.0

package flows

import (
	"sync"
	"time"
)

// Flow is one distinct canonical structure plus its occurrence stats.
type Flow struct {
	RootHash  string
	Structure *CanonicalNode
	Count     int
	FirstSeen time.Time
	LastSeen  time.Time
}

// FlowSnapshot is a value copy of a Flow for the Redis writer, taken under the
// store lock so the writer never races concurrent Observe calls.
type FlowSnapshot struct {
	RootHash    string
	Structure   *CanonicalNode // immutable once canonicalized; safe to share
	Count       int
	FirstSeenMs int64
	LastSeenMs  int64
	NodeCount   int
}

// FlowStore accumulates distinct flows by root hash. Safe for concurrent use.
type FlowStore struct {
	mu    sync.Mutex
	flows map[string]*Flow
	dirty map[string]struct{}
}

// NewFlowStore returns an empty store.
func NewFlowStore() *FlowStore {
	return &FlowStore{
		flows: make(map[string]*Flow),
		dirty: make(map[string]struct{}),
	}
}

// Observe records one finalized trace. A new root hash inserts a flow; a repeat
// increments Count, updates LastSeen, and refreshes the structure so per-node
// metadata reflects the most recent trace (last-write-wins).
func (s *FlowStore) Observe(root *CanonicalNode, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.flows[root.Hash]; ok {
		f.Count++
		f.LastSeen = now
		f.Structure = root
	} else {
		s.flows[root.Hash] = &Flow{
			RootHash: root.Hash, Structure: root, Count: 1, FirstSeen: now, LastSeen: now,
		}
	}
	s.dirty[root.Hash] = struct{}{}
}

// SnapshotDirty returns value copies of the flows changed since the last call
// and clears the dirty set.
func (s *FlowStore) SnapshotDirty() []FlowSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]FlowSnapshot, 0, len(s.dirty))
	for h := range s.dirty {
		f := s.flows[h]
		out = append(out, FlowSnapshot{
			RootHash:    f.RootHash,
			Structure:   f.Structure,
			Count:       f.Count,
			FirstSeenMs: f.FirstSeen.UnixMilli(),
			LastSeenMs:  f.LastSeen.UnixMilli(),
			NodeCount:   countNodes(f.Structure),
		})
	}
	s.dirty = make(map[string]struct{})
	return out
}

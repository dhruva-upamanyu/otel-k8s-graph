// SPDX-License-Identifier: Apache-2.0

package graph

import "time"

// BatchWriteSet is the write path's in-memory graph accumulator. It
// implements Writer, so builder.Derive can target it directly and reuse all
// existing derivation rules; because it stores SETS keyed by identity,
// re-deriving the same series every scrape collapses to no-ops instead of
// growing unbounded.
//
// Each entity and edge carries a last-seen time, stamped on every Merge, so
// Expire can drop entries no longer being observed. It is self-contained
// (depends on nothing in the legacy write path). It is NOT safe for
// concurrent use; the receiver guards the shared instance with a mutex.
type BatchWriteSet struct {
	entities map[string]entitySetVal
	edges    map[edgeSetKey]time.Time // value = last-seen
	metas    map[string]map[string]string
}

type entitySetVal struct {
	kind     Kind
	name     string
	lastSeen time.Time
}

type edgeSetKey struct {
	from   string
	kind   EdgeKind
	to     string
	action string
}

// NewBatchWriteSet returns an empty set.
func NewBatchWriteSet() *BatchWriteSet {
	return &BatchWriteSet{
		entities: make(map[string]entitySetVal),
		edges:    make(map[edgeSetKey]time.Time),
		metas:    make(map[string]map[string]string),
	}
}

// Upsert records an entity (idempotent by id). Last-seen is stamped by Merge,
// so request-local sets built via Upsert/UpsertWithEdges leave it zero.
func (s *BatchWriteSet) Upsert(id string, kind Kind, name string) {
	s.entities[id] = entitySetVal{kind: kind, name: name}
}

// UpsertWithEdges records the source entity, each target entity, and each
// directed edge. Duplicate edges (same from/kind/to/action) collapse.
func (s *BatchWriteSet) UpsertWithEdges(fromID string, fromKind Kind, fromName string, edges []EdgeTo) {
	s.entities[fromID] = entitySetVal{kind: fromKind, name: fromName}
	for _, e := range edges {
		s.entities[e.ToID] = entitySetVal{kind: e.ToKind, name: e.ToName}
		s.edges[edgeSetKey{from: fromID, kind: e.Kind, to: e.ToID, action: e.Action}] = time.Time{}
	}
}

// AddEdges records directed edges from fromID without upserting fromID or the
// targets as entities. Used to anchor a relationship edge onto an entity owned
// by another writer (e.g. a watcher-owned container). Last-seen is stamped by
// Merge, like other edges.
func (s *BatchWriteSet) AddEdges(fromID string, edges []EdgeTo) {
	for _, e := range edges {
		s.edges[edgeSetKey{from: fromID, kind: e.Kind, to: e.ToID, action: e.Action}] = time.Time{}
	}
}

// SetMetadata records an entity's metadata once. An entity's metadata
// (image, sdk language, k8s labels, ...) is stable across all of its
// datapoints, so the first sighting wins and later identical calls are
// skipped — this avoids re-merging the same resource attribute map once per
// datapoint (hundreds of thousands of redundant writes per request).
func (s *BatchWriteSet) SetMetadata(id string, metadata map[string]string) {
	if len(metadata) == 0 {
		return
	}
	if _, ok := s.metas[id]; ok {
		return
	}
	m := make(map[string]string, len(metadata))
	for k, v := range metadata {
		if v == "" {
			continue
		}
		m[k] = v
	}
	if len(m) > 0 {
		s.metas[id] = m
	}
}

// Merge unions other into s, stamping every merged entity and edge with now
// as its last-seen time. Metadata is recorded once per entity (see
// SetMetadata); it is re-added if the entity was previously expired.
func (s *BatchWriteSet) Merge(other *BatchWriteSet, now time.Time) {
	if other == nil {
		return
	}
	for id, v := range other.entities {
		s.entities[id] = entitySetVal{kind: v.kind, name: v.name, lastSeen: now}
	}
	for k := range other.edges {
		s.edges[k] = now
	}
	for id, md := range other.metas {
		s.SetMetadata(id, md)
	}
}

// Expire removes every entity and edge last seen before cutoff (plus the
// metadata of removed entities) and returns how many of each were dropped.
func (s *BatchWriteSet) Expire(cutoff time.Time) (entities, edges int) {
	for id, v := range s.entities {
		if v.lastSeen.Before(cutoff) {
			delete(s.entities, id)
			delete(s.metas, id)
			entities++
		}
	}
	for k, lastSeen := range s.edges {
		if lastSeen.Before(cutoff) {
			delete(s.edges, k)
			edges++
		}
	}
	return entities, edges
}

// Counts returns the number of unique entities, edges, and entities carrying
// metadata.
func (s *BatchWriteSet) Counts() (entities, edges, metas int) {
	return len(s.entities), len(s.edges), len(s.metas)
}

// compile-time check that *BatchWriteSet satisfies Writer.
var _ Writer = (*BatchWriteSet)(nil)

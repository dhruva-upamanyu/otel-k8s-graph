// SPDX-License-Identifier: Apache-2.0

package flows

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Finalizer receives the canonical root of a finalized trace.
type Finalizer func(root *CanonicalNode, now time.Time)

type bucket struct {
	spans      map[string]SpanRecord // by span id (OTLP retries dedup here)
	firstSeen  time.Time
	finalizeAt time.Time
	rootSeen   bool
}

// Assembler buffers spans by trace id and finalizes each trace after the
// finalize trigger (root span seen OR window elapsed) plus a grace window.
// It is safe for concurrent Ingest; Sweep runs on a single ticker goroutine.
type Assembler struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	window    time.Duration
	grace     time.Duration
	maxTraces int
	clock     func() time.Time
	onFinal   Finalizer
	logger    *slog.Logger
}

// NewAssembler builds an Assembler. window is the no-root cap (10s), grace the
// straggler window (3s), maxTraces the buffer cap (oldest dropped past it).
func NewAssembler(window, grace time.Duration, maxTraces int, clock func() time.Time, onFinal Finalizer, logger *slog.Logger) *Assembler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Assembler{
		buckets:   make(map[string]*bucket),
		window:    window,
		grace:     grace,
		maxTraces: maxTraces,
		clock:     clock,
		onFinal:   onFinal,
		logger:    logger,
	}
}

// Ingest buffers one span into its trace bucket and updates the finalize deadline.
func (a *Assembler) Ingest(r SpanRecord) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.clock()
	b := a.buckets[r.TraceID]
	if b == nil {
		if len(a.buckets) >= a.maxTraces {
			a.evictOldestLocked()
		}
		b = &bucket{
			spans:      make(map[string]SpanRecord),
			firstSeen:  now,
			finalizeAt: now.Add(a.window + a.grace), // no-root worst case
		}
		a.buckets[r.TraceID] = b
	}
	b.spans[r.SpanID] = r
	if r.IsRoot() && !b.rootSeen {
		b.rootSeen = true
		if cand := now.Add(a.grace); cand.Before(b.finalizeAt) {
			b.finalizeAt = cand
		}
	}
}

// Sweep finalizes every bucket whose deadline has passed. Buckets are detached
// under the lock, then canonicalized outside it.
func (a *Assembler) Sweep() {
	now := a.clock()
	a.mu.Lock()
	var ready []*bucket
	for id, b := range a.buckets {
		if !now.Before(b.finalizeAt) {
			ready = append(ready, b)
			delete(a.buckets, id)
		}
	}
	a.mu.Unlock()

	for _, b := range ready {
		recs := make([]SpanRecord, 0, len(b.spans))
		for _, r := range b.spans {
			recs = append(recs, r)
		}
		a.onFinal(canonicalize(buildTree(recs)), now)
	}
}

// Run sweeps on interval until ctx is cancelled.
func (a *Assembler) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.Sweep()
		}
	}
}

// evictOldestLocked drops the bucket with the earliest firstSeen. Caller holds mu.
func (a *Assembler) evictOldestLocked() {
	var oldestID string
	var oldest time.Time
	for id, b := range a.buckets {
		if oldestID == "" || b.firstSeen.Before(oldest) {
			oldestID, oldest = id, b.firstSeen
		}
	}
	if oldestID != "" {
		delete(a.buckets, oldestID)
		a.logger.Warn("flows: buffer full, dropped oldest trace", slog.String("trace_id", oldestID))
	}
}

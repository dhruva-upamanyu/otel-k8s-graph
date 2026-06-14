// SPDX-License-Identifier: Apache-2.0

package flows

import (
	"testing"
	"time"
)

func flow(meta string) *CanonicalNode {
	return canonicalize(buildTree([]SpanRecord{
		rec("1", "", "gateway", "GET /"),
		recAt("2", "1", "orders", "GET /orders", 1, map[string]string{"v": meta}),
	}))
}

func TestFlowStore_ObserveCountsAndLastWriteWins(t *testing.T) {
	s := NewFlowStore()
	t0 := time.Unix(1000, 0)
	t1 := time.Unix(1005, 0)

	s.Observe(flow("old"), t0)
	s.Observe(flow("new"), t1) // same structure (hash), newer metadata

	snap := s.SnapshotDirty()
	if len(snap) != 1 {
		t.Fatalf("want 1 distinct flow, got %d", len(snap))
	}
	f := snap[0]
	if f.Count != 2 {
		t.Fatalf("count = %d, want 2", f.Count)
	}
	if f.FirstSeenMs != t0.UnixMilli() || f.LastSeenMs != t1.UnixMilli() {
		t.Fatalf("first/last = %d/%d, want %d/%d", f.FirstSeenMs, f.LastSeenMs, t0.UnixMilli(), t1.UnixMilli())
	}
	if f.NodeCount != 2 {
		t.Fatalf("node count = %d, want 2", f.NodeCount)
	}
	if got := f.Structure.Children[0].Meta["v"]; got != "new" {
		t.Fatalf("metadata last-write-wins failed: %q", got)
	}
}

func TestFlowStore_SnapshotDirtyClears(t *testing.T) {
	s := NewFlowStore()
	s.Observe(flow("x"), time.Unix(1, 0))
	if len(s.SnapshotDirty()) != 1 {
		t.Fatal("first snapshot should return the flow")
	}
	if len(s.SnapshotDirty()) != 0 {
		t.Fatal("second snapshot should be empty (dirty cleared)")
	}
}

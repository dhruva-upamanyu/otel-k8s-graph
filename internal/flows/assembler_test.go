// SPDX-License-Identifier: Apache-2.0

package flows

import (
	"sync"
	"testing"
	"time"
)

// fakeClock returns a settable time for deterministic deadline tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) set(t time.Time) { c.mu.Lock(); defer c.mu.Unlock(); c.t = t }

func newTestAssembler(clk *fakeClock, sink *[]*CanonicalNode) *Assembler {
	return NewAssembler(10*time.Second, 3*time.Second, 100, clk.now,
		func(root *CanonicalNode, _ time.Time) { *sink = append(*sink, root) }, nil)
}

func TestAssembler_RootFinalizesAtRootPlusGrace(t *testing.T) {
	base := time.Unix(1000, 0)
	clk := &fakeClock{t: base}
	var got []*CanonicalNode
	a := newTestAssembler(clk, &got)

	a.Ingest(rec("2", "1", "orders", "GET /orders")) // child first
	clk.set(base.Add(1 * time.Second))
	a.Ingest(rec("1", "", "gateway", "GET /")) // root at +1s -> finalize at +4s

	clk.set(base.Add(3 * time.Second))
	a.Sweep()
	if len(got) != 0 {
		t.Fatalf("should not finalize before root+grace, got %d", len(got))
	}
	clk.set(base.Add(4 * time.Second))
	a.Sweep()
	if len(got) != 1 {
		t.Fatalf("should finalize at root+grace, got %d", len(got))
	}
}

func TestAssembler_NoRootFinalizesAtWindowPlusGrace(t *testing.T) {
	base := time.Unix(2000, 0)
	clk := &fakeClock{t: base}
	var got []*CanonicalNode
	a := newTestAssembler(clk, &got)

	a.Ingest(rec("2", "1", "orders", "GET /orders")) // no root ever

	clk.set(base.Add(12 * time.Second))
	a.Sweep()
	if len(got) != 0 {
		t.Fatalf("should not finalize before 13s, got %d", len(got))
	}
	clk.set(base.Add(13 * time.Second))
	a.Sweep()
	if len(got) != 1 {
		t.Fatalf("should finalize at window+grace (13s), got %d", len(got))
	}
}

func TestAssembler_RetriedSpanDoesNotDuplicateNode(t *testing.T) {
	base := time.Unix(3000, 0)
	clk := &fakeClock{t: base}
	var got []*CanonicalNode
	a := newTestAssembler(clk, &got)

	a.Ingest(rec("1", "", "gateway", "GET /"))
	a.Ingest(rec("2", "1", "orders", "GET /orders"))
	a.Ingest(rec("2", "1", "orders", "GET /orders")) // duplicate delivery, same span id

	clk.set(base.Add(3 * time.Second))
	a.Sweep()
	if len(got) != 1 || len(got[0].Children) != 1 {
		t.Fatalf("duplicate span id should dedup to one child, got %+v", got)
	}
}

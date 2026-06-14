// SPDX-License-Identifier: Apache-2.0

package flows

import "testing"

// recAt is like rec but sets EndNano and Attrs for metadata-selection tests.
func recAt(spanID, parentID, dep, name string, end uint64, attrs map[string]string) SpanRecord {
	r := rec(spanID, parentID, dep, name)
	r.EndNano = end
	r.Attrs = attrs
	return r
}

func TestCanonicalize_CollapsesDuplicateSiblingsAndOrder(t *testing.T) {
	// gateway -> {orders (retry), orders, inventory}
	collapsed := canonicalize(buildTree([]SpanRecord{
		rec("1", "", "gateway", "GET /"),
		rec("2", "1", "orders", "GET /orders"),
		rec("3", "1", "orders", "GET /orders"),
		rec("4", "1", "inventory", "GET /inv"),
	}))
	// gateway -> {inventory, orders} built in a different order
	single := canonicalize(buildTree([]SpanRecord{
		rec("a", "", "gateway", "GET /"),
		rec("b", "a", "inventory", "GET /inv"),
		rec("c", "a", "orders", "GET /orders"),
	}))
	if collapsed.Hash != single.Hash {
		t.Fatalf("retries/order should collapse: %s != %s", collapsed.Hash, single.Hash)
	}
	if len(collapsed.Children) != 2 {
		t.Fatalf("want 2 distinct children, got %d", len(collapsed.Children))
	}
}

func TestCanonicalize_DifferentStructureDiffersAndSortsChildren(t *testing.T) {
	a := canonicalize(buildTree([]SpanRecord{
		rec("1", "", "gateway", "GET /"),
		rec("2", "1", "orders", "GET /orders"),
	}))
	b := canonicalize(buildTree([]SpanRecord{
		rec("1", "", "gateway", "GET /"),
		rec("2", "1", "payments", "POST /pay"),
	}))
	if a.Hash == b.Hash {
		t.Fatal("different downstream should produce different hashes")
	}
	// children are sorted by hash (deterministic)
	multi := canonicalize(buildTree([]SpanRecord{
		rec("1", "", "gateway", "GET /"),
		rec("2", "1", "orders", "GET /orders"),
		rec("3", "1", "payments", "POST /pay"),
	}))
	for i := 1; i < len(multi.Children); i++ {
		if multi.Children[i-1].Hash > multi.Children[i].Hash {
			t.Fatal("children must be sorted by hash")
		}
	}
}

func TestCanonicalize_MetadataFromMostRecentDuplicate(t *testing.T) {
	root := canonicalize(buildTree([]SpanRecord{
		rec("1", "", "gateway", "GET /"),
		recAt("2", "1", "orders", "GET /orders", 100, map[string]string{"v": "old"}),
		recAt("3", "1", "orders", "GET /orders", 200, map[string]string{"v": "new"}),
	}))
	if len(root.Children) != 1 {
		t.Fatalf("duplicates should collapse to 1, got %d", len(root.Children))
	}
	if got := root.Children[0].Meta["v"]; got != "new" {
		t.Fatalf("metadata should come from latest span (EndNano 200), got %q", got)
	}
}

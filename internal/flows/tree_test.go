// SPDX-License-Identifier: Apache-2.0

package flows

import "testing"

func rec(spanID, parentID, dep, name string) SpanRecord {
	return SpanRecord{
		TraceID: "t", SpanID: spanID, ParentID: parentID,
		Deployment: dep, Namespace: "ns", Name: name,
	}
}

func TestBuildTree_SingleRoot(t *testing.T) {
	root := buildTree([]SpanRecord{
		rec("1", "", "gateway", "GET /"),
		rec("2", "1", "orders", "GET /orders"),
	})
	if root.rec.SpanID != "1" {
		t.Fatalf("root span = %q, want 1", root.rec.SpanID)
	}
	if len(root.children) != 1 || root.children[0].rec.SpanID != "2" {
		t.Fatalf("want one child span 2, got %+v", root.children)
	}
}

func TestBuildTree_ForestGetsVirtualRoot(t *testing.T) {
	root := buildTree([]SpanRecord{
		rec("1", "", "a", "x"),
		rec("2", "", "b", "y"),
	})
	if root.rec.SpanID != "" || root.rec.Name != "" {
		t.Fatalf("virtual root should have empty identity, got %+v", root.rec)
	}
	if len(root.children) != 2 {
		t.Fatalf("virtual root should have 2 children, got %d", len(root.children))
	}
}

func TestBuildTree_MissingParentTreatedAsRoot(t *testing.T) {
	// span "2" references parent "99" which is absent -> it becomes a root,
	// and with the single real root "1" that is two roots -> virtual root.
	root := buildTree([]SpanRecord{
		rec("1", "", "a", "x"),
		rec("2", "99", "b", "y"),
	})
	if root.rec.SpanID != "" {
		t.Fatalf("expected virtual root for forest, got span %q", root.rec.SpanID)
	}
	if len(root.children) != 2 {
		t.Fatalf("want 2 roots under virtual root, got %d", len(root.children))
	}
}

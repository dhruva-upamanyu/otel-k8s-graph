// SPDX-License-Identifier: Apache-2.0

package flows

// treeNode is a span plus its child spans, before canonicalization.
type treeNode struct {
	rec      SpanRecord
	children []*treeNode
}

// buildTree reassembles records into a single tree via parent links. A record
// is a root when it has no parent or its parent is absent from the set (dropped
// or late span). When there is more than one root the trace is a forest, so the
// roots hang under a synthetic virtual root with empty identity — this keeps
// "one trace = one flow" and lets multi-root traces collapse consistently.
func buildTree(recs []SpanRecord) *treeNode {
	nodes := make(map[string]*treeNode, len(recs))
	for _, r := range recs {
		nodes[r.SpanID] = &treeNode{rec: r}
	}
	var roots []*treeNode
	for _, r := range recs {
		n := nodes[r.SpanID]
		if r.ParentID == "" {
			roots = append(roots, n)
			continue
		}
		if parent, ok := nodes[r.ParentID]; ok {
			parent.children = append(parent.children, n)
		} else {
			roots = append(roots, n)
		}
	}
	if len(roots) == 1 {
		return roots[0]
	}
	return &treeNode{children: roots} // virtual root: zero-value rec = empty identity
}

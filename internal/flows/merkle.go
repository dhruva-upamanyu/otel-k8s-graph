// SPDX-License-Identifier: Apache-2.0

package flows

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
)

// CanonicalNode is the collapsed, hashed form of a tree node. Children are
// deduplicated by subtree Hash and sorted by Hash, so sibling order, retries,
// and fan-out count do not affect the structure. Hash is the Merkle hash of
// this node's identity plus its distinct child hashes; the root node's Hash is
// the flow id. Meta is the span attributes of the most recent span that mapped
// to this node. A CanonicalNode is immutable once returned.
type CanonicalNode struct {
	Hash       string            `json:"h"`
	Deployment string            `json:"deployment"`
	Namespace  string            `json:"ns"`
	Name       string            `json:"name"`
	Meta       map[string]string `json:"meta,omitempty"`
	Children   []*CanonicalNode  `json:"children,omitempty"`

	srcEndNano uint64 // unexported: latest span end among collapsed duplicates; not serialized
}

// canonicalize collapses a treeNode bottom-up into a CanonicalNode.
func canonicalize(n *treeNode) *CanonicalNode {
	// Canonicalize children, then keep one representative per distinct hash —
	// the representative with the latest source span (max EndNano) so its
	// metadata is the most recent observation.
	byHash := make(map[string]*CanonicalNode)
	for _, child := range n.children {
		cc := canonicalize(child)
		if ex, ok := byHash[cc.Hash]; !ok || cc.srcEndNano > ex.srcEndNano {
			byHash[cc.Hash] = cc
		}
	}
	distinct := make([]*CanonicalNode, 0, len(byHash))
	for _, c := range byHash {
		distinct = append(distinct, c)
	}
	sort.Slice(distinct, func(i, j int) bool { return distinct[i].Hash < distinct[j].Hash })

	h := sha256.New()
	h.Write([]byte(n.rec.Deployment))
	h.Write([]byte{0x1f})
	h.Write([]byte(n.rec.Namespace))
	h.Write([]byte{0x1f})
	h.Write([]byte(n.rec.Name))
	h.Write([]byte{0x1e})
	for _, c := range distinct {
		h.Write([]byte(c.Hash))
		h.Write([]byte{0x1d})
	}

	return &CanonicalNode{
		Hash:       hex.EncodeToString(h.Sum(nil)),
		Deployment: n.rec.Deployment,
		Namespace:  n.rec.Namespace,
		Name:       n.rec.Name,
		Meta:       n.rec.Attrs,
		Children:   distinct,
		srcEndNano: n.rec.EndNano,
	}
}

// countNodes returns the number of nodes in a canonical tree.
func countNodes(n *CanonicalNode) int {
	total := 1
	for _, c := range n.Children {
		total += countNodes(c)
	}
	return total
}

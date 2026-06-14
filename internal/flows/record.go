// SPDX-License-Identifier: Apache-2.0

// Package flows reassembles OTLP trace spans into per-trace trees and collapses
// them into abstract, Merkle-canonicalized structures ("flows") for storage.
package flows

// SpanRecord is a deep-copied, source-agnostic span. The receiver populates it
// from an OTLP span (pdata is freed after the Export call, so all fields are
// copies). Name is the already-templatized span name. Deployment/Namespace come
// from resource attributes; Attrs are the span's own attributes, used as node
// metadata.
type SpanRecord struct {
	TraceID    string
	SpanID     string
	ParentID   string // "" when the span is a trace root
	Deployment string
	Namespace  string
	Name       string
	StartNano  uint64
	EndNano    uint64
	Attrs      map[string]string
}

// IsRoot reports whether the span has no parent (a trace root).
func (r SpanRecord) IsRoot() bool { return r.ParentID == "" }

// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"net/url"
	"strings"
)

// TemplatizeAttrs collapses high-cardinality numeric IDs in the datapoint
// attributes that feed entity/edge derivation, in place. Intended to run
// once at ingest (in the receiver's collectRecords) so normalization happens
// at the boundary rather than scattered across the derivers. Only the two
// known high-cardinality keys are touched. Idempotent.
func TemplatizeAttrs(attrs map[string]string) {
	if v, ok := attrs[attrURLFull]; ok {
		attrs[attrURLFull] = templatizeURL(v)
	}
	if v, ok := attrs[attrSpanName]; ok {
		attrs[attrSpanName] = templatizeSpanName(v)
	}
}

// templatizeURL collapses numeric path segments of a URL (or bare path),
// dropping any query/fragment. Full URLs keep their scheme+host; reassembled
// manually because url.URL.String() would percent-encode the "{}" of "{n}".
func templatizeURL(s string) string {
	if s == "" {
		return s
	}
	if strings.HasPrefix(s, "/") {
		if i := strings.IndexAny(s, "?#"); i >= 0 {
			s = s[:i]
		}
		return templatizePath(s)
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return s
	}
	return u.Scheme + "://" + u.Host + templatizePath(u.Path)
}

// templatizeSpanName collapses numeric path segments in a span name so that
// names differing only by an integer id (e.g. "GET /orders/1" vs
// "GET /orders/2") canonicalize to the same value. Without it, every
// distinct id mints a new database QUERIES edge (the dedup key includes the
// action), defeating in-memory dedup since each is a genuinely distinct key.
//
// Rules (intentionally narrow):
//   - "/path/123"        -> "/path/{n}"        (bare path shape)
//   - "GET /path/123"    -> "GET /path/{n}"    (METHOD path shape)
//   - anything else (no leading "/", no "METHOD /path") is left untouched.
//
// Idempotent: already-templatized input is unchanged.
func templatizeSpanName(s string) string {
	if s == "" {
		return s
	}
	// Plain path shape: "/api/orders/123"
	if strings.HasPrefix(s, "/") {
		return templatizePath(s)
	}
	// "METHOD /path" shape: "GET /api/orders/123"
	sp := strings.IndexByte(s, ' ')
	if sp == -1 {
		return s
	}
	method, rest := s[:sp], s[sp+1:]
	if !strings.HasPrefix(rest, "/") {
		return s
	}
	return method + " " + templatizePath(rest)
}

// templatizePath rewrites every all-digit path segment to {n}. Idempotent.
func templatizePath(p string) string {
	if p == "" || p == "/" {
		return p
	}
	parts := strings.Split(p, "/")
	changed := false
	for i, seg := range parts {
		if isAllDigits(seg) {
			parts[i] = "{n}"
			changed = true
		}
	}
	if !changed {
		return p
	}
	return strings.Join(parts, "/")
}

// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/graph"
)

const (
	attrSpanKind      = "span.kind"
	attrHTTPMethod    = "http.request.method"
	attrHTTPRoute     = "http.route"
	attrURLFull       = "url.full"
	attrServerAddress = "server.address"
	attrPeerService   = "peer.service"
	attrServiceName   = "service.name"
)

// deriveEndpointEdges inspects a span-metric record and, if it represents
// an HTTP server or client side, upserts the corresponding endpoint
// entity and bidirectional edges to the emitting container/pod.
//
// Detection: span.kind == SPAN_KIND_SERVER/CLIENT AND
//
//	http.request.method is set in the series attrs.
//
// Server endpoints: endpoint:<service.name>/<method>/<http.route>;
// emitting container EXPOSES, endpoint EXPOSED_BY container.
//
// Client endpoints: endpoint:<target>/<method>/<url.full path>; target =
// peer.service if set, else server.address. Emitting container CALLS,
// endpoint CALLED_BY container.
//
// Records without an emitting container/pod, or missing the required
// endpoint-identifying attributes, are silently skipped.
func deriveEndpointEdges(g graph.Writer, r Record, emitterID string) {
	if emitterID == "" {
		return
	}
	spanKind := normalizeSpanKind(r.SeriesAttrs[attrSpanKind])
	if spanKind != "SERVER" && spanKind != "CLIENT" {
		return
	}
	method := r.SeriesAttrs[attrHTTPMethod]
	if method == "" {
		return
	}

	var (
		svcName, route string
		forward        graph.EdgeKind
		reverse        graph.EdgeKind
	)

	switch spanKind {
	case "SERVER":
		route = r.SeriesAttrs[attrHTTPRoute]
		if route == "" {
			return
		}
		// service.name lives in series attrs on span metrics emitted by the
		// spanmetrics connector. Fall back to resource attrs just in case.
		svcName = r.SeriesAttrs[attrServiceName]
		if svcName == "" {
			svcName = r.Attrs[attrServiceName]
		}
		if svcName == "" {
			return
		}
		forward, reverse = graph.EdgeExposes, graph.EdgeExposedBy

	case "CLIENT":
		urlFull := r.SeriesAttrs[attrURLFull]
		if urlFull == "" {
			return
		}
		route = extractURLPath(urlFull)
		if route == "" {
			return
		}
		// Prefer peer.service if instrumentation set it; otherwise fall back
		// to the called hostname. Strip any port from server.address.
		if peer := r.SeriesAttrs[attrPeerService]; peer != "" {
			svcName = peer
		} else {
			svcName = stripPort(r.SeriesAttrs[attrServerAddress])
		}
		if svcName == "" {
			return
		}
		forward, reverse = graph.EdgeCalls, graph.EdgeCalledBy
	}

	endpointID, endpointName := makeEndpointID(svcName, method, normalizeRoute(route))
	addRelationship(g, emitterID, forward, reverse,
		endpointID, graph.KindEndpoint, endpointName, "")
}

// emitterFor picks the most-specific entity to attach endpoint edges to,
// preferring container over pod. Returns ("", "", "") if neither is known.
func emitterFor(containerID, containerName, podID, podName string) (string, graph.Kind, string) {
	if containerID != "" {
		return containerID, graph.KindContainer, containerName
	}
	if podID != "" {
		return podID, graph.KindPod, podName
	}
	return "", "", ""
}

func normalizeSpanKind(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	return strings.TrimPrefix(s, "SPAN_KIND_")
}

// makeEndpointID returns (id, displayName). The display name is the
// "<METHOD> <route>" shape that's easier for an LLM consumer to read; the
// ID embeds the owning service to keep cluster-wide uniqueness.
func makeEndpointID(svc, method, route string) (string, string) {
	if !strings.HasPrefix(route, "/") {
		route = "/" + route
	}
	return "endpoint:" + svc + "/" + method + route, method + " " + route
}

// extractURLPath returns the path component of a URL. If s already looks
// like a bare path ("/..."), it is returned as-is (sans query/fragment).
func extractURLPath(s string) string {
	if strings.HasPrefix(s, "/") {
		if i := strings.IndexAny(s, "?#"); i >= 0 {
			return s[:i]
		}
		return s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	return u.Path
}

func stripPort(addr string) string {
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		return addr[:i]
	}
	return addr
}

// placeholderSegmentRe matches a single path segment that's a route
// placeholder in any common style: :foo, {foo}, <foo>. These come from
// framework-emitted http.route attributes (e.g. /api/orders/:id) and we
// normalize them so they collapse with the digit-templatized url.full
// path on the client side.
var placeholderSegmentRe = regexp.MustCompile(`^(?::[^/]+|\{[^/]+\}|<[^/]+>)$`)

// normalizeRoute rewrites placeholder-style segments AND all-digit
// segments to {n}. Idempotent: re-applying to already-templatized input
// is a no-op.
func normalizeRoute(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	parts := strings.Split(p, "/")
	changed := false
	for i, seg := range parts {
		if placeholderSegmentRe.MatchString(seg) || isAllDigits(seg) {
			parts[i] = "{n}"
			changed = true
		}
	}
	if !changed {
		return p
	}
	return strings.Join(parts, "/")
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

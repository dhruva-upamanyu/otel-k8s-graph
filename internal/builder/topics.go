// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"strings"

	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/graph"
)

const (
	attrSpanName         = "span.name"
	attrMessagingDestKey = "messaging.destination.name"
)

// deriveTopicEdges inspects a messaging span record and, if it represents a
// producer or consumer side, upserts the topic entity and bidirectional edges
// to the emitting container/pod.
//
// Detection: span.kind == SPAN_KIND_PRODUCER (publish) or
// SPAN_KIND_CONSUMER (process/receive). Topic name comes from
// messaging.destination.name — the authoritative OTel attribute raw spans
// carry — falling back to parsing the "<destination> <operation>" span.name
// convention for older emitters that don't set it (see topicName).
//
// Producer:  container PUBLISHES topic / topic PUBLISHED_BY container.
// Consumer:  container CONSUMES topic / topic CONSUMED_BY container.
//
// Records without an emitter or without a resolvable topic name are
// silently skipped.
func deriveTopicEdges(g graph.Writer, r Record, emitterID string) {
	if emitterID == "" {
		return
	}
	spanKind := normalizeSpanKind(r.SeriesAttrs[attrSpanKind])
	if spanKind != "PRODUCER" && spanKind != "CONSUMER" {
		return
	}
	topicName := resolveTopicName(r.SeriesAttrs)
	if topicName == "" {
		return
	}

	var forward, reverse graph.EdgeKind
	if spanKind == "PRODUCER" {
		forward, reverse = graph.EdgePublishes, graph.EdgePublishedBy
	} else {
		forward, reverse = graph.EdgeConsumes, graph.EdgeConsumedBy
	}

	topicID := "topic:" + topicName
	addRelationship(g, emitterID, forward, reverse,
		topicID, graph.KindTopic, topicName, "")
}

// resolveTopicName returns the topic name for a messaging span. It prefers the
// authoritative messaging.destination.name attribute (set by modern OTel
// messaging instrumentations) and falls back to parsing the span name for
// emitters that don't set it. Topic names are stable, low-cardinality
// identifiers (not path-like), so the value is used raw — no templatization.
func resolveTopicName(series map[string]string) string {
	if dest := series[attrMessagingDestKey]; dest != "" {
		return dest
	}
	return extractTopicName(series[attrSpanName])
}

// extractTopicName returns the first space-delimited token of a
// messaging span name, or "" if the name has no space (i.e. doesn't
// follow the "<destination> <operation>" convention).
func extractTopicName(spanName string) string {
	sp := strings.IndexByte(spanName, ' ')
	if sp <= 0 {
		return ""
	}
	return spanName[:sp]
}

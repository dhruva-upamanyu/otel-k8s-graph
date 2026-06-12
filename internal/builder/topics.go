// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"strings"

	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/graph"
)

const attrSpanName = "span.name"

// deriveTopicEdges inspects a messaging span-metric record and, if it
// represents a producer or consumer side, upserts the topic entity and
// bidirectional edges to the emitting container/pod.
//
// Detection: span.kind == SPAN_KIND_PRODUCER (publish) or
// SPAN_KIND_CONSUMER (process/receive). Topic name is extracted from
// span.name: OTel messaging convention names spans
// "<destination> <operation>" (e.g., "payment-topic publish",
// "payment-topic process"), so the first space-delimited token is the
// topic.
//
// Producer:  container PUBLISHES topic / topic PUBLISHED_BY container.
// Consumer:  container CONSUMES topic / topic CONSUMED_BY container.
//
// Records without an emitter or without a parseable topic name are
// silently skipped.
func deriveTopicEdges(g graph.Writer, r Record, emitterID string) {
	if emitterID == "" {
		return
	}
	spanKind := normalizeSpanKind(r.SeriesAttrs[attrSpanKind])
	if spanKind != "PRODUCER" && spanKind != "CONSUMER" {
		return
	}
	topicName := extractTopicName(r.SeriesAttrs[attrSpanName])
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

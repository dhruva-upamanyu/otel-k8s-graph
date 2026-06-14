// SPDX-License-Identifier: Apache-2.0

package builder

import "testing"

// The authoritative topic name is the messaging.destination.name attribute that
// raw OTel messaging spans carry. It must win over the span.name heuristic.
func TestDerive_Topic_ProducerUsesMessagingDestinationName(t *testing.T) {
	w := &recWriter{}
	Derive(w, Record{
		Attrs: map[string]string{
			"k8s.namespace.name": "default", "k8s.pod.name": "p-1", "k8s.container.name": "app",
		},
		SeriesAttrs: map[string]string{
			"span.kind":                  "SPAN_KIND_PRODUCER",
			"messaging.destination.name": "orders-topic",
			"span.name":                  "send misleading", // must be ignored
		},
	})
	topic := "topic:orders-topic"
	c := "container:default/p-1/app"

	if !w.hasUpsert(topic) {
		t.Errorf("topic not owned; upserts=%v", w.upserts)
	}
	if w.ownsStructural() {
		t.Errorf("structural entity upserted by relationships path; upserts=%v", w.upserts)
	}
	if !w.hasEdge(c + "|PUBLISHES|" + topic + "|") {
		t.Errorf("missing container PUBLISHES topic edge; edges=%v", w.edges)
	}
	if !w.hasEdge(topic + "|PUBLISHED_BY|" + c + "|") {
		t.Errorf("missing topic PUBLISHED_BY container edge; edges=%v", w.edges)
	}
}

func TestDerive_Topic_ConsumerUsesMessagingDestinationName(t *testing.T) {
	w := &recWriter{}
	Derive(w, Record{
		Attrs: map[string]string{
			"k8s.namespace.name": "default", "k8s.pod.name": "p-1", "k8s.container.name": "app",
		},
		SeriesAttrs: map[string]string{
			"span.kind":                  "SPAN_KIND_CONSUMER",
			"messaging.destination.name": "orders-topic",
		},
	})
	topic := "topic:orders-topic"
	c := "container:default/p-1/app"

	if !w.hasUpsert(topic) {
		t.Errorf("topic not owned; upserts=%v", w.upserts)
	}
	if !w.hasEdge(c + "|CONSUMES|" + topic + "|") {
		t.Errorf("missing container CONSUMES topic edge; edges=%v", w.edges)
	}
	if !w.hasEdge(topic + "|CONSUMED_BY|" + c + "|") {
		t.Errorf("missing topic CONSUMED_BY container edge; edges=%v", w.edges)
	}
}

// When messaging.destination.name is absent (older emitters), fall back to the
// "<destination> <operation>" span.name convention — no regression.
func TestDerive_Topic_FallsBackToSpanNameWhenAttrAbsent(t *testing.T) {
	w := &recWriter{}
	Derive(w, Record{
		Attrs: map[string]string{
			"k8s.namespace.name": "default", "k8s.pod.name": "p-1", "k8s.container.name": "app",
		},
		SeriesAttrs: map[string]string{
			"span.kind": "SPAN_KIND_PRODUCER",
			"span.name": "payment-topic publish", // no messaging.destination.name
		},
	})
	topic := "topic:payment-topic"
	c := "container:default/p-1/app"

	if !w.hasUpsert(topic) {
		t.Errorf("fallback topic not derived from span.name; upserts=%v", w.upserts)
	}
	if !w.hasEdge(c + "|PUBLISHES|" + topic + "|") {
		t.Errorf("missing PUBLISHES edge on fallback; edges=%v", w.edges)
	}
}

// With neither attribute nor a parseable span.name, no topic is derived.
func TestDerive_Topic_NoSourceDerivesNothing(t *testing.T) {
	w := &recWriter{}
	Derive(w, Record{
		Attrs: map[string]string{
			"k8s.namespace.name": "default", "k8s.pod.name": "p-1", "k8s.container.name": "app",
		},
		SeriesAttrs: map[string]string{
			"span.kind": "SPAN_KIND_PRODUCER",
			"span.name": "publish", // no space -> not the convention; no attr
		},
	})
	for _, id := range w.upserts {
		if id == "topic:publish" || id == "topic:" {
			t.Errorf("should not derive a topic from a non-conventional span name; upserts=%v", w.upserts)
		}
	}
}

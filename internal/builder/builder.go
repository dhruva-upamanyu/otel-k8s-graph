// SPDX-License-Identifier: Apache-2.0

// Package builder derives endpoint/topic/database RELATIONSHIPS and their edges
// from span-metric records. Structural entities (namespace/node/deployment/
// pod/container) and their containment/management edges are owned by the K8s
// watcher, not this path — Derive anchors relationship edges onto the emitting
// container/pod via AddEdges, without creating or owning those structural
// entities.
//
// Emitter ID format (used only as an edge anchor, never upserted here):
//
//	pod:<namespace>/<name>
//	container:<namespace>/<pod>/<name>
package builder

import (
	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/graph"
)

const (
	attrNamespace = "k8s.namespace.name"
	attrPod       = "k8s.pod.name"
	attrContainer = "k8s.container.name"
)

// Derive applies a single span-metric record to the graph, deriving any
// endpoint/topic/database relationship it carries. Returns whether the record
// had a usable emitter (container/pod) to anchor edges onto. It retains no
// reference to r's maps, so the caller may reuse r.SeriesAttrs after it returns.
func Derive(g graph.Writer, r Record) bool {
	namespace := r.Attrs[attrNamespace]
	pod := r.Attrs[attrPod]
	container := r.Attrs[attrContainer]

	// Compute the emitter (container > pod) as an edge anchor only. The
	// structural entities are owned by the K8s watcher; we never upsert them.
	var podID, containerID string
	if pod != "" && namespace != "" {
		podID = "pod:" + namespace + "/" + pod
	}
	if container != "" && podID != "" {
		containerID = "container:" + namespace + "/" + pod + "/" + container
	}
	emitterID, _, _ := emitterFor(containerID, container, podID, pod)
	if emitterID == "" {
		return false
	}

	deriveEndpointEdges(g, r, emitterID)
	deriveTopicEdges(g, r, emitterID)
	deriveDatabaseEdges(g, r, emitterID)
	return true
}

// addRelationship owns the relationship entity (endpoint/topic/database) and
// wires the bidirectional edge to the emitter. The forward edge is anchored
// onto the emitter via AddEdges, so the emitter (a watcher-owned container or
// pod) is never upserted by this path.
func addRelationship(g graph.Writer, emitterID string,
	forward, reverse graph.EdgeKind,
	relID string, relKind graph.Kind, relName, action string,
) {
	g.Upsert(relID, relKind, relName)
	g.AddEdges(relID, []graph.EdgeTo{{Kind: reverse, ToID: emitterID, Action: action}})
	g.AddEdges(emitterID, []graph.EdgeTo{{Kind: forward, ToID: relID, Action: action}})
}

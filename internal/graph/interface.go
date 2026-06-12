// SPDX-License-Identifier: Apache-2.0

// Package graph models the graph of Kubernetes and OTel-derived
// entities (namespaces, nodes, zones, regions, deployments, pods, containers,
// endpoints, topics, databases) and the edges between them.
//
// The read surface is the Graph interface, implemented by RedisGraph
// (redis.go) over a shared Redis instance. The write surface is the Writer
// interface: the K8s watcher writes structural entities through RedisWriter
// (rediswriter.go), and the OTel path accumulates relationships in a
// BatchWriteSet (batchset.go) that a background flusher writes to Redis.
package graph

import (
	"time"
)

type Kind string

const (
	KindNamespace  Kind = "namespace"
	KindNode       Kind = "node"
	KindZone       Kind = "zone"
	KindRegion     Kind = "region"
	KindDeployment Kind = "deployment"
	KindPod        Kind = "pod"
	KindContainer  Kind = "container"
	KindEndpoint   Kind = "endpoint"
	KindTopic      Kind = "topic"
	KindDatabase   Kind = "database"

	// K8s workload kinds
	KindStatefulSet  Kind = "statefulset"
	KindDaemonSet    Kind = "daemonset"
	KindJob          Kind = "job"
	KindCronJob      Kind = "cronjob"
	KindRollout      Kind = "rollout"
	KindHPA          Kind = "hpa"
	KindScaledObject Kind = "scaledobject"
)

type EdgeKind string

const (
	EdgeContains    EdgeKind = "CONTAINS"
	EdgeRunsIn      EdgeKind = "RUNS_IN"
	EdgeManages     EdgeKind = "MANAGES"
	EdgeManagedBy   EdgeKind = "MANAGED_BY"
	EdgeExposes     EdgeKind = "EXPOSES"
	EdgeExposedBy   EdgeKind = "EXPOSED_BY"
	EdgeCalls       EdgeKind = "CALLS"
	EdgeCalledBy    EdgeKind = "CALLED_BY"
	EdgePublishes   EdgeKind = "PUBLISHES"
	EdgePublishedBy EdgeKind = "PUBLISHED_BY"
	EdgeConsumes    EdgeKind = "CONSUMES"
	EdgeConsumedBy  EdgeKind = "CONSUMED_BY"
	EdgeQueries     EdgeKind = "QUERIES"
	EdgeQueriedBy   EdgeKind = "QUERIED_BY"

	// Scaling edges (used by HPA/KEDA tasks)
	EdgeScales   EdgeKind = "SCALES"
	EdgeScaledBy EdgeKind = "SCALED_BY"
)

type Edge struct {
	Kind       EdgeKind `json:"edge_kind"`
	ToResource string   `json:"toResource"`
	// Action distinguishes multiple edges of the same kind between the
	// same pair (e.g., several QUERIES edges from a container to one
	// database, each tagged with a different SQL operation). Empty for
	// structural edges where (kind, to) is already unique.
	Action string `json:"action,omitempty"`
}

// EdgeTo describes the far side of an edge plus the attributes needed to
// upsert that side if it does not already exist. Action is optional and
// participates in the dedup key alongside (Kind, ToID).
type EdgeTo struct {
	Kind   EdgeKind
	ToID   string
	ToKind Kind
	ToName string
	Action string
}

type Entity struct {
	ID         string            `json:"id"`
	Kind       Kind              `json:"kind"`
	Name       string            `json:"name"`
	LastSeenAt time.Time         `json:"last_seen_at"`
	Edges      []Edge            `json:"edges"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// SearchHit pairs a matching entity with the list of fields the query
// matched against. matched_in entries:
//
//	"id"
//	"name"
//	"metadata.<key>"
//	"edge.<KIND>.action"      (one entry per edge kind, not per matching edge)
type SearchHit struct {
	Entity    Entity   `json:"entity"`
	MatchedIn []string `json:"matched_in"`
}

// Writer is the write-only surface the builder targets. builder.Derive
// records mutations into a BatchWriteSet, which the receiver later flushes
// to Redis.
type Writer interface {
	Upsert(id string, kind Kind, name string)
	UpsertWithEdges(fromID string, fromKind Kind, fromName string, edges []EdgeTo)
	SetMetadata(id string, metadata map[string]string)
	// AddEdges records directed edges from fromID WITHOUT upserting fromID or
	// the edge targets as entities. The relationships-only path uses it to
	// anchor an edge onto an entity owned by another writer (e.g. a container
	// owned by the K8s watcher) without claiming ownership of it.
	AddEdges(fromID string, edges []EdgeTo)
}

// Graph is the read surface the query API consumes. The graph is written
// to Redis by the background flusher; this interface only reads it.
type Graph interface {
	Get(id string) (Entity, bool)
	Size() int
	Subgraph(rootID string, maxDepth int) ([]Entity, bool)
	ListByKind(kind Kind) []Entity
	Search(q string, kind Kind, limit int) []SearchHit
	Prune(cutoff time.Time) int
}

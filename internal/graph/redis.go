// SPDX-License-Identifier: Apache-2.0

package graph

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisGraph is a read-only view over the graph that the background
// flusher writes to Redis (see BatchWriteSet.WriteDeltaToRedis). It backs the
// query API; all writes happen through the flusher, not this type.
//
// Redis schema (prefix configurable, default "graph"):
//
//	<prefix>:entity:<id>            HASH  id, kind, name, last_seen_at_ms
//	<prefix>:entity:<id>:metadata   HASH  arbitrary string key/values
//	<prefix>:entity:<id>:edges      SET   JSON-encoded Edge objects
//	<prefix>:by_kind:<kind>         SET   entity IDs of the given kind
//	<prefix>:ids                    SET   all entity IDs (for global iteration)
type RedisGraph struct {
	rdb    *redis.Client
	prefix string
}

// RedisOptions configures a RedisGraph.
type RedisOptions struct {
	// KeyPrefix is the namespace for all keys. Defaults to "graph".
	KeyPrefix string
}

// NewRedis returns a read-only Graph backed by the given Redis client.
func NewRedis(rdb *redis.Client, opts RedisOptions) *RedisGraph {
	if opts.KeyPrefix == "" {
		opts.KeyPrefix = "graph"
	}
	return &RedisGraph{rdb: rdb, prefix: opts.KeyPrefix}
}

// Key helpers
func (g *RedisGraph) entityKey(id string) string   { return g.prefix + ":entity:" + id }
func (g *RedisGraph) metadataKey(id string) string { return g.prefix + ":entity:" + id + ":metadata" }
func (g *RedisGraph) edgesKey(id string) string    { return g.prefix + ":entity:" + id + ":edges" }
func (g *RedisGraph) byKindKey(k Kind) string      { return g.prefix + ":by_kind:" + string(k) }
func (g *RedisGraph) idsKey() string               { return g.prefix + ":ids" }

// Get loads a single entity with its edges and metadata.
func (g *RedisGraph) Get(id string) (Entity, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	core, err := g.rdb.HGetAll(ctx, g.entityKey(id)).Result()
	if err != nil || len(core) == 0 {
		return Entity{}, false
	}
	return g.assemble(ctx, id, core)
}

// assemble builds an Entity from its core HASH plus auxiliary keys.
func (g *RedisGraph) assemble(ctx context.Context, id string, core map[string]string) (Entity, bool) {
	e := Entity{
		ID:    core["id"],
		Kind:  Kind(core["kind"]),
		Name:  core["name"],
		Edges: []Edge{},
	}
	if ms := core["last_seen_at_ms"]; ms != "" {
		if v, err := parseInt64(ms); err == nil {
			e.LastSeenAt = time.UnixMilli(v)
		}
	}

	pipe := g.rdb.Pipeline()
	edgesCmd := pipe.SMembers(ctx, g.edgesKey(id))
	metaCmd := pipe.HGetAll(ctx, g.metadataKey(id))
	if _, err := pipe.Exec(ctx); err != nil {
		return e, true // partial result is better than no result
	}

	for _, raw := range edgesCmd.Val() {
		var edge Edge
		if err := json.Unmarshal([]byte(raw), &edge); err == nil {
			e.Edges = append(e.Edges, edge)
		}
	}
	if md := metaCmd.Val(); len(md) > 0 {
		e.Metadata = md
	}
	return e, true
}

func (g *RedisGraph) Size() int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := g.rdb.SCard(ctx, g.idsKey()).Result()
	if err != nil {
		return 0
	}
	return int(n)
}

// Subgraph runs BFS from rootID. Each hop loads the entity + edges from
// Redis. maxDepth<=0 means unbounded but capped at the total entity
// count to avoid pathological scans.
func (g *RedisGraph) Subgraph(rootID string, maxDepth int) ([]Entity, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	core, err := g.rdb.HGetAll(ctx, g.entityKey(rootID)).Result()
	if err != nil || len(core) == 0 {
		return nil, false
	}
	if maxDepth <= 0 {
		maxDepth = g.Size()
	}
	root, _ := g.assemble(ctx, rootID, core)

	visited := map[string]int{rootID: 0}
	queue := []string{rootID}
	out := []Entity{root}
	rootEdges := root.Edges

	frontier := map[string]Entity{rootID: root}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		depth := visited[id]
		if depth >= maxDepth {
			continue
		}
		entEdges := frontier[id].Edges
		if id == rootID {
			entEdges = rootEdges
		}
		for _, e := range entEdges {
			if _, seen := visited[e.ToResource]; seen {
				continue
			}
			childCore, err := g.rdb.HGetAll(ctx, g.entityKey(e.ToResource)).Result()
			if err != nil || len(childCore) == 0 {
				continue
			}
			child, _ := g.assemble(ctx, e.ToResource, childCore)
			visited[e.ToResource] = depth + 1
			queue = append(queue, e.ToResource)
			out = append(out, child)
			frontier[e.ToResource] = child
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, true
}

func (g *RedisGraph) ListByKind(kind Kind) []Entity {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ids, err := g.rdb.SMembers(ctx, g.byKindKey(kind)).Result()
	if err != nil {
		return nil
	}
	out := make([]Entity, 0, len(ids))
	for _, id := range ids {
		core, err := g.rdb.HGetAll(ctx, g.entityKey(id)).Result()
		if err != nil || len(core) == 0 {
			continue
		}
		e, _ := g.assemble(ctx, id, core)
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (g *RedisGraph) Search(q string, kind Kind, limit int) []SearchHit {
	if q == "" {
		return nil
	}
	if limit <= 0 {
		limit = 50
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	needle := strings.ToLower(q)

	var ids []string
	var err error
	if kind != "" {
		ids, err = g.rdb.SMembers(ctx, g.byKindKey(kind)).Result()
	} else {
		ids, err = g.rdb.SMembers(ctx, g.idsKey()).Result()
	}
	if err != nil {
		return nil
	}

	out := make([]SearchHit, 0)
	for _, id := range ids {
		core, err := g.rdb.HGetAll(ctx, g.entityKey(id)).Result()
		if err != nil || len(core) == 0 {
			continue
		}
		e, _ := g.assemble(ctx, id, core)
		var matched []string
		if strings.Contains(strings.ToLower(e.ID), needle) {
			matched = append(matched, "id")
		}
		if strings.Contains(strings.ToLower(e.Name), needle) {
			matched = append(matched, "name")
		}
		for k, v := range e.Metadata {
			if strings.Contains(strings.ToLower(v), needle) {
				matched = append(matched, "metadata."+k)
			}
		}
		edgeKinds := map[EdgeKind]struct{}{}
		for _, edge := range e.Edges {
			if edge.Action == "" {
				continue
			}
			if strings.Contains(strings.ToLower(edge.Action), needle) {
				edgeKinds[edge.Kind] = struct{}{}
			}
		}
		for k := range edgeKinds {
			matched = append(matched, "edge."+string(k)+".action")
		}
		if len(matched) > 0 {
			sort.Strings(matched)
			out = append(out, SearchHit{Entity: e, MatchedIn: matched})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Entity.ID < out[j].Entity.ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Prune drops every entity whose last_seen_at_ms is strictly before cutoff
// and strips dangling edges from survivors. Synchronous (not enqueued)
// because it iterates all entities and the caller almost certainly wants
// the returned count to be accurate.
func (g *RedisGraph) Prune(cutoff time.Time) int {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ids, err := g.rdb.SMembers(ctx, g.idsKey()).Result()
	if err != nil {
		return 0
	}
	cutoffMs := cutoff.UnixMilli()

	dropped := make(map[string]struct{})
	for _, id := range ids {
		ms, err := g.rdb.HGet(ctx, g.entityKey(id), "last_seen_at_ms").Result()
		if err != nil {
			continue
		}
		v, err := parseInt64(ms)
		if err != nil {
			continue
		}
		if v < cutoffMs {
			dropped[id] = struct{}{}
		}
	}
	if len(dropped) == 0 {
		return 0
	}
	for id := range dropped {
		kind, _ := g.rdb.HGet(ctx, g.entityKey(id), "kind").Result()
		pipe := g.rdb.Pipeline()
		pipe.Del(ctx, g.entityKey(id), g.metadataKey(id), g.edgesKey(id))
		pipe.SRem(ctx, g.idsKey(), id)
		if kind != "" {
			pipe.SRem(ctx, g.byKindKey(Kind(kind)), id)
		}
		_, _ = pipe.Exec(ctx)
	}
	// Strip dangling edges from survivors.
	survivors, _ := g.rdb.SMembers(ctx, g.idsKey()).Result()
	for _, sid := range survivors {
		edges, err := g.rdb.SMembers(ctx, g.edgesKey(sid)).Result()
		if err != nil {
			continue
		}
		for _, raw := range edges {
			var edge Edge
			if err := json.Unmarshal([]byte(raw), &edge); err != nil {
				continue
			}
			if _, gone := dropped[edge.ToResource]; gone {
				g.rdb.SRem(ctx, g.edgesKey(sid), raw)
			}
		}
	}
	return len(dropped)
}

func parseInt64(s string) (int64, error) {
	var v int64
	var neg bool
	if len(s) > 0 && s[0] == '-' {
		neg = true
		s = s[1:]
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, errInvalidInt
		}
		v = v*10 + int64(c-'0')
	}
	if neg {
		v = -v
	}
	return v, nil
}

var errInvalidInt = &strconvErr{"invalid integer"}

type strconvErr struct{ msg string }

func (e *strconvErr) Error() string { return e.msg }

// compile-time check that *RedisGraph satisfies Graph.
var _ Graph = (*RedisGraph)(nil)

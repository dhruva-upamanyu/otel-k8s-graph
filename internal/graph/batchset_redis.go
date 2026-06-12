// SPDX-License-Identifier: Apache-2.0

package graph

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

// Snapshot returns a deep copy of the set. The receiver takes this under its
// mutex and then flushes the copy, so the (slow) Redis write never holds the
// lock that the ingest path needs.
func (s *BatchWriteSet) Snapshot() *BatchWriteSet {
	cp := &BatchWriteSet{
		entities: make(map[string]entitySetVal, len(s.entities)),
		edges:    make(map[edgeSetKey]time.Time, len(s.edges)),
		metas:    make(map[string]map[string]string, len(s.metas)),
	}
	for id, v := range s.entities {
		cp.entities[id] = v
	}
	for k, lastSeen := range s.edges {
		cp.edges[k] = lastSeen
	}
	for id, md := range s.metas {
		m := make(map[string]string, len(md))
		for k, v := range md {
			m[k] = v
		}
		cp.metas[id] = m
	}
	return cp
}

// FlushStats reports what a delta flush changed.
type FlushStats struct {
	AddedEntities   int
	RemovedEntities int
	AddedEdges      int
	RemovedEdges    int
	Commands        int
}

// WriteDeltaToRedis writes only what changed since prev: entities/edges in s
// but not prev are added (HSET/SADD), and entities/edges in prev but not s
// are removed (DEL/SREM). Pass an empty prev (or nil) for a full write. All
// commands are plain and idempotent (no Lua, no conditional writes), chunked
// into pipelines of chunkSize.
//
// Schema (prefix configurable; mirrors the read side):
//
//	<prefix>:entity:<id>            HASH  id, kind, name, last_seen_at_ms
//	<prefix>:entity:<id>:metadata   HASH  arbitrary string key/values
//	<prefix>:entity:<id>:edges      SET   JSON-encoded Edge objects
//	<prefix>:by_kind:<kind>         SET   entity IDs of the given kind
//	<prefix>:ids                    SET   all entity IDs
func (s *BatchWriteSet) WriteDeltaToRedis(ctx context.Context, rdb *redis.Client, prefix string, chunkSize int, now time.Time, prev *BatchWriteSet) (FlushStats, error) {
	if chunkSize <= 0 {
		chunkSize = 1000
	}
	if prev == nil {
		prev = NewBatchWriteSet()
	}
	var st FlushStats
	ms := now.UnixMilli()
	idsKey := prefix + ":ids"

	pipe := rdb.Pipeline()
	n := 0
	flush := func() error {
		if n == 0 {
			return nil
		}
		_, err := pipe.Exec(ctx)
		pipe = rdb.Pipeline()
		n = 0
		return err
	}
	add := func(cmds int) error {
		n += cmds
		st.Commands += cmds
		if n >= chunkSize {
			return flush()
		}
		return nil
	}

	// Added entities: in s, not in prev.
	for id, e := range s.entities {
		if _, ok := prev.entities[id]; ok {
			continue
		}
		pipe.HSet(ctx, prefix+":entity:"+id,
			"id", id, "kind", string(e.kind), "name", e.name, "last_seen_at_ms", ms)
		pipe.SAdd(ctx, prefix+":by_kind:"+string(e.kind), id)
		pipe.SAdd(ctx, idsKey, id)
		cmds := 3
		if md := s.metas[id]; len(md) > 0 {
			pipe.HSet(ctx, prefix+":entity:"+id+":metadata", metaArgs(md)...)
			cmds++
		}
		st.AddedEntities++
		if err := add(cmds); err != nil {
			return st, err
		}
	}
	// Removed entities: in prev, not in s.
	for id, e := range prev.entities {
		if _, ok := s.entities[id]; ok {
			continue
		}
		pipe.Del(ctx, prefix+":entity:"+id, prefix+":entity:"+id+":metadata", prefix+":entity:"+id+":edges")
		pipe.SRem(ctx, idsKey, id)
		pipe.SRem(ctx, prefix+":by_kind:"+string(e.kind), id)
		st.RemovedEntities++
		if err := add(3); err != nil {
			return st, err
		}
	}
	// Added edges: in s, not in prev.
	for k := range s.edges {
		if _, ok := prev.edges[k]; ok {
			continue
		}
		edgeJSON, _ := json.Marshal(Edge{Kind: k.kind, ToResource: k.to, Action: k.action})
		pipe.SAdd(ctx, prefix+":entity:"+k.from+":edges", string(edgeJSON))
		st.AddedEdges++
		if err := add(1); err != nil {
			return st, err
		}
	}
	// Removed edges: in prev, not in s.
	for k := range prev.edges {
		if _, ok := s.edges[k]; ok {
			continue
		}
		edgeJSON, _ := json.Marshal(Edge{Kind: k.kind, ToResource: k.to, Action: k.action})
		pipe.SRem(ctx, prefix+":entity:"+k.from+":edges", string(edgeJSON))
		st.RemovedEdges++
		if err := add(1); err != nil {
			return st, err
		}
	}
	if err := flush(); err != nil {
		return st, err
	}
	return st, nil
}

// metaArgs flattens a metadata map into HSET field/value args. Values stored
// in the set are already non-empty (see SetMetadata).
func metaArgs(md map[string]string) []any {
	args := make([]any, 0, len(md)*2)
	for k, v := range md {
		args = append(args, k, v)
	}
	return args
}

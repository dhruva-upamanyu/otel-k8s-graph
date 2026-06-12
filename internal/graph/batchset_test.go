// SPDX-License-Identifier: Apache-2.0

package graph

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// AddEdges must anchor a relationship edge onto an entity (e.g. a watcher-owned
// container) without creating/owning that entity: the edge lands in the
// container's edge set, but no container entity hash or index membership is
// written. This is the core of the relationships-only model.
func TestBatchWriteSet_AddEdgesAnchorsWithoutOwningEntity(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close(); mr.Close() })

	local := NewBatchWriteSet()
	local.Upsert("endpoint:e", KindEndpoint, "e") // owned
	local.AddEdges("endpoint:e", []EdgeTo{{Kind: EdgeExposedBy, ToID: "container:c"}})
	local.AddEdges("container:c", []EdgeTo{{Kind: EdgeExposes, ToID: "endpoint:e"}}) // container NOT owned

	live := NewBatchWriteSet()
	live.Merge(local, time.Now())
	if _, err := live.WriteDeltaToRedis(context.Background(), rdb, "t", 0, time.Now(), NewBatchWriteSet()); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Endpoint IS owned: core hash + index membership written.
	if got := mr.HGet("t:entity:endpoint:e", "kind"); got != "endpoint" {
		t.Fatalf("endpoint entity not written (kind=%q)", got)
	}
	if ok, _ := mr.SIsMember("t:ids", "endpoint:e"); !ok {
		t.Fatal("endpoint not in t:ids")
	}
	// Container is NOT owned: no core hash, not in ids.
	if mr.Exists("t:entity:container:c") {
		t.Fatal("container entity must NOT be written by the relationships path")
	}
	if ok, _ := mr.SIsMember("t:ids", "container:c"); ok {
		t.Fatal("container must NOT be added to t:ids")
	}
	// But the edge IS anchored on the container's edge set (and the endpoint's).
	if n, _ := mr.SCard("t:entity:container:c:edges"); n != 1 {
		t.Fatalf("container edge set = %d, want 1 (the EXPOSES edge)", n)
	}
	if n, _ := mr.SCard("t:entity:endpoint:e:edges"); n != 1 {
		t.Fatalf("endpoint edge set = %d, want 1 (the EXPOSED_BY edge)", n)
	}
}

// The safety-critical path: when a relationship entity goes stale, the OTel
// flusher's delta must delete the relationship and SREM the forward edge off
// the container — but must NEVER delete the watcher-owned container entity.
func TestBatchWriteSet_ExpiryRemovesRelationshipNotContainer(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close(); mr.Close() })
	ctx := context.Background()
	now := time.Now()

	// A watcher-owned container already present in Redis (written by graph-k8s).
	mr.HSet("t:entity:container:c", "id", "container:c", "kind", "container", "name", "app")
	mr.SAdd("t:ids", "container:c")
	mr.SAdd("t:by_kind:container", "container:c")

	// Flush 1: an endpoint relationship appears, anchored on that container.
	s1 := NewBatchWriteSet()
	s1.Upsert("endpoint:e", KindEndpoint, "e")
	s1.AddEdges("endpoint:e", []EdgeTo{{Kind: EdgeExposedBy, ToID: "container:c"}})
	s1.AddEdges("container:c", []EdgeTo{{Kind: EdgeExposes, ToID: "endpoint:e"}})
	live1 := NewBatchWriteSet()
	live1.Merge(s1, now)
	if _, err := live1.WriteDeltaToRedis(ctx, rdb, "t", 0, now, NewBatchWriteSet()); err != nil {
		t.Fatalf("flush1: %v", err)
	}
	if n, _ := mr.SCard("t:entity:container:c:edges"); n != 1 {
		t.Fatalf("expected the EXPOSES edge on the container after flush1, got %d", n)
	}

	// Flush 2: the endpoint went stale (absent from the new set). The delta
	// vs live1 must drop the endpoint and its edges.
	empty := NewBatchWriteSet()
	if _, err := empty.WriteDeltaToRedis(ctx, rdb, "t", 0, now, live1); err != nil {
		t.Fatalf("flush2: %v", err)
	}
	if mr.Exists("t:entity:endpoint:e") {
		t.Fatal("stale endpoint entity should be deleted")
	}
	if n, _ := mr.SCard("t:entity:container:c:edges"); n != 0 {
		t.Fatalf("forward edge should be SREM'd off the container, got %d", n)
	}
	// Safety property: the OTel expiry must leave the watcher-owned container alone.
	if !mr.Exists("t:entity:container:c") {
		t.Fatal("OTel expiry must NOT delete the watcher-owned container entity")
	}
	if ok, _ := mr.SIsMember("t:ids", "container:c"); !ok {
		t.Fatal("container must remain in t:ids")
	}
}

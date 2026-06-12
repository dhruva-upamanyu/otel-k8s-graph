// SPDX-License-Identifier: Apache-2.0

package graph

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestWriter(t *testing.T) (*RedisWriter, *miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	// Large interval so only explicit Flush() drives writes in tests.
	w := NewRedisWriter(rdb, "tg", 1000, time.Hour, nil)
	t.Cleanup(func() { _ = rdb.Close(); mr.Close() })
	return w, mr, rdb
}

func TestRedisWriter_UpsertEntityWritesSchema(t *testing.T) {
	w, mr, _ := newTestWriter(t)
	w.UpsertEntity("pod:default/p1", KindPod, "p1", map[string]string{"image": "x", "blank": ""})
	w.Flush()

	if got := mr.HGet("tg:entity:pod:default/p1", "kind"); got != "pod" {
		t.Fatalf("entity kind = %q, want pod", got)
	}
	inIDs, err := mr.SIsMember("tg:ids", "pod:default/p1")
	if err != nil {
		t.Fatalf("SIsMember tg:ids: %v", err)
	}
	if !inIDs {
		t.Fatal("id not in tg:ids")
	}
	inByKind, err := mr.SIsMember("tg:by_kind:pod", "pod:default/p1")
	if err != nil {
		t.Fatalf("SIsMember tg:by_kind:pod: %v", err)
	}
	if !inByKind {
		t.Fatal("id not in tg:by_kind:pod")
	}
	if got := mr.HGet("tg:entity:pod:default/p1:metadata", "image"); got != "x" {
		t.Fatalf("metadata image = %q, want x", got)
	}
	if mr.HGet("tg:entity:pod:default/p1:metadata", "blank") != "" {
		t.Fatal("empty metadata value should be skipped")
	}
}

func TestRedisWriter_DeleteEntityRemovesSchema(t *testing.T) {
	w, mr, _ := newTestWriter(t)
	w.UpsertEntity("pod:default/p1", KindPod, "p1", nil)
	w.AddEdge("pod:default/p1", Edge{Kind: EdgeRunsIn, ToResource: "namespace:default"})
	w.Flush()

	w.DeleteEntity("pod:default/p1", KindPod)
	w.Flush()

	if mr.Exists("tg:entity:pod:default/p1") {
		t.Fatal("entity key should be deleted")
	}
	// After deletion, the set key may not exist; ErrKeyNotFound means not a member.
	inIDs, err := mr.SIsMember("tg:ids", "pod:default/p1")
	if err != nil && err != miniredis.ErrKeyNotFound {
		t.Fatalf("SIsMember tg:ids: %v", err)
	}
	if inIDs {
		t.Fatal("id should be removed from tg:ids")
	}
	inByKind, err := mr.SIsMember("tg:by_kind:pod", "pod:default/p1")
	if err != nil && err != miniredis.ErrKeyNotFound {
		t.Fatalf("SIsMember tg:by_kind:pod: %v", err)
	}
	if inByKind {
		t.Fatal("id should be removed from tg:by_kind:pod")
	}
}

func TestRedisWriter_AddRemoveEdge(t *testing.T) {
	w, mr, _ := newTestWriter(t)
	e := Edge{Kind: EdgeContains, ToResource: "pod:default/p1"}
	w.AddEdge("namespace:default", e)
	w.Flush()
	if n, _ := mr.SCard("tg:entity:namespace:default:edges"); n != 1 {
		t.Fatalf("edges = %d, want 1", n)
	}
	w.RemoveEdge("namespace:default", e)
	w.Flush()
	if n, _ := mr.SCard("tg:entity:namespace:default:edges"); n != 0 {
		t.Fatalf("edges after remove = %d, want 0", n)
	}
}

func TestRedisWriter_TickerFlushes(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close(); mr.Close() })
	w := NewRedisWriter(rdb, "tg", 1000, 20*time.Millisecond, nil)
	w.Run()
	defer w.Close()
	w.UpsertEntity("node:n1", KindNode, "n1", nil) // no explicit Flush
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if mr.HGet("tg:entity:node:n1", "name") == "n1" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("ticker did not flush within deadline")
}

func TestRedisWriter_FlushesAtBatchSize(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close(); mr.Close() })
	// batchSize 3 (one UpsertEntity = 3 commands), hour interval: the single
	// upsert should flush inline without an explicit Flush().
	w := NewRedisWriter(rdb, "tg", 3, time.Hour, nil)
	w.UpsertEntity("node:n1", KindNode, "n1", nil)
	if got := mr.HGet("tg:entity:node:n1", "name"); got != "n1" {
		t.Fatalf("expected inline flush at batch size; entity missing (got %q)", got)
	}
}

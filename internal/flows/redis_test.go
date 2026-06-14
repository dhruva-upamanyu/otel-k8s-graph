// SPDX-License-Identifier: Apache-2.0

package flows

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestWriteFlowsToRedis(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	s := NewFlowStore()
	s.Observe(flow("x"), time.Unix(1000, 0))
	snap := s.SnapshotDirty()
	if err := WriteFlowsToRedis(context.Background(), rdb, "graph", snap); err != nil {
		t.Fatalf("write: %v", err)
	}

	hash := snap[0].RootHash
	key := "graph:flow:" + hash
	if got := mr.HGet(key, "count"); got != "1" {
		t.Fatalf("count = %q, want 1", got)
	}
	if got := mr.HGet(key, "root_hash"); got != hash {
		t.Fatalf("root_hash = %q, want %q", got, hash)
	}
	if ok, _ := mr.SIsMember("graph:flow:ids", hash); !ok {
		t.Fatal("hash not in graph:flow:ids")
	}
	var node CanonicalNode
	if err := json.Unmarshal([]byte(mr.HGet(key, "structure")), &node); err != nil {
		t.Fatalf("structure JSON: %v", err)
	}
	if node.Hash != hash || len(node.Children) != 1 {
		t.Fatalf("structure round-trip wrong: %+v", node)
	}
}

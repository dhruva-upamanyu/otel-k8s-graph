// SPDX-License-Identifier: Apache-2.0

package graph

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisWriter batches graph mutations into Redis pipelines. It is the write
// path for the single-replica K8s watcher: handlers append commands, which are
// flushed when the buffer reaches batchSize (inline) or every interval (ticker).
// Safe for concurrent use (the watcher's informers fire handlers concurrently).
type RedisWriter struct {
	rdb       *redis.Client
	prefix    string
	batchSize int
	interval  time.Duration
	now       func() time.Time
	logger    *slog.Logger

	mu   sync.Mutex
	pipe redis.Pipeliner
	n    int

	stop      chan struct{}
	doneWG    sync.WaitGroup
	closeOnce sync.Once
}

// NewRedisWriter constructs a writer. batchSize<=0 defaults to 1000;
// interval<=0 defaults to 5s; nil logger defaults to slog.Default.
func NewRedisWriter(rdb *redis.Client, prefix string, batchSize int, interval time.Duration, logger *slog.Logger) *RedisWriter {
	if batchSize <= 0 {
		batchSize = 1000
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &RedisWriter{
		rdb: rdb, prefix: prefix, batchSize: batchSize, interval: interval,
		now: time.Now, logger: logger, pipe: rdb.Pipeline(), stop: make(chan struct{}),
	}
}

// Run starts the periodic flush ticker. Call Close to stop it.
func (w *RedisWriter) Run() {
	w.doneWG.Add(1)
	go func() {
		defer w.doneWG.Done()
		t := time.NewTicker(w.interval)
		defer t.Stop()
		for {
			select {
			case <-w.stop:
				return
			case <-t.C:
				w.Flush()
			}
		}
	}()
}

// Close stops the ticker and flushes any buffered commands. Safe to call multiple times.
func (w *RedisWriter) Close() {
	w.closeOnce.Do(func() {
		close(w.stop)
		w.doneWG.Wait()
		w.Flush()
	})
}

// Flush executes any buffered pipeline.
func (w *RedisWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLocked()
}

func (w *RedisWriter) flushLocked() {
	if w.n == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := w.pipe.Exec(ctx); err != nil {
		w.logger.Warn("graph-k8s: redis flush failed",
			slog.Any("err", err), slog.Int("commands", w.n))
	}
	w.pipe = w.rdb.Pipeline()
	w.n = 0
}

// add appends commands under the lock, flushing inline when full.
func (w *RedisWriter) add(cmds int, build func(pipe redis.Pipeliner)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	build(w.pipe)
	w.n += cmds
	if w.n >= w.batchSize {
		w.flushLocked()
	}
}

// UpsertEntity writes the entity core hash + index membership (+ metadata).
func (w *RedisWriter) UpsertEntity(id string, kind Kind, name string, metadata map[string]string) {
	nowMs := w.now().UnixMilli()
	metaArgs := make([]any, 0, len(metadata)*2)
	for k, v := range metadata {
		if v != "" {
			metaArgs = append(metaArgs, k, v)
		}
	}
	cmds := 3
	if len(metaArgs) > 0 {
		cmds = 4
	}
	w.add(cmds, func(pipe redis.Pipeliner) {
		pipe.HSet(context.Background(), keyEntity(w.prefix, id),
			"id", id, "kind", string(kind), "name", name, "last_seen_at_ms", nowMs)
		pipe.SAdd(context.Background(), keyByKind(w.prefix, kind), id)
		pipe.SAdd(context.Background(), keyIDs(w.prefix), id)
		if len(metaArgs) > 0 {
			pipe.HSet(context.Background(), keyMetadata(w.prefix, id), metaArgs...)
		}
	})
}

// DeleteEntity removes the entity's keys and index membership. Callers handle
// cascade (e.g. deleting a pod's containers) by calling DeleteEntity per child.
func (w *RedisWriter) DeleteEntity(id string, kind Kind) {
	w.add(3, func(pipe redis.Pipeliner) {
		pipe.Del(context.Background(), keyEntity(w.prefix, id), keyMetadata(w.prefix, id), keyEdges(w.prefix, id))
		pipe.SRem(context.Background(), keyIDs(w.prefix), id)
		pipe.SRem(context.Background(), keyByKind(w.prefix, kind), id)
	})
}

// AddEdge adds a directed edge onto the source entity's edge set.
func (w *RedisWriter) AddEdge(fromID string, e Edge) {
	j, _ := json.Marshal(e)
	w.add(1, func(pipe redis.Pipeliner) {
		pipe.SAdd(context.Background(), keyEdges(w.prefix, fromID), string(j))
	})
}

// RemoveEdge removes a directed edge from the source entity's edge set.
func (w *RedisWriter) RemoveEdge(fromID string, e Edge) {
	j, _ := json.Marshal(e)
	w.add(1, func(pipe redis.Pipeliner) {
		pipe.SRem(context.Background(), keyEdges(w.prefix, fromID), string(j))
	})
}

// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"google.golang.org/grpc"
	_ "google.golang.org/grpc/encoding/gzip" // register gzip decompressor for OTLP clients

	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/flows"
	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/graph"
	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/otlpreceiver"
)

// Command graph-otel ingests OTel trace spans over gRPC and derives the
// service-relationship graph (endpoint/topic/database entities + CALLS/
// QUERIES/PUBLISHES/EXPOSES edges) into Redis. It is a pure writer: it
// accumulates the discovered graph in an in-memory set (no Redis in the
// request path), and a background flusher periodically writes the delta to
// Redis. Raw spans are never persisted — each span is consumed to derive
// relationships and then discarded. The query API lives in graph-read; this
// binary serves only /healthz for k8s probes. Spans also feed an in-process
// flow assembler that reassembles each trace, collapses it into an abstract
// Merkle-canonicalized structure, and stores the distinct structures ("flows")
// in Redis under the <prefix>:flow:* keys.
func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	listenAddr := envOrDefault("LISTEN_ADDR", ":8080")
	grpcAddr := envOrDefault("GRPC_LISTEN_ADDR", ":4317")
	// Max OTLP message the gRPC server accepts (decompressed). gRPC's default
	// is 4 MiB; collectors batching high-volume trace spans overflow it and
	// drop items. Raise it and make it tunable.
	grpcMaxRecvMiB := envInt(logger, "GRPC_MAX_RECV_MSG_MIB", 32)

	redisHost := os.Getenv("REDIS_HOST")
	redisPort := envOrDefault("REDIS_PORT", "6379")
	redisPassword := os.Getenv("REDIS_PASSWORD")
	redisDB := envInt(logger, "REDIS_DB", 0)
	graphKeyPrefix := envOrDefault("GRAPH_REDIS_KEY_PREFIX", "graph")
	// Wait this long after startup for the graph to stabilize, then flush
	// the in-memory set to Redis every flushInterval. Each flush first expires
	// entities/edges not seen for expiryTTL (must exceed the flush interval).
	flushDelay := envDuration(logger, "GRAPH_FLUSH_DELAY", 200*time.Second)
	flushInterval := envDuration(logger, "GRAPH_FLUSH_INTERVAL", 60*time.Second)
	expiryTTL := envDuration(logger, "GRAPH_EXPIRY_TTL", 100*time.Second)
	flowWindow := envDuration(logger, "FLOW_WINDOW", 10*time.Second)
	flowGrace := envDuration(logger, "FLOW_GRACE", 3*time.Second)
	flowSweepInterval := envDuration(logger, "FLOW_SWEEP_INTERVAL", 1*time.Second)
	flowFlushInterval := envDuration(logger, "FLOW_FLUSH_INTERVAL", 30*time.Second)
	flowMaxTraces := envInt(logger, "FLOW_MAX_BUFFERED_TRACES", 100_000)

	if redisHost == "" {
		logger.Error("REDIS_HOST is required")
		os.Exit(1)
	}
	redisAddr := redisHost + ":" + redisPort

	logger.Info("graph-otel: starting",
		slog.String("listen_addr", listenAddr),
		slog.String("grpc_listen_addr", grpcAddr),
		slog.Int("grpc_max_recv_msg_mib", grpcMaxRecvMiB),
		slog.String("redis_addr", redisAddr),
		slog.Duration("flush_delay", flushDelay),
		slog.Duration("flush_interval", flushInterval),
		slog.Duration("expiry_ttl", expiryTTL),
	)

	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Username: os.Getenv("REDIS_USER"),
		Password: redisPassword,
		DB:       redisDB,
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// OTLP/gRPC TracesService receiver: the sole ingest path.
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		logger.Error("grpc listen failed", slog.Any("err", err))
		os.Exit(1)
	}
	recv := otlpreceiver.NewTrace(logger)
	flowStore := flows.NewFlowStore()
	assembler := flows.NewAssembler(flowWindow, flowGrace, flowMaxTraces, time.Now,
		func(root *flows.CanonicalNode, now time.Time) { flowStore.Observe(root, now) }, logger)
	recv.SetFlowSink(assembler.Ingest)
	grpcSrv := grpc.NewServer(grpc.MaxRecvMsgSize(grpcMaxRecvMiB * 1024 * 1024))
	ptraceotlp.RegisterGRPCServer(grpcSrv, recv)
	go func() {
		if err := grpcSrv.Serve(lis); err != nil {
			logger.Error("grpc server crashed", slog.Any("err", err))
			cancel()
		}
	}()

	// Background flusher: periodically dump the accumulated graph to Redis.
	go runFlusher(ctx, logger, recv, rdb, graphKeyPrefix, flushDelay, flushInterval, expiryTTL)
	go assembler.Run(ctx, flowSweepInterval)
	go runFlowFlusher(ctx, logger, flowStore, rdb, graphKeyPrefix, flowFlushInterval)

	// HTTP server: /healthz only (liveness/readiness probes). The query API
	// lives in graph-read; this binary just writes. /healthz is Redis-free
	// so a Redis hiccup doesn't flip the pod unready.
	healthMux := http.NewServeMux()
	healthMux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           healthMux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server crashed", slog.Any("err", err))
			cancel()
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown signal received")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown failed", slog.Any("err", err))
	}
	grpcSrv.GracefulStop()
	if rdb != nil {
		_ = rdb.Close()
	}
}

// runFlusher waits delay, then every interval expires stale entries (not seen
// for ttl) and writes only the delta vs the previous flush to Redis, timing
// each flush. Runs until ctx is canceled.
func runFlusher(ctx context.Context, logger *slog.Logger, recv *otlpreceiver.TraceServer, rdb *redis.Client, prefix string, delay, interval, ttl time.Duration) {
	logger.Info("redis flusher scheduled",
		slog.Duration("initial_delay", delay), slog.Duration("interval", interval), slog.Duration("expiry_ttl", ttl))
	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return
	}

	previous := graph.NewBatchWriteSet() // empty -> first flush writes everything
	flush := func() {
		now := time.Now()
		snap, expiredEntities, expiredEdges := recv.ExpireAndSnapshot(now.Add(-ttl))
		start := time.Now()
		fctx, fcancel := context.WithTimeout(context.Background(), 60*time.Second)
		st, err := snap.WriteDeltaToRedis(fctx, rdb, prefix, 0, now, previous)
		fcancel()
		dur := time.Since(start).Milliseconds()
		if err != nil {
			// Leave previous unchanged so the whole delta is retried next cycle.
			logger.Error("redis flush failed",
				slog.Any("err", err), slog.Int("commands", st.Commands), slog.Int64("duration_ms", dur))
			return
		}
		previous = snap
		entities, edges, _ := snap.Counts()
		logger.Info("flushed graph delta to redis",
			slog.Int("entities", entities), slog.Int("edges", edges),
			slog.Int("added_entities", st.AddedEntities), slog.Int("removed_entities", st.RemovedEntities),
			slog.Int("added_edges", st.AddedEdges), slog.Int("removed_edges", st.RemovedEdges),
			slog.Int("expired_entities", expiredEntities), slog.Int("expired_edges", expiredEdges),
			slog.Int("commands", st.Commands), slog.Int64("duration_ms", dur))
	}

	flush() // first flush at the delay mark
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			flush()
		}
	}
}

// runFlowFlusher periodically writes the changed-flows delta to Redis until ctx
// is cancelled.
func runFlowFlusher(ctx context.Context, logger *slog.Logger, store *flows.FlowStore, rdb *redis.Client, prefix string, interval time.Duration) {
	logger.Info("flow flusher scheduled", slog.Duration("interval", interval))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	flush := func() {
		snap := store.SnapshotDirty()
		if len(snap) == 0 {
			return
		}
		fctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := flows.WriteFlowsToRedis(fctx, rdb, prefix, snap)
		cancel()
		if err != nil {
			hashes := make([]string, len(snap))
			for i, f := range snap {
				hashes[i] = f.RootHash
			}
			store.RequeueDirty(hashes)
			logger.Error("flow flush failed; requeued", slog.Any("err", err), slog.Int("flows", len(snap)))
			return
		}
		logger.Info("flushed flows to redis", slog.Int("flows", len(snap)))
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			flush()
		}
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(logger *slog.Logger, key string, def int) int {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		logger.Warn("invalid int, using default", slog.String("key", key), slog.String("value", s), slog.Int("default", def))
		return def
	}
	return n
}

func envDuration(logger *slog.Logger, key string, def time.Duration) time.Duration {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		logger.Warn("invalid duration, using default", slog.String("key", key), slog.String("value", s), slog.Duration("default", def))
		return def
	}
	return d
}

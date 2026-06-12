// SPDX-License-Identifier: Apache-2.0

// Command graph-k8s watches Kubernetes structural objects and writes the
// structural graph to Redis. Single replica (one authoritative writer).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/graph"
	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/k8swatch"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	redisHost := os.Getenv("REDIS_HOST")
	if redisHost == "" {
		logger.Error("REDIS_HOST is required")
		os.Exit(1)
	}
	redisAddr := redisHost + ":" + envOr("REDIS_PORT", "6379")
	prefix := envOr("GRAPH_REDIS_KEY_PREFIX", "graph")
	resync := envDuration(logger, "WATCH_RESYNC_PERIOD", 5*time.Minute)
	batchSize := envInt(logger, "WATCH_BATCH_SIZE", 1000)
	batchInterval := envDuration(logger, "WATCH_BATCH_INTERVAL", 5*time.Second)

	logger.Info("graph-k8s: starting",
		slog.String("redis_addr", redisAddr),
		slog.Duration("resync_period", resync),
		slog.Int("batch_size", batchSize),
		slog.Duration("batch_interval", batchInterval),
	)

	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Username: os.Getenv("REDIS_USER"),
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       envInt(logger, "REDIS_DB", 0),
	})
	defer func() { _ = rdb.Close() }()

	cfg, err := rest.InClusterConfig()
	if err != nil {
		logger.Error("in-cluster config failed", slog.Any("err", err))
		os.Exit(1)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		logger.Error("kubernetes client failed", slog.Any("err", err))
		os.Exit(1)
	}

	writer := graph.NewRedisWriter(rdb, prefix, batchSize, batchInterval, logger)
	writer.Run()
	defer writer.Close()

	watcher := k8swatch.NewWatcher(client, writer, resync, logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	watcher.Run(ctx) // blocks until signal
	logger.Info("graph-k8s: shutting down")
}

func envOr(key, def string) string {
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
		logger.Warn("invalid int, using default", slog.String("key", key), slog.String("value", s))
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
		logger.Warn("invalid duration, using default", slog.String("key", key), slog.String("value", s))
		return def
	}
	return d
}

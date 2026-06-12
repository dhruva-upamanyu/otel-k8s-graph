// SPDX-License-Identifier: Apache-2.0

// Command graph-read serves the graph that graph-k8s and
// graph-otel write to Redis. It is a pure reader (it never writes) with two
// modes:
//
//	graph-read        HTTP query API (reads Redis): search/entities/entity/
//	                     subgraph/prune/healthz
//	graph-read mcp    MCP server over stdio for LLM clients; a thin REST
//	                     client of the HTTP query API (it never touches Redis)
//
// HTTP mode needs REDIS_HOST (+ REDIS_PORT/PASSWORD/DB, GRAPH_REDIS_KEY_PREFIX,
// LISTEN_ADDR). MCP mode needs GRAPH_BASE_URL (the query API endpoint,
// default http://localhost:8080).
package main

import (
	"context"
	"errors"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/redis/go-redis/v9"

	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/api"
	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/graph"
	graphmcp "github.com/dhruvaupamanyu/otel-k8s-graph/internal/mcp"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		runMCP()
		return
	}
	runHTTP(logger)
}

// runHTTP serves the REST query API (reading Redis) until a signal arrives.
func runHTTP(logger *slog.Logger) {
	host := os.Getenv("REDIS_HOST")
	if host == "" {
		logger.Error("REDIS_HOST is required")
		os.Exit(1)
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:     host + ":" + envOrDefault("REDIS_PORT", "6379"),
		Username: os.Getenv("REDIS_USER"),
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       envInt(logger, "REDIS_DB", 0),
	})
	defer func() { _ = rdb.Close() }()
	g := graph.NewRedis(rdb, graph.RedisOptions{KeyPrefix: envOrDefault("GRAPH_REDIS_KEY_PREFIX", "graph")})

	listenAddr := envOrDefault("LISTEN_ADDR", ":8080")
	logger.Info("graph-read: starting HTTP query API", slog.String("listen_addr", listenAddr))
	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           api.NewHandler(api.Deps{Graph: g, Clock: time.Now}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server crashed", slog.Any("err", err))
			cancel()
		}
	}()
	<-ctx.Done()
	logger.Info("graph-read: shutting down")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
}

// runMCP serves the MCP toolset over stdio (spawned by an MCP client). It is a
// REST client of the graph-read HTTP query API; it never touches Redis.
// Stdin close / SDK "server is closing" wrappers around EOF are normal
// client-driven shutdowns, not failures.
func runMCP() {
	client := graphmcp.NewGraphClient(os.Getenv("GRAPH_BASE_URL"))
	srv := graphmcp.New(client)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	err := srv.Run(ctx, &mcp.StdioTransport{})
	if err != nil && !errors.Is(err, io.EOF) && !strings.Contains(err.Error(), "server is closing") {
		log.Fatalf("mcp server: %v", err)
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
		logger.Warn("invalid int, using default", slog.String("key", key), slog.String("value", s))
		return def
	}
	return n
}

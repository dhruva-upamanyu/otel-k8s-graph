// SPDX-License-Identifier: Apache-2.0

// Package api wires the HTTP query surface for the graph.
//
// Routes (entity/subgraph IDs use a wildcard so namespace-qualified IDs
// like "pod:default/my-app" pass through with their slashes intact):
//
//	GET  /search?q=<text>&kind=<kind>&limit=N -> case-insensitive substring across ID, Name, Metadata, and edge actions
//	GET  /entities?kind=<kind>                -> list every entity of the given kind
//	GET  /entity/{id...}                      -> single entity with edges and metadata
//	GET  /subgraph/{id...}?max_depth=N        -> BFS reachable set from id
//	POST /prune?older_than=<duration>         -> drop stale entities (returns count)
//	GET  /healthz                             -> liveness (PINGs Redis when configured)
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/graph"
)

// Clock lets tests freeze time. Production passes time.Now.
type Clock func() time.Time

// Pinger is satisfied by *redis.Client; abstracted so /healthz can be
// tested without a real client and so the dependency stays one-way.
type Pinger interface {
	Ping(ctx context.Context) error
}

type Deps struct {
	Graph  graph.Graph
	Pinger Pinger
	Clock  Clock
}

func NewHandler(d Deps) http.Handler {
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			http.Error(w, "q query param required", http.StatusBadRequest)
			return
		}
		kind := r.URL.Query().Get("kind") // optional, empty = all kinds
		limit, ok := intParam(w, r, "limit", 50, 0, 500)
		if !ok {
			return
		}
		hits := d.Graph.Search(q, graph.Kind(kind), limit)
		writeJSON(w, http.StatusOK, map[string]any{
			"query":   q,
			"kind":    kind,
			"count":   len(hits),
			"matches": hits,
		})
	})

	mux.HandleFunc("GET /entities", func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		if kind == "" {
			http.Error(w, "kind query param required (e.g. ?kind=endpoint)", http.StatusBadRequest)
			return
		}
		entities := d.Graph.ListByKind(graph.Kind(kind))
		writeJSON(w, http.StatusOK, map[string]any{
			"kind":     kind,
			"count":    len(entities),
			"entities": entities,
		})
	})

	mux.HandleFunc("GET /entity/{id...}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		e, ok := d.Graph.Get(id)
		if !ok {
			http.Error(w, "no such entity", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, e)
	})

	mux.HandleFunc("GET /subgraph/{id...}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		depth, ok := intParam(w, r, "max_depth", 0, 0, 100)
		if !ok {
			return
		}
		entities, found := d.Graph.Subgraph(id, depth)
		if !found {
			http.Error(w, "no such entity", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"root":     id,
			"entities": entities,
		})
	})

	mux.HandleFunc("POST /prune", func(w http.ResponseWriter, r *http.Request) {
		olderThan := r.URL.Query().Get("older_than")
		if olderThan == "" {
			http.Error(w, "older_than query param required (e.g. 5m, 1h)", http.StatusBadRequest)
			return
		}
		dur, err := time.ParseDuration(olderThan)
		if err != nil || dur <= 0 {
			http.Error(w, "older_than must be a positive duration", http.StatusBadRequest)
			return
		}
		cutoff := clock().Add(-dur)
		dropped := d.Graph.Prune(cutoff)
		writeJSON(w, http.StatusOK, map[string]any{
			"dropped":    dropped,
			"cutoff":     cutoff,
			"graph_size": d.Graph.Size(),
		})
	})

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if d.Pinger != nil {
			if err := d.Pinger.Ping(r.Context()); err != nil {
				http.Error(w, "redis unreachable: "+err.Error(), http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	return mux
}

func intParam(w http.ResponseWriter, r *http.Request, key string, def, min, max int) (int, bool) {
	s := r.URL.Query().Get(key)
	if s == "" {
		return def, true
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < min || (max > 0 && n > max) {
		http.Error(w, key+" must be an integer in valid range", http.StatusBadRequest)
		return 0, false
	}
	return n, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		http.Error(w, "marshal failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

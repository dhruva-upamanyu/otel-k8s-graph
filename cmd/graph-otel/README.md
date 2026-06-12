# graph-otel

Ingests **OTel span metrics** over OTLP/gRPC and writes the **span metric derived
relationships and entities** to Redis: `endpoint`, `topic`, `database` entities and
the `EXPOSES`/`CALLS`/`PUBLISHES`/`CONSUMES`/`QUERIES` edges between services and
those resources.


## Data flow

```
OTLP/gRPC  ->  receiver  ->  in-memory BatchWriteSet  ->  background flusher  ->  Redis
(span        (derive       (deduped sets of            (delta vs previous,
 metrics)     relationships) entities + edges,           every interval)
                            stamped last-seen)
```

1. **Receive.** Implements the OTLP `MetricsService`. Only datapoint/resource
   *attributes* are read; the metric values are discarded (graph derivation depends on
   attributes alone). gRPC decodes the request before the handler, so the hot
   path is attribute extraction.
2. **Derive.** For each record it finds the emitting container/pod (the edge
   anchor) and derives any HTTP endpoint, messaging topic, or database it
   touches — templatizing high-cardinality values (e.g. `/orders/123` →
   `/orders/{n}`) so they collapse to one edge.
3. **Accumulate.** Relationships are merged into an in-memory set keyed by
   identity, so re-deriving the same series each scrape is a no-op. Every entry
   carries a last-seen timestamp.
4. **Flush.** After `GRAPH_FLUSH_DELAY`, a background goroutine expires
   entries not seen within `GRAPH_EXPIRY_TTL`, then writes only the delta vs
   the previous flush to Redis every `GRAPH_FLUSH_INTERVAL`. Expiring an
   endpoint removes the relationship entity and the edge off the container — but
   never the container.

## Configuration

| Env | Default | Meaning |
|-----|---------|---------|
| `REDIS_HOST` | _(required)_ | Redis host |
| `REDIS_PORT` / `REDIS_USER` / `REDIS_PASSWORD` / `REDIS_DB` | `6379` / – / – / `0` | Redis connection |
| `GRAPH_REDIS_KEY_PREFIX` | `graph` | Redis key prefix (must match the other components) |
| `LISTEN_ADDR` | `:8080` | HTTP bind; serves `/healthz` only |
| `GRPC_LISTEN_ADDR` | `:4317` | OTLP/gRPC bind (collector pushes here) |
| `GRPC_MAX_RECV_MSG_MIB` | `32` | Max decompressed OTLP message size (MiB) |
| `GRAPH_FLUSH_DELAY` | `200s` | Stabilization wait before the first flush |
| `GRAPH_FLUSH_INTERVAL` | `60s` | Flush cadence thereafter |
| `GRAPH_EXPIRY_TTL` | `100s` | Drop entries not seen for this long (must exceed the flush interval) |

## Collector setup

Point your OpenTelemetry Collector's **metrics** exporter (the spanmetrics
output) at the OTLP service:

```yaml
exporters:
  otlp/graph:
    endpoint: graph-otel-otlp:4317
    tls: { insecure: true }
```

The richer the span attributes (`http.route`, `db.system`, `server.address`,
`service.name`, the k8s.* resource attrs), the more relationships are derived.

## Build & run

```bash
go build ./cmd/graph-otel
REDIS_HOST=localhost ./graph-otel
```

Deploy via the [Helm chart](../../helm/graph/README.md).

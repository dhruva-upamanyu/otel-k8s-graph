# otel-k8s-graph

A live **relationship graph** of a Kubernetes cluster: what runs where, and
which service talks to what. Built from two sources, queryable by humans,
scripts, and LLM agents.

- **Cluster structure** — namespaces, nodes, deployments, pods, containers and
  how they contain/manage each other — from the **Kubernetes API**.
- **Service relationships** — which service calls which HTTP endpoint, queries
  which database, publishes to which topic — from **OTel span metrics**.

The result is one graph you can ask things like *"what services call
`/v1/checkout`?"*, *"how many upstream dependencies does this deployment
have?"*, or *"which services talk to the `mysql` database at `10.0.0.0`?"* —
over REST, or from Claude/any MCP client.

## Architecture

```
  Kubernetes API ──watch──────>  graph-k8s ──┐
   (pods, nodes, ...)           (structure)  │
                                             ├──>  Redis  <──reads──  graph-read ──> REST API
  OTel Collector ──OTLP/gRPC──> graph-otel ──┘    (graph)                        └─> MCP server
   (span metrics)              (relationships)                                       (graph-read mcp)
```

Three small Go binaries, each with one job, coordinating only through Redis:

| Component | Source | Role | Docs |
|-----------|--------|------|------|
| **graph-k8s** | Kubernetes API | Writes structural entities + containment/management edges. Single writer. | [cmd/graph-k8s](cmd/graph-k8s/README.md) |
| **graph-otel** | OTel span metrics | Writes relationship entities + CALLS/QUERIES/PUBLISHES/EXPOSES edges. | [cmd/graph-otel](cmd/graph-otel/README.md) |
| **graph-read** | Redis | Serves the read/query HTTP API; also the MCP server (`graph-read mcp`). | [cmd/graph-read](cmd/graph-read/README.md) |

## Quickstart

The Helm chart bundles a single-replica Redis by default, so a fresh install
is self-contained:

```bash
# 1. Build + push the three images (versioned + latest) and install the chart:
REGISTRY=<your-registry> ./deploy.sh

# 2. Point your OTel Collector's span-metrics exporter at graph-otel:
#      endpoint: graph-otel-otlp:4317
```

Already have Redis? `--set redis.internal.enabled=false --set redis.host=...`.
See the [chart README](helm/graph/README.md) for every value, including
`existingSecret` for credentials and persistence for the bundled Redis.

## The graph

**Entity kinds:** `namespace`, `node`, `deployment`, `pod`, `container`
(K8s derived); `endpoint`, `topic`, `database` (span metrics derived).

**Edge kinds:** `CONTAINS`/`RUNS_IN`, `MANAGES`/`MANAGED_BY` (K8s derived);
`EXPOSES`/`EXPOSED_BY`, `CALLS`/`CALLED_BY`, `PUBLISHES`/`PUBLISHED_BY`,
`CONSUMES`/`CONSUMED_BY`, `QUERIES`/`QUERIED_BY` (span metrics derived). Edges
are single-directional but have a counterpart edge in the store, and `QUERIES`
edges carry an `action` (the SQL/command).

**Entity IDs** are namespace-qualified where applicable: `pod:<ns>/<name>`,
`container:<ns>/<pod>/<name>`, `endpoint:<service>/<METHOD>/<route>`,
`database:<system>/<host>[:<port>]`, `topic:<name>`.

### Redis schema (prefix configurable, default `graph`)

```
<prefix>:entity:<id>            HASH  id, kind, name, last_seen_at_ms
<prefix>:entity:<id>:metadata   HASH  arbitrary string key/values
<prefix>:entity:<id>:edges      SET   JSON-encoded Edge objects
<prefix>:by_kind:<kind>         SET   entity IDs of the given kind
<prefix>:ids                    SET   all entity IDs
```

## Querying

- **REST** (graph-read): `GET /search`, `/entities`, `/entity/{id}`,
  `/subgraph/{id}`, `POST /prune`, `GET /healthz`.
- **MCP** (`graph-read mcp`): the tools `search`, `get_entity`,
  `list_entities`, `get_subgraph` for LLM clients (Claude Code, Claude
  Desktop, …). See [cmd/graph-read](cmd/graph-read/README.md).

## Repository layout

```
cmd/graph-k8s/       K8s watcher binary
cmd/graph-otel/      OTLP ingest binary
cmd/graph-read/      read API + MCP binary
internal/k8swatch/   informer wiring + object->entity mapping + diff/apply
internal/otlpreceiver/  OTLP MetricsService server
internal/builder/    span record -> relationship entities/edges
internal/graph/      Redis read (RedisGraph) + write (RedisWriter, BatchWriteSet) + keys/schema
internal/api/        HTTP query handlers
internal/mcp/        MCP server + tools
helm/graph/          Helm chart (+ generated README)
```

## Development

```bash
go build ./...
go test ./...
go test -race ./internal/graph/ ./internal/k8swatch/
```

Requires Go 1.25+. Tests use [miniredis](https://github.com/alicebob/miniredis)
and the client-go fake clientset, so no real Redis or cluster is needed.

See [CONTRIBUTING.md](CONTRIBUTING.md) for how to contribute.

## License

Apache License 2.0 — see [LICENSE](LICENSE).

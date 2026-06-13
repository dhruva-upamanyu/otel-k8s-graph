# otel-k8s-graph

A live **relationship graph** of a Kubernetes cluster: what runs where, and
which service talks to what. Built from two sources, queryable by humans,
scripts, and LLM agents.

- **Cluster structure** — namespaces, nodes, zones, regions, workloads
  (deployments, statefulsets, daemonsets, jobs, rollouts, …), autoscalers
  (HPA, KEDA), pods and containers, and how they contain/manage/scale each
  other — from the **Kubernetes API**.
- **Service relationships** — which service calls which HTTP endpoint, queries
  which database, publishes to which topic — from **OTel span metrics**.

The result is one graph you can ask things like 
*"what pods call `/v1/checkout` vs what pods call `/v2/checkout` (can i safely deprecate v1)?"*,
*"how many upstream dependencies does this deployment have? - (what services are impacted by this deployment)"*, 
*"how many apis are there that are exposed but dont have callers??- (find dead code)"*, 
*"which services talk to the `mysql` database at `10.0.0.0`?"* —
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
REGISTRY=<your-registry> ./deploy.sh # add a registry here that your k8s cluster can access,
#                                      the script will build and push the image,
#                                      update the helm chart and run the helm install command 

# 2. Point your OTel Collector's span-metrics exporter at graph-otel:
#      endpoint: graph-otel-otlp:4317
```

Already have Redis? `--set redis.internal.enabled=false --set redis.host=...`.
See the [chart README](helm/graph/README.md) for every value, including
`existingSecret` for credentials and persistence for the bundled Redis.

### Sample values: self-contained install

The chart needs only a registry; everything else has working defaults
(bundled Redis included):

```yaml
# graph-values.yaml
image:
  registry: <your-registry>   # e.g. ghcr.io/<you>

# Optional: keep the graph across Redis restarts (default: rebuilt from
# the K8s API and OTel metrics, so persistence is off).
# redis:
#   internal:
#     persistence:
#       enabled: true
#       size: 1Gi
```

```bash
helm upgrade --install graph helm/graph -f graph-values.yaml
```

### Sample values: OTel Collector feeding span metrics

graph-otel consumes **span metrics**, not raw traces. If you don't already
produce them, the [spanmetrics connector](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/connector/spanmetricsconnector)
can derive them from your traces. Sample values for the upstream
[opentelemetry-collector chart](https://github.com/open-telemetry/opentelemetry-helm-charts/tree/main/charts/opentelemetry-collector):

```yaml
# otel-collector-values.yaml
mode: deployment

image:
  repository: ghcr.io/open-telemetry/opentelemetry-collector-releases/opentelemetry-collector-contrib

# Adds the k8sattributes processor (+ RBAC) to every pipeline, so spans —
# and therefore the span metrics — carry k8s.namespace.name / k8s.pod.name /
# k8s.container.name. graph-otel needs these to attach relationships to the
# right container.
presets:
  kubernetesAttributes:
    enabled: true

config:
  connectors:
    spanmetrics:
      histogram:
        explicit:
          buckets: [5ms, 10ms, 25ms, 50ms, 100ms, 250ms, 500ms, 1s, 2s, 5s]
      # The dimensions graph-otel reads to derive endpoints, databases and
      # topics. (service.name, span.name and span.kind are included by
      # default.)
      dimensions:
        - name: http.request.method
        - name: http.route
        - name: url.full        # client-side fallback when http.route is absent;
                                # digits are templatized, but drop it if your URLs
                                # are high-cardinality
        - name: peer.service
        - name: server.address
        - name: server.port
        - name: db.system
      metrics_flush_interval: 15s

  exporters:
    otlp/graph:
      # <service>.<namespace>: adjust if you installed the chart elsewhere
      endpoint: graph-otel-otlp.default.svc.cluster.local:4317
      tls:
        insecure: true

  service:
    pipelines:
      # Traces in (apps send OTLP to this collector) -> span metrics out.
      traces:
        receivers: [otlp]
        processors: [memory_limiter, batch]
        exporters: [spanmetrics]
      metrics/graph:
        receivers: [spanmetrics]
        processors: [batch]
        exporters: [otlp/graph]
```

```bash
helm repo add open-telemetry https://open-telemetry.github.io/opentelemetry-helm-charts
helm upgrade --install otel-collector open-telemetry/opentelemetry-collector \
  -f otel-collector-values.yaml
```

Already producing span metrics? Just add the `otlp/graph` exporter to your
existing metrics pipeline.

## The graph

**Entity kinds:** `namespace`, `node`, `zone`, `region`, `deployment`, `statefulset`, `daemonset`, `job`, `cronjob`, `rollout`, `pod`, `container`, `hpa`, `scaledobject` (K8s derived); `endpoint`, `topic`, `database` (span metrics derived).

**Edge kinds:** `CONTAINS`/`RUNS_IN`, `MANAGES`/`MANAGED_BY`, `SCALES`/`SCALED_BY` (K8s derived);
`EXPOSES`/`EXPOSED_BY`, `CALLS`/`CALLED_BY`, `PUBLISHES`/`PUBLISHED_BY`,
`CONSUMES`/`CONSUMED_BY`, `QUERIES`/`QUERIED_BY` (span metrics derived). Edges
are single-directional but have a counterpart edge in the store, and `QUERIES`
edges carry an `action` (the SQL/command).

**Entity IDs** are namespace-qualified where applicable: `pod:<ns>/<name>`,
`container:<ns>/<pod>/<name>`, `endpoint:<service>/<METHOD>/<route>`,
`database:<system>/<host>[:<port>]`, `topic:<name>`.

**Zones & regions.** Nodes carrying the well-known `topology.kubernetes.io/zone` / `region` labels (or their legacy `failure-domain.beta.kubernetes.io` forms) produce `zone` and `region` entities: `region CONTAINS zone CONTAINS node`. Cross-zone questions — *"which services call auth-service from another zone?"* — become short graph walks: pod → node → zone on each side of a `CALLS` edge.

**Workloads & autoscalers.** Beyond deployments, graph-k8s models `statefulset`, `daemonset`, `job`/`cronjob`, and Argo `rollout` (each `MANAGES` its pods), plus `hpa` and KEDA `scaledobject` autoscalers that `SCALES` a target workload. HPA replica bounds, KEDA triggers/scaling policy, and cron schedules are captured as metadata. The Argo and KEDA resources are CRDs — graph-k8s detects their absence and skips them, so it runs anywhere.

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

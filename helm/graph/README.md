# graph

Kubernetes + OTel relationship graph: graph-k8s (K8s API -> Redis structure writer), graph-otel (OTel trace spans -> Redis relationship writer), and graph-read (Redis -> HTTP query API; also the MCP server when run locally).

![Version: 0.1.0](https://img.shields.io/badge/Version-0.1.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.1.0](https://img.shields.io/badge/AppVersion-0.1.0-informational?style=flat-square)

This chart deploys the three graph components:

| Component | Role |
|-----------|------|
| **graph-k8s** | Watches the Kubernetes API and writes cluster structure (namespace/node/zone/region/deployment/statefulset/daemonset/job/cronjob/rollout/pod/container + hpa/scaledobject autoscalers) to Redis. Single authoritative writer. |
| **graph-otel** | Ingests OTel trace spans over gRPC and writes service relationships (endpoint/topic/database + CALLS/QUERIES/PUBLISHES/EXPOSES) to Redis. |
| **graph-read** | Serves the read/query HTTP API from Redis. The same image also runs the MCP server locally as `graph-read mcp`. |

## Installing

The chart bundles a single-replica Redis by default, so this is all you need:

```bash
# Build + push all three images (versioned + latest), then helm upgrade --install:
REGISTRY=<your-registry> ./deploy.sh

# Or with helm directly against pre-built images:
helm upgrade --install graph helm/graph \
  --namespace default --create-namespace \
  --set image.registry=<your-registry>
```

`image.registry` is required. The default tag is `latest`; deploy.sh pins the
exact version at install time. Disable a component with
`--set graphRead.enabled=false`.

## Redis

Bundled (default): a single-replica Redis StatefulSet, no auth, ClusterIP
only. Enable persistence with `--set redis.internal.persistence.enabled=true`.
For HA or production use, bring your own Redis:

Inline:

```bash
helm ... --set redis.internal.enabled=false \
  --set redis.host=my-redis --set redis.password=s3cret
```

From an existing Secret — sources `REDIS_HOST` / `REDIS_USER` / `REDIS_PASSWORD`
via `secretKeyRef` (user/password optional):

```bash
kubectl create secret generic redis-creds \
  --from-literal=REDIS_HOST=my-redis \
  --from-literal=REDIS_PASSWORD=s3cret
helm ... --set redis.internal.enabled=false --set redis.existingSecret=redis-creds
```

## Exposing the API (Ingress)

Off by default. **The query API has no authentication and includes the
destructive `POST /prune`** — put auth in front (e.g. ingress basic-auth or
oauth2-proxy annotations) or restrict source IPs before exposing it.

```bash
helm ... \
  --set graphRead.ingress.enabled=true \
  --set graphRead.ingress.className=nginx \
  --set graphRead.ingress.hosts[0].host=graph.example.com \
  --set graphRead.ingress.hosts[0].paths[0].path=/ \
  --set graphRead.ingress.hosts[0].paths[0].pathType=Prefix
```

Once exposed, the MCP server can run anywhere:

```bash
GRAPH_BASE_URL=https://graph.example.com graph-read mcp
```

## MCP server

`graph-read` doubles as the MCP server when run locally (not in-cluster):

```bash
graph-read mcp   # stdio MCP; set GRAPH_BASE_URL to the query API endpoint
```

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| graph.keyPrefix | string | `"graph"` | `GRAPH_REDIS_KEY_PREFIX` namespacing every Redis key. All three components MUST agree on this to share one graph. |
| graphK8s.config.batchInterval | string | `"5s"` | `WATCH_BATCH_INTERVAL`: flush partial batches at least this often. |
| graphK8s.config.batchSize | int | `1000` | `WATCH_BATCH_SIZE`: flush the Redis pipeline once this many commands are buffered (the burst path during initial sync). |
| graphK8s.config.resyncPeriod | string | `"5m"` | `WATCH_RESYNC_PERIOD`: how often informers re-list and re-deliver every object (self-heal). Lower = faster correction, more API load. |
| graphK8s.enabled | bool | `true` | Render the graph-k8s component. |
| graphK8s.imageName | string | `"graph-k8s"` | Image repository name under `image.registry`. |
| graphK8s.resources | object | `{"limits":{"cpu":"100m","memory":"512Mi"},"requests":{"cpu":"100m","memory":"512Mi"}}` | CPU/memory requests and limits. Memory is sized for the informer cache; the limit allows for the startup LIST peak (~50-100k pods). |
| graphOtel.config.expiryTtl | string | `"100s"` | `GRAPH_EXPIRY_TTL`: drop entities/edges not seen for this long. MUST exceed flushInterval. |
| graphOtel.config.flushDelay | string | `"200s"` | `GRAPH_FLUSH_DELAY`: wait this long after startup for the in-memory graph to stabilize before the first Redis flush. |
| graphOtel.config.flushInterval | string | `"60s"` | `GRAPH_FLUSH_INTERVAL`: flush the delta to Redis this often thereafter. |
| graphOtel.config.grpcListenAddr | string | `":4317"` | `GRPC_LISTEN_ADDR`: OTLP/gRPC bind address (collector pushes spans here). |
| graphOtel.config.grpcMaxRecvMsgMib | int | `32` | `GRPC_MAX_RECV_MSG_MIB`: max decompressed OTLP message size (MiB). Raise if collectors batch high-cardinality metrics past gRPC's 4 MiB default. |
| graphOtel.config.listenAddr | string | `":8080"` | `LISTEN_ADDR`: HTTP bind address; serves /healthz only (probes). |
| graphOtel.enabled | bool | `true` | Render the graph-otel component. |
| graphOtel.imageName | string | `"graph-otel"` | Image repository name under `image.registry`. |
| graphOtel.resources | object | `{"limits":{"cpu":"200m","memory":"512Mi"},"requests":{"cpu":"200m","memory":"512Mi"}}` | CPU/memory requests and limits. |
| graphOtel.service.otlpPort | int | `4317` | OTLP gRPC port. Point the collector's traces exporter at graph-otel-otlp:<otlpPort>. |
| graphOtel.service.type | string | `"ClusterIP"` | Service type for the OTLP ingest Service (graph-otel-otlp). |
| graphRead.config.listenAddr | string | `":8080"` | `LISTEN_ADDR`: HTTP bind address for the query API. |
| graphRead.enabled | bool | `true` | Render the graph-read component. |
| graphRead.imageName | string | `"graph-read"` | Image repository name under `image.registry`. |
| graphRead.ingress.annotations | object | `{}` | Annotations for the Ingress (cert-manager, auth, rate limits, ...). |
| graphRead.ingress.className | string | `""` | IngressClass name (e.g. nginx). Empty = the cluster default. |
| graphRead.ingress.enabled | bool | `false` | Expose the query API via an Ingress. The API has NO auth and includes the destructive POST /prune — add auth at the ingress (e.g. basic-auth / oauth2-proxy annotations) or restrict source IPs before exposing it. |
| graphRead.ingress.hosts | list | `[{"host":"graph.example.com","paths":[{"path":"/","pathType":"Prefix"}]}]` | Hostnames and paths routed to the graph-read Service. |
| graphRead.ingress.tls | list | `[]` | Ingress TLS configuration. |
| graphRead.replicas | int | `1` | Replica count (stateless reader; safe to raise). |
| graphRead.resources | object | `{"limits":{"cpu":"500m","memory":"512Mi"},"requests":{"cpu":"100m","memory":"256Mi"}}` | CPU/memory requests and limits. |
| graphRead.service.port | int | `8080` | Service port for the query API. |
| graphRead.service.type | string | `"ClusterIP"` | Service type for the query API. |
| image.pullPolicy | string | `"Always"` | imagePullPolicy applied to every component. `Always` because the default tag is a moving target. |
| image.registry | string | `""` | Container registry for all images, no trailing slash; REQUIRED (no default). The image ref is `<registry>/<app imageName>:<tag>`. |
| image.tag | string | `"latest"` | Shared image tag for all three components. `latest` tracks the most recent deploy.sh build; deploy.sh pins the exact version at install time. |
| redis.db | int | `0` | `REDIS_DB` logical database index. Always inline. |
| redis.existingSecret | string | `""` | Name of an existing Secret to source host/username/password from (via secretKeyRef; only when `internal.enabled` is false). |
| redis.host | string | `""` | `REDIS_HOST` for external Redis (used only when `internal.enabled` is false and `existingSecret` is empty). |
| redis.internal.enabled | bool | `true` | Deploy a bundled single-replica Redis and use it (ignores host/username/password/existingSecret below). |
| redis.internal.image | string | `"redis:8-alpine"` | Image for the bundled Redis. |
| redis.internal.persistence.enabled | bool | `false` | Persist Redis data in a PVC. Off by default: the graph is fully rebuilt from the Kubernetes API and OTel metrics after a restart. |
| redis.internal.persistence.size | string | `"1Gi"` | PVC size when persistence is enabled. |
| redis.internal.persistence.storageClassName | string | `""` | StorageClass for the PVC. Empty = cluster default. |
| redis.internal.resources | object | `{"limits":{"cpu":"500m","memory":"1Gi"},"requests":{"cpu":"100m","memory":"256Mi"}}` | CPU/memory requests and limits for the bundled Redis. |
| redis.password | string | `""` | `REDIS_PASSWORD` for external Redis. Empty = no auth. |
| redis.port | string | `"6379"` | `REDIS_PORT`. Always inline. |
| redis.secretKeys | object | `{"host":"REDIS_HOST","password":"REDIS_PASSWORD","username":"REDIS_USER"}` | Keys within `existingSecret` for host/username/password (used only when `existingSecret` is set; username/password are optional). |
| redis.username | string | `""` | `REDIS_USER` (Redis ACL user) for external Redis. Empty = default user. |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.14.2](https://github.com/norwoodj/helm-docs/releases/v1.14.2)

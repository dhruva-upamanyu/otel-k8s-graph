# graph-k8s

Watches the Kubernetes API and writes the **k8s derived** graph to Redis:
`namespace`, `node`, `deployment`, `pod`, `container` entities and the
`CONTAINS`/`RUNS_IN`/`MANAGES`/`MANAGED_BY` edges between them.

It is the **data source** for k8s derived entities — it creates and deletes
these entities based only on Kubernetes events.

## How it works

1. **Informers.** Uses client-go `SharedInformerFactory` to watch pods, nodes,
   namespaces, deployments, and replicasets. Each informer keeps an in-memory
   cache and delivers add/update/delete events. This is a long-lived process
   (one replica), not a polling loop.
2. **Map.** A pure mapper turns each object into a `Desired` set of
   entities + edges (e.g. a pod yields its pod and container entities, plus
   namespace/node/deployment containment edges).
3. **Diff & apply.** On update it diffs old vs new `Desired` and emits only the
   delta. It deletes only entities it *owns* (the pod and its containers) — a
   pod moving off a node removes the *edge*, never the live node.
4. **Pod → Deployment.** Resolved by walking ownerRefs: pod → ReplicaSet →
   Deployment, via the ReplicaSet lister. The pod handler is registered only
   after caches sync, so the ReplicaSet cache is populated and the
   `MANAGES` edge is reliable on the very first events (client-go replays
   existing pods as adds to a late-registered handler).
5. **Write-through.** Handlers write to a batched Redis pipeline (flush on
   `WATCH_BATCH_SIZE` commands or every `WATCH_BATCH_INTERVAL`).
6. **Self-heal.** `WATCH_RESYNC_PERIOD` re-delivers every object periodically so
   any missed event is corrected.

## Configuration

| Env | Default | Meaning |
|-----|---------|---------|
| `REDIS_HOST` | _(required)_ | Redis host |
| `REDIS_PORT` | `6379` | Redis port |
| `REDIS_USER` | _(none)_ | Redis ACL user |
| `REDIS_PASSWORD` | _(none)_ | Redis password |
| `REDIS_DB` | `0` | Redis logical DB |
| `GRAPH_REDIS_KEY_PREFIX` | `graph` | Redis key prefix (must match the other components) |
| `WATCH_RESYNC_PERIOD` | `5m` | Informer resync / self-heal interval |
| `WATCH_BATCH_SIZE` | `1000` | Flush the Redis pipeline at this many commands |
| `WATCH_BATCH_INTERVAL` | `5s` | Flush partial batches at least this often |

## RBAC

Needs cluster-wide read access (`get`/`list`/`watch`) to: `pods`, `nodes`,
`namespaces` (core) and `deployments`, `replicasets` (apps). The Helm chart
ships the ServiceAccount + ClusterRole + ClusterRoleBinding.


## Build & run

```bash
go build ./cmd/graph-k8s
REDIS_HOST=localhost ./graph-k8s   # needs in-cluster config or a kubeconfig
```

In-cluster it reads its ServiceAccount via `rest.InClusterConfig()`. Deploy via
the [Helm chart](../../helm/graph/README.md).

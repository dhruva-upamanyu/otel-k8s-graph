# graph-read

The **read surface** for the graph. It is a pure reader — it never
writes Redis — with two modes that share one `RedisGraph` read core:

| Mode | Command | Transport | Use |
|------|---------|-----------|-----|
| HTTP query API | `graph-read` | HTTP (`:8080`) | UIs, scripts, any REST client. Runs in-cluster. |
| MCP server | `graph-read mcp` | stdio | LLM clients (Claude Code/Desktop). Run locally; calls the HTTP API. |

The MCP mode is a thin **REST client** of the HTTP API (set `GRAPH_BASE_URL`);
it does not talk to Redis itself, so the query logic lives in one place.

## HTTP query API

| Method & path | Description |
|---------------|-------------|
| `GET /search?q=<text>&kind=<kind>&limit=N` | Case-insensitive substring across IDs, names, metadata values, and edge actions |
| `GET /entities?kind=<kind>` | List every entity of a kind |
| `GET /entity/{id...}` | One entity with its edges + metadata (IDs may contain `/`) |
| `GET /subgraph/{id...}?max_depth=N` | BFS reachable set from an entity (blast radius / call neighborhood) |
| `POST /prune?older_than=<dur>` | Drop entities not seen for `<dur>` (e.g. `5m`); returns the count |
| `GET /healthz` | Liveness/readiness |

## MCP server

Exposes four tools to an LLM client: `search`, `get_entity`, `list_entities`,
`get_subgraph` (mirroring the REST endpoints), plus an instructions block that
teaches the model the graph vocabulary. Each tool call is forwarded to the HTTP
API and the JSON is returned verbatim.

Wire it into an MCP client by pointing the client at the binary in `mcp` mode.
For a cluster-hosted query API, port-forward it first:

```bash
kubectl port-forward svc/graph-read 8080:8080
```

```json
{
  "mcpServers": {
    "graph": {
      "command": "graph-read",
      "args": ["mcp"],
      "env": { "GRAPH_BASE_URL": "http://localhost:8080" }
    }
  }
}
```

(Or run it from the image: `docker run -i --rm -e GRAPH_BASE_URL=... <image> mcp`.)

## Configuration

**HTTP mode:**

| Env | Default | Meaning |
|-----|---------|---------|
| `REDIS_HOST` | _(required)_ | Redis host |
| `REDIS_PORT` / `REDIS_USER` / `REDIS_PASSWORD` / `REDIS_DB` | `6379` / – / – / `0` | Redis connection |
| `GRAPH_REDIS_KEY_PREFIX` | `graph` | Redis key prefix (must match the writers) |
| `LISTEN_ADDR` | `:8080` | HTTP bind address |

**MCP mode:**

| Env | Default | Meaning |
|-----|---------|---------|
| `GRAPH_BASE_URL` | `http://localhost:8080` | The HTTP query API endpoint to call |

## Scale

Stateless, so `graphRead.replicas` may be raised freely.

## Build & run

```bash
go build ./cmd/graph-read
REDIS_HOST=localhost ./graph-read          # HTTP query API
GRAPH_BASE_URL=http://localhost:8080 ./graph-read mcp   # MCP server
```

Deploy the HTTP mode via the [Helm chart](../../helm/graph/README.md).

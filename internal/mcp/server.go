// SPDX-License-Identifier: Apache-2.0

// Package mcp implements the Model Context Protocol server that exposes the
// graph as a typed toolset for LLM clients. The tool handlers call
// the graph-read HTTP query API (via GraphClient); they never touch
// Redis directly.
package mcp

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// instructions are surfaced to the LLM via MCP's initialize result so the
// model understands the graph vocabulary before reaching for any tool.
const instructions = `You are exploring Kubernetes microservices based environment using a graph built from Kubernetes API state (cluster structure) and OTel span metrics (service relationships).

The graph has these entity kinds:
- namespace, node, zone, region — cluster structure
- deployment, statefulset, daemonset, job, cronjob, rollout, pod, container — workloads
- hpa, scaledobject — autoscalers
- endpoint — an HTTP route served by some service (id: endpoint:<service>/<METHOD>/<route>)
- topic — a messaging destination (id: topic:<name>)
- database — a queried datastore (id: database:<system>/<host>[:<port>])

Edge kinds and their meaning:
- CONTAINS / RUNS_IN — structural hierarchy (namespace contains pod, pod contains container, node contains pod, zone contains node, region contains zone)
- MANAGES / MANAGED_BY — controller owns workload/pods (deployment, statefulset, daemonset, job, rollout -> pods; cronjob -> jobs; scaledobject -> its keda-owned hpa)
- SCALES / SCALED_BY — autoscaler targets a workload (hpa or scaledobject -> deployment/statefulset/rollout)
- EXPOSES / EXPOSED_BY — server side of an HTTP endpoint
- CALLS / CALLED_BY — client side of an HTTP endpoint (container -> endpoint)
- PUBLISHES / PUBLISHED_BY, CONSUMES / CONSUMED_BY — Kafka/messaging
- QUERIES / QUERIED_BY — database; each edge carries an "action" field with the SQL operation or command (e.g. "SELECT auth.users")

Identifier conventions:
- pod:<ns>/<name>, container:<ns>/<pod>/<name>, and similarly statefulset/daemonset/job/cronjob/rollout/hpa/scaledobject:<ns>/<name>

Recommended workflow:
1. If the user names something approximately, use the "search" tool first (case-insensitive, matches IDs, names, metadata, and edge actions).
2. Use "get_entity" to drill in once you have a concrete ID. It returns metadata (this metadata has attributes 
like what kind of language is that container written in, versions, image tags etc) and all edges (with actions).
3. Use "get_subgraph" to downstream dependencies, immediate or at a deeper level. Use other graph + edge related information to answer structural queries
like how many pods does this deployment run, how many nodes does this deployment span over (this is not a direct edge but a 2 hop edge deployment MANAGES pods 
which RUNS_IN a node. Or how many deployments touch this node where node CONTAINS pod and pod is MANAGED_BY deployment etc
Prefer fewer, more targeted calls. Quote the IDs you fetch in your answers so a human can verify.`

// New builds the MCP server backed by the graph-read REST API.
func New(client *GraphClient) *mcp.Server {
	srv := mcp.NewServer(
		&mcp.Implementation{Name: "graph-read", Version: "0.1.0"},
		&mcp.ServerOptions{Instructions: instructions},
	)
	registerTools(srv, client)
	return srv
}

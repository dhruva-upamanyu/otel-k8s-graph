// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/graph"
)

const (
	attrDBSystem   = "db.system"
	attrServerPort = "server.port"
)

// deriveDatabaseEdges detects database client span metrics and attaches a
// database entity with QUERIES / QUERIED_BY edges from the emitting
// container. Each unique span.name (SQL operation, "Create connection",
// etc.) becomes a distinct edge tagged with that action — the dedup key
// is (kind, action, to), so calling the same DB many ways produces many
// edges, one per operation.
//
// Detection: db.system present AND span.kind == SPAN_KIND_CLIENT AND
// server.address present. server.port is recommended but optional; the
// database ID falls back to system+host when port is missing.
//
// Database ID format: database:<system>/<host>[:<port>]
//
//	e.g. database:mysql/10.27.0.3:3306, database:postgres/db.example.com
func deriveDatabaseEdges(g graph.Writer, r Record, emitterID string) {
	if emitterID == "" {
		return
	}
	system := r.SeriesAttrs[attrDBSystem]
	if system == "" {
		return
	}
	if normalizeSpanKind(r.SeriesAttrs[attrSpanKind]) != "CLIENT" {
		return
	}
	host := r.SeriesAttrs[attrServerAddress]
	if host == "" {
		return
	}

	dbID, dbName := makeDatabaseID(system, host, r.SeriesAttrs[attrServerPort])
	// Templatize numeric path segments so high-cardinality span names
	// (e.g. "GET /orders/123") collapse to one action/edge instead of one
	// per id. SQL-shaped names ("SELECT auth.users") pass through unchanged.
	action := templatizeSpanName(r.SeriesAttrs[attrSpanName])

	addRelationship(g, emitterID, graph.EdgeQueries, graph.EdgeQueriedBy,
		dbID, graph.KindDatabase, dbName, action)
}

func makeDatabaseID(system, host, port string) (id, name string) {
	endpoint := host
	if port != "" {
		endpoint = host + ":" + port
	}
	return "database:" + system + "/" + endpoint, system + "@" + endpoint
}

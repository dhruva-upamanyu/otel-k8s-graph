// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"strings"
	"testing"

	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/graph"
)

// recWriter records the Writer calls Derive makes.
type recWriter struct {
	upserts []string // entity ids Upserted (owned)
	edges   []string // "from|kind|to|action"
	metas   []string // ids SetMetadata was called on
}

func (r *recWriter) Upsert(id string, _ graph.Kind, _ string) { r.upserts = append(r.upserts, id) }
func (r *recWriter) UpsertWithEdges(from string, _ graph.Kind, _ string, edges []graph.EdgeTo) {
	r.upserts = append(r.upserts, from)
	for _, e := range edges {
		r.upserts = append(r.upserts, e.ToID)
		r.edges = append(r.edges, from+"|"+string(e.Kind)+"|"+e.ToID+"|"+e.Action)
	}
}
func (r *recWriter) SetMetadata(id string, _ map[string]string) { r.metas = append(r.metas, id) }
func (r *recWriter) AddEdges(from string, edges []graph.EdgeTo) {
	for _, e := range edges {
		r.edges = append(r.edges, from+"|"+string(e.Kind)+"|"+e.ToID+"|"+e.Action)
	}
}

func (r *recWriter) hasUpsert(id string) bool { return contains(r.upserts, id) }
func (r *recWriter) hasEdge(s string) bool    { return contains(r.edges, s) }
func (r *recWriter) ownsStructural() bool {
	for _, id := range r.upserts {
		if strings.HasPrefix(id, "container:") || strings.HasPrefix(id, "pod:") ||
			strings.HasPrefix(id, "namespace:") || strings.HasPrefix(id, "node:") ||
			strings.HasPrefix(id, "deployment:") {
			return true
		}
	}
	return false
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestDerive_ServerEndpoint_OwnsEndpointNotStructure(t *testing.T) {
	w := &recWriter{}
	ok := Derive(w, Record{
		Attrs: map[string]string{
			"k8s.namespace.name": "default", "k8s.pod.name": "auth-1", "k8s.container.name": "app",
		},
		SeriesAttrs: map[string]string{
			"span.kind": "SPAN_KIND_SERVER", "http.request.method": "POST",
			"http.route": "/api/auth/validate", "service.name": "auth-service",
		},
	})
	if !ok {
		t.Fatal("expected an emitter")
	}
	ep := "endpoint:auth-service/POST/api/auth/validate"
	c := "container:default/auth-1/app"

	if !w.hasUpsert(ep) {
		t.Errorf("endpoint not owned; upserts=%v", w.upserts)
	}
	if w.ownsStructural() {
		t.Errorf("structural entity upserted by relationships path; upserts=%v", w.upserts)
	}
	if len(w.metas) != 0 {
		t.Errorf("no metadata should be written; metas=%v", w.metas)
	}
	if !w.hasEdge(c + "|EXPOSES|" + ep + "|") {
		t.Errorf("missing container EXPOSES endpoint edge; edges=%v", w.edges)
	}
	if !w.hasEdge(ep + "|EXPOSED_BY|" + c + "|") {
		t.Errorf("missing endpoint EXPOSED_BY container edge; edges=%v", w.edges)
	}
}

func TestDerive_DatabaseQuery_OwnsDatabaseWithAction(t *testing.T) {
	w := &recWriter{}
	Derive(w, Record{
		Attrs: map[string]string{
			"k8s.namespace.name": "default", "k8s.pod.name": "auth-1", "k8s.container.name": "app",
		},
		SeriesAttrs: map[string]string{
			"span.kind": "SPAN_KIND_CLIENT", "db.system": "mysql",
			"server.address": "10.0.0.3", "server.port": "3306", "span.name": "SELECT auth.users",
		},
	})
	db := "database:mysql/10.0.0.3:3306"
	c := "container:default/auth-1/app"

	if !w.hasUpsert(db) {
		t.Errorf("database not owned; upserts=%v", w.upserts)
	}
	if w.ownsStructural() {
		t.Errorf("structural entity upserted; upserts=%v", w.upserts)
	}
	if !w.hasEdge(c + "|QUERIES|" + db + "|SELECT auth.users") {
		t.Errorf("missing QUERIES edge with action; edges=%v", w.edges)
	}
	if !w.hasEdge(db + "|QUERIED_BY|" + c + "|SELECT auth.users") {
		t.Errorf("missing QUERIED_BY edge with action; edges=%v", w.edges)
	}
}

func TestDerive_NoK8sAnchor_WritesNothing(t *testing.T) {
	w := &recWriter{}
	ok := Derive(w, Record{
		Attrs: map[string]string{}, // no k8s.* -> no emitter
		SeriesAttrs: map[string]string{
			"span.kind": "SPAN_KIND_SERVER", "http.request.method": "GET",
			"http.route": "/x", "service.name": "svc",
		},
	})
	if ok {
		t.Fatal("expected false with no emitter")
	}
	if len(w.upserts) != 0 || len(w.edges) != 0 {
		t.Fatalf("nothing should be written; upserts=%v edges=%v", w.upserts, w.edges)
	}
}

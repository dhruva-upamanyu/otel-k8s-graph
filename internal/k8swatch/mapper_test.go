// SPDX-License-Identifier: Apache-2.0

package k8swatch

import (
	"testing"

	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/graph"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func hasEntity(d Desired, id string, kind graph.Kind) bool {
	for _, e := range d.Entities {
		if e.ID == id && e.Kind == kind {
			return true
		}
	}
	return false
}

func hasEdge(d Desired, from string, kind graph.EdgeKind, to string) bool {
	for _, e := range d.Edges {
		if e.FromID == from && e.Edge.Kind == kind && e.Edge.ToResource == to {
			return true
		}
	}
	return false
}

func TestMapPod_EntitiesAndEdges(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "auth-1", Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName:   "node-a",
			Containers: []corev1.Container{{Name: "app", Image: "myorg/auth"}},
		},
	}
	d := MapPod(pod, "auth-deploy")

	podIDv := "pod:default/auth-1"
	cID := "container:default/auth-1/app"
	if !hasEntity(d, podIDv, graph.KindPod) {
		t.Error("missing pod entity")
	}
	if !hasEntity(d, cID, graph.KindContainer) {
		t.Error("missing container entity")
	}
	if !hasEdge(d, "namespace:default", graph.EdgeContains, podIDv) ||
		!hasEdge(d, podIDv, graph.EdgeRunsIn, "namespace:default") {
		t.Error("missing namespace<->pod edges")
	}
	if !hasEdge(d, "node:node-a", graph.EdgeContains, podIDv) ||
		!hasEdge(d, podIDv, graph.EdgeRunsIn, "node:node-a") {
		t.Error("missing node<->pod edges")
	}
	if !hasEdge(d, "deployment:default/auth-deploy", graph.EdgeManages, podIDv) ||
		!hasEdge(d, podIDv, graph.EdgeManagedBy, "deployment:default/auth-deploy") {
		t.Error("missing deployment<->pod edges")
	}
	if !hasEdge(d, podIDv, graph.EdgeContains, cID) ||
		!hasEdge(d, cID, graph.EdgeRunsIn, podIDv) {
		t.Error("missing pod<->container edges")
	}
	// metadata
	for _, e := range d.Entities {
		if e.ID == cID && e.Metadata["container.image.name"] != "myorg/auth" {
			t.Errorf("container image metadata = %q, want myorg/auth", e.Metadata["container.image.name"])
		}
		if e.ID == podIDv && e.Metadata["k8s.node.name"] != "node-a" {
			t.Errorf("pod node metadata = %q, want node-a", e.Metadata["k8s.node.name"])
		}
	}
}

func TestMapPod_UnscheduledHasNoNodeEdge(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c"}}},
	}
	d := MapPod(pod, "")
	for _, e := range d.Edges {
		if len(e.FromID) >= 5 && e.FromID[:5] == "node:" {
			t.Fatalf("unscheduled pod should have no node edge (from %s)", e.FromID)
		}
		if len(e.Edge.ToResource) >= 5 && e.Edge.ToResource[:5] == "node:" {
			t.Fatalf("unscheduled pod should have no node edge (to %s)", e.Edge.ToResource)
		}
	}
}

func TestMapNode_Entity(t *testing.T) {
	n := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a", Labels: map[string]string{"role": "worker"}}}
	d := MapNode(n)
	if !hasEntity(d, "node:node-a", graph.KindNode) {
		t.Fatal("missing node entity")
	}
}

func TestMapNode_ZoneAndRegion(t *testing.T) {
	n := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-a",
			Labels: map[string]string{
				"topology.kubernetes.io/zone":   "asia-south1-a",
				"topology.kubernetes.io/region": "asia-south1",
			},
		},
	}
	d := MapNode(n)

	// Entities: node, zone, region
	if !hasEntity(d, "node:node-a", graph.KindNode) {
		t.Error("missing node entity")
	}
	if !hasEntity(d, "zone:asia-south1-a", graph.KindZone) {
		t.Error("missing zone entity")
	}
	if !hasEntity(d, "region:asia-south1", graph.KindRegion) {
		t.Error("missing region entity")
	}

	// Edges: zone CONTAINS node + node RUNS_IN zone (addPair 1)
	if !hasEdge(d, "zone:asia-south1-a", graph.EdgeContains, "node:node-a") {
		t.Error("missing zone CONTAINS node edge")
	}
	if !hasEdge(d, "node:node-a", graph.EdgeRunsIn, "zone:asia-south1-a") {
		t.Error("missing node RUNS_IN zone edge")
	}

	// Edges: region CONTAINS zone + zone RUNS_IN region (addPair 2)
	if !hasEdge(d, "region:asia-south1", graph.EdgeContains, "zone:asia-south1-a") {
		t.Error("missing region CONTAINS zone edge")
	}
	if !hasEdge(d, "zone:asia-south1-a", graph.EdgeRunsIn, "region:asia-south1") {
		t.Error("missing zone RUNS_IN region edge")
	}

	// Exactly 4 edges total
	if len(d.Edges) != 4 {
		t.Errorf("expected 4 edges, got %d: %+v", len(d.Edges), d.Edges)
	}
}

func TestMapNode_ZoneOnly(t *testing.T) {
	n := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-b",
			Labels: map[string]string{"topology.kubernetes.io/zone": "us-east1-b"},
		},
	}
	d := MapNode(n)

	if !hasEntity(d, "node:node-b", graph.KindNode) {
		t.Error("missing node entity")
	}
	if !hasEntity(d, "zone:us-east1-b", graph.KindZone) {
		t.Error("missing zone entity")
	}
	for _, e := range d.Entities {
		if e.Kind == graph.KindRegion {
			t.Error("zone-only node must not produce a region entity")
		}
	}

	// Exactly 2 edges (zone<->node pair)
	if len(d.Edges) != 2 {
		t.Errorf("expected 2 edges, got %d: %+v", len(d.Edges), d.Edges)
	}
}

func TestMapNode_LegacyZoneAndRegionLabels(t *testing.T) {
	n := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-c",
			Labels: map[string]string{
				"failure-domain.beta.kubernetes.io/zone":   "eu-west1-a",
				"failure-domain.beta.kubernetes.io/region": "eu-west1",
			},
		},
	}
	d := MapNode(n)

	if !hasEntity(d, "zone:eu-west1-a", graph.KindZone) {
		t.Error("missing zone entity from legacy label")
	}
	if !hasEntity(d, "region:eu-west1", graph.KindRegion) {
		t.Error("missing region entity from legacy label")
	}
	if len(d.Edges) != 4 {
		t.Errorf("expected 4 edges, got %d", len(d.Edges))
	}
}

func TestMapNode_ModernLabelWinsOverLegacy(t *testing.T) {
	n := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-d",
			Labels: map[string]string{
				"topology.kubernetes.io/zone":              "modern-zone",
				"failure-domain.beta.kubernetes.io/zone":   "legacy-zone",
				"topology.kubernetes.io/region":            "modern-region",
				"failure-domain.beta.kubernetes.io/region": "legacy-region",
			},
		},
	}
	d := MapNode(n)

	if !hasEntity(d, "zone:modern-zone", graph.KindZone) {
		t.Error("modern zone entity not found")
	}
	for _, e := range d.Entities {
		if e.ID == "zone:legacy-zone" {
			t.Error("legacy zone entity must not be created when modern label present")
		}
		if e.ID == "region:legacy-region" {
			t.Error("legacy region entity must not be created when modern label present")
		}
	}
}

func TestMapNode_RegionLabelOnlyNoEntities(t *testing.T) {
	n := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-e",
			Labels: map[string]string{"topology.kubernetes.io/region": "us-central1"},
		},
	}
	d := MapNode(n)

	// Only the node entity; no zone, no region, no edges
	if len(d.Entities) != 1 || !hasEntity(d, "node:node-e", graph.KindNode) {
		t.Errorf("expected only node entity, got %+v", d.Entities)
	}
	if len(d.Edges) != 0 {
		t.Errorf("expected no edges, got %+v", d.Edges)
	}
}

func TestMapNode_NoLabels(t *testing.T) {
	n := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-f"}}
	d := MapNode(n)

	if len(d.Entities) != 1 || !hasEntity(d, "node:node-f", graph.KindNode) {
		t.Errorf("expected only node entity, got %+v", d.Entities)
	}
	if len(d.Edges) != 0 {
		t.Errorf("expected no edges, got %+v", d.Edges)
	}
}

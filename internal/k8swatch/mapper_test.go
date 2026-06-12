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

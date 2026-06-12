// SPDX-License-Identifier: Apache-2.0

package k8swatch

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/graph"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// recWriter records calls for assertions. It is written by informer
// goroutines and read by the test goroutine, so all access is mutex-guarded.
type recWriter struct {
	mu      sync.Mutex
	upserts []string // "id"
	deletes []string // "id"
	addEdge []string // "from|kind|to"
	remEdge []string // "from|kind|to"
}

func (r *recWriter) UpsertEntity(id string, _ graph.Kind, _ string, _ map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.upserts = append(r.upserts, id)
}
func (r *recWriter) DeleteEntity(id string, _ graph.Kind) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deletes = append(r.deletes, id)
}
func (r *recWriter) AddEdge(from string, e graph.Edge) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addEdge = append(r.addEdge, from+"|"+string(e.Kind)+"|"+e.ToResource)
}
func (r *recWriter) RemoveEdge(from string, e graph.Edge) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.remEdge = append(r.remEdge, from+"|"+string(e.Kind)+"|"+e.ToResource)
}

func (r *recWriter) hasUpsert(id string) bool   { return r.has(&r.upserts, id) }
func (r *recWriter) hasAddEdge(s string) bool   { return r.has(&r.addEdge, s) }
func (r *recWriter) snapshotUpserts() []string  { return r.snapshot(&r.upserts) }
func (r *recWriter) snapshotAddEdges() []string { return r.snapshot(&r.addEdge) }

func (r *recWriter) has(slice *[]string, v string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range *slice {
		if x == v {
			return true
		}
	}
	return false
}

func (r *recWriter) snapshot(slice *[]string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(*slice))
	copy(out, *slice)
	return out
}

func TestApplyAdd_UpsertsAllAndAddsEdges(t *testing.T) {
	r := &recWriter{}
	d := Desired{
		Entities: []DesiredEntity{{ID: "pod:default/p", Kind: graph.KindPod, Name: "p"}},
		Edges:    []DesiredEdge{{FromID: "namespace:default", Edge: graph.Edge{Kind: graph.EdgeContains, ToResource: "pod:default/p"}}},
	}
	applyAdd(r, d)
	if len(r.upserts) != 1 || r.upserts[0] != "pod:default/p" {
		t.Fatalf("upserts = %v", r.upserts)
	}
	if len(r.addEdge) != 1 {
		t.Fatalf("addEdge = %v", r.addEdge)
	}
}

func TestApplyUpdate_DiffsEdgesAndDropsRemovedEntities(t *testing.T) {
	r := &recWriter{}
	oldD := Desired{
		Entities: []DesiredEntity{{ID: "pod:default/p", Kind: graph.KindPod, Name: "p"}, {ID: "node:node-a", Kind: graph.KindNode, Name: "node-a"}},
		Edges:    []DesiredEdge{{FromID: "node:node-a", Edge: graph.Edge{Kind: graph.EdgeContains, ToResource: "pod:default/p"}}},
	}
	newD := Desired{
		Entities: []DesiredEntity{{ID: "pod:default/p", Kind: graph.KindPod, Name: "p"}, {ID: "node:node-b", Kind: graph.KindNode, Name: "node-b"}},
		Edges:    []DesiredEdge{{FromID: "node:node-b", Edge: graph.Edge{Kind: graph.EdgeContains, ToResource: "pod:default/p"}}},
	}
	applyUpdate(r, oldD, newD)

	sort.Strings(r.addEdge)
	if len(r.addEdge) != 1 || r.addEdge[0] != "node:node-b|CONTAINS|pod:default/p" {
		t.Fatalf("addEdge = %v", r.addEdge)
	}
	if len(r.remEdge) != 1 || r.remEdge[0] != "node:node-a|CONTAINS|pod:default/p" {
		t.Fatalf("remEdge = %v", r.remEdge)
	}
	if len(r.deletes) != 0 {
		t.Fatalf("node move must not delete the node entity; deletes = %v", r.deletes)
	}
}

func TestApplyDelete_RemovesEntitiesAndEdges(t *testing.T) {
	r := &recWriter{}
	d := Desired{
		Entities: []DesiredEntity{{ID: "pod:default/p", Kind: graph.KindPod}, {ID: "container:default/p/c", Kind: graph.KindContainer}},
		Edges:    []DesiredEdge{{FromID: "namespace:default", Edge: graph.Edge{Kind: graph.EdgeContains, ToResource: "pod:default/p"}}},
	}
	applyDelete(r, d)
	sort.Strings(r.deletes)
	if len(r.deletes) != 2 {
		t.Fatalf("deletes = %v", r.deletes)
	}
	if len(r.remEdge) != 1 {
		t.Fatalf("remEdge = %v", r.remEdge)
	}
}

func TestApplyUpdate_RemovedContainerIsDeleted(t *testing.T) {
	r := &recWriter{}
	oldD := Desired{Entities: []DesiredEntity{
		{ID: "pod:default/p", Kind: graph.KindPod, Name: "p"},
		{ID: "container:default/p/c1", Kind: graph.KindContainer, Name: "c1"},
		{ID: "container:default/p/c2", Kind: graph.KindContainer, Name: "c2"},
	}}
	newD := Desired{Entities: []DesiredEntity{
		{ID: "pod:default/p", Kind: graph.KindPod, Name: "p"},
		{ID: "container:default/p/c1", Kind: graph.KindContainer, Name: "c1"},
	}}
	applyUpdate(r, oldD, newD)
	if len(r.deletes) != 1 || r.deletes[0] != "container:default/p/c2" {
		t.Fatalf("expected the removed container to be deleted; deletes = %v", r.deletes)
	}
}

func TestApplyUpdate_NodeZoneLabelRemoved(t *testing.T) {
	// Old desired: node with zone label -> node entity + zone entity + 2 edges
	oldNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-a",
			Labels: map[string]string{"topology.kubernetes.io/zone": "us-east1-b"},
		},
	}
	newNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"}, // zone label removed
	}

	oldD := MapNode(oldNode)
	newD := MapNode(newNode)

	r := &recWriter{}
	applyUpdate(r, oldD, newD)

	// Zone/node edges must be removed
	zoneContainsNode := "zone:us-east1-b|CONTAINS|node:node-a"
	nodeRunsInZone := "node:node-a|RUNS_IN|zone:us-east1-b"
	if !r.has(&r.remEdge, zoneContainsNode) {
		t.Errorf("zone CONTAINS node edge not removed; remEdge=%v", r.remEdge)
	}
	if !r.has(&r.remEdge, nodeRunsInZone) {
		t.Errorf("node RUNS_IN zone edge not removed; remEdge=%v", r.remEdge)
	}

	// Zone entity must NOT be deleted (it is a shared entity)
	for _, d := range r.deletes {
		if d == "zone:us-east1-b" {
			t.Error("zone entity must not be deleted on label removal")
		}
	}

	// Node entity must still be present (upserted in newD)
	if !r.hasUpsert("node:node-a") {
		t.Errorf("node entity not upserted; upserts=%v", r.upserts)
	}
}

func TestWatcher_InitialSyncWritesEntities(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "auth-1", Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "auth-rs"}},
		},
		Spec: corev1.PodSpec{NodeName: "node-a", Containers: []corev1.Container{{Name: "app"}}},
	}
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "auth-rs", Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "auth"}},
		},
	}
	client := fake.NewSimpleClientset(pod, rs)
	r := &recWriter{}
	wt := NewWatcher(client, r, 0, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go wt.Run(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r.hasUpsert("pod:default/auth-1") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	if !r.hasUpsert("pod:default/auth-1") {
		t.Fatalf("pod entity not written; upserts=%v", r.snapshotUpserts())
	}
	if !r.hasUpsert("container:default/auth-1/app") {
		t.Fatalf("container entity not written; upserts=%v", r.snapshotUpserts())
	}
	if !r.hasAddEdge("deployment:default/auth|MANAGES|pod:default/auth-1") {
		t.Fatalf("deployment edge missing; addEdge=%v", r.snapshotAddEdges())
	}
}

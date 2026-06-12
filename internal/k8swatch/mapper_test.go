// SPDX-License-Identifier: Apache-2.0

package k8swatch

import (
	"testing"

	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/graph"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
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
	d := MapPod(pod, deploymentID("default", "auth-deploy"), "auth-deploy", graph.KindDeployment)

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
	d := MapPod(pod, "", "", "")
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
	if !hasEntity(d, "region:modern-region", graph.KindRegion) {
		t.Error("modern region entity not found")
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

// ---- StatefulSet ----

func TestMapStatefulSet_EntityAndLabels(t *testing.T) {
	ss := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "postgres", Namespace: "db",
			Labels: map[string]string{"app": "postgres"},
		},
	}
	d := MapStatefulSet(ss)
	if !hasEntity(d, "statefulset:db/postgres", graph.KindStatefulSet) {
		t.Error("missing statefulset entity")
	}
	if len(d.Entities) != 1 {
		t.Errorf("expected 1 entity, got %d", len(d.Entities))
	}
	for _, e := range d.Entities {
		if e.ID == "statefulset:db/postgres" {
			if e.Metadata["label.app"] != "postgres" {
				t.Errorf("label.app = %q, want postgres", e.Metadata["label.app"])
			}
		}
	}
}

// ---- DaemonSet ----

func TestMapDaemonSet_EntityAndLabels(t *testing.T) {
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "fluentd", Namespace: "logging",
			Labels: map[string]string{"component": "logging"},
		},
	}
	d := MapDaemonSet(ds)
	if !hasEntity(d, "daemonset:logging/fluentd", graph.KindDaemonSet) {
		t.Error("missing daemonset entity")
	}
	if len(d.Entities) != 1 {
		t.Errorf("expected 1 entity, got %d", len(d.Entities))
	}
	for _, e := range d.Entities {
		if e.ID == "daemonset:logging/fluentd" {
			if e.Metadata["label.component"] != "logging" {
				t.Errorf("label.component = %q, want logging", e.Metadata["label.component"])
			}
		}
	}
}

// ---- CronJob ----

func TestMapCronJob_EntityAndSchedule(t *testing.T) {
	cj := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: "daily-report", Namespace: "jobs",
			Labels: map[string]string{"team": "data"},
		},
		Spec: batchv1.CronJobSpec{Schedule: "0 3 * * *"},
	}
	d := MapCronJob(cj)
	if !hasEntity(d, "cronjob:jobs/daily-report", graph.KindCronJob) {
		t.Error("missing cronjob entity")
	}
	if len(d.Entities) != 1 {
		t.Errorf("expected 1 entity, got %d", len(d.Entities))
	}
	for _, e := range d.Entities {
		if e.ID == "cronjob:jobs/daily-report" {
			if e.Metadata["label.team"] != "data" {
				t.Errorf("label.team = %q, want data", e.Metadata["label.team"])
			}
			if e.Metadata["cronjob.schedule"] != "0 3 * * *" {
				t.Errorf("cronjob.schedule = %q, want 0 3 * * *", e.Metadata["cronjob.schedule"])
			}
		}
	}
}

// ---- Job ----

func TestMapJob_NoOwner(t *testing.T) {
	j := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: "migrate-db", Namespace: "default",
			Labels: map[string]string{"run": "migrate"},
		},
	}
	d := MapJob(j)
	if !hasEntity(d, "job:default/migrate-db", graph.KindJob) {
		t.Error("missing job entity")
	}
	if len(d.Entities) != 1 {
		t.Errorf("expected 1 entity (job only), got %d: %+v", len(d.Entities), d.Entities)
	}
	if len(d.Edges) != 0 {
		t.Errorf("expected no edges, got %d: %+v", len(d.Edges), d.Edges)
	}
}

func TestMapJob_WithCronJobOwner(t *testing.T) {
	j := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: "daily-report-1234", Namespace: "jobs",
			Labels: map[string]string{"team": "data"},
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "CronJob", Name: "daily-report"},
			},
		},
	}
	d := MapJob(j)
	jobIDv := "job:jobs/daily-report-1234"
	cronIDv := "cronjob:jobs/daily-report"
	if !hasEntity(d, jobIDv, graph.KindJob) {
		t.Error("missing job entity")
	}
	if !hasEntity(d, cronIDv, graph.KindCronJob) {
		t.Error("missing cronjob entity")
	}
	if !hasEdge(d, cronIDv, graph.EdgeManages, jobIDv) {
		t.Error("missing cronjob MANAGES job edge")
	}
	if !hasEdge(d, jobIDv, graph.EdgeManagedBy, cronIDv) {
		t.Error("missing job MANAGED_BY cronjob edge")
	}
}

// ---- MapPod with new owner kinds ----

func TestMapPod_StatefulSetOwner(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pg-0", Namespace: "db"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "pg"}}},
	}
	ssID := statefulSetID("db", "postgres")
	d := MapPod(pod, ssID, "postgres", graph.KindStatefulSet)

	podIDv := "pod:db/pg-0"
	if !hasEntity(d, ssID, graph.KindStatefulSet) {
		t.Error("missing statefulset entity")
	}
	if !hasEdge(d, ssID, graph.EdgeManages, podIDv) {
		t.Error("missing statefulset MANAGES pod edge")
	}
	if !hasEdge(d, podIDv, graph.EdgeManagedBy, ssID) {
		t.Error("missing pod MANAGED_BY statefulset edge")
	}
}

func TestMapPod_DaemonSetOwner(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "fluentd-abc", Namespace: "logging"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "fluentd"}}},
	}
	dsID := daemonSetID("logging", "fluentd")
	d := MapPod(pod, dsID, "fluentd", graph.KindDaemonSet)

	podIDv := "pod:logging/fluentd-abc"
	if !hasEntity(d, dsID, graph.KindDaemonSet) {
		t.Error("missing daemonset entity")
	}
	if !hasEdge(d, dsID, graph.EdgeManages, podIDv) {
		t.Error("missing daemonset MANAGES pod edge")
	}
	if !hasEdge(d, podIDv, graph.EdgeManagedBy, dsID) {
		t.Error("missing pod MANAGED_BY daemonset edge")
	}
}

func TestMapPod_JobOwner(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "migrate-xyz", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "migrate"}}},
	}
	jID := jobID("default", "migrate-db")
	d := MapPod(pod, jID, "migrate-db", graph.KindJob)

	podIDv := "pod:default/migrate-xyz"
	if !hasEntity(d, jID, graph.KindJob) {
		t.Error("missing job entity")
	}
	if !hasEdge(d, jID, graph.EdgeManages, podIDv) {
		t.Error("missing job MANAGES pod edge")
	}
	if !hasEdge(d, podIDv, graph.EdgeManagedBy, jID) {
		t.Error("missing pod MANAGED_BY job edge")
	}
}

func TestMapPod_NoOwner(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "standalone", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
	}
	d := MapPod(pod, "", "", "")
	// No owner entity should be present
	for _, e := range d.Entities {
		switch e.Kind {
		case graph.KindDeployment, graph.KindStatefulSet, graph.KindDaemonSet, graph.KindJob:
			t.Errorf("unexpected owner entity %s (%s) when no owner provided", e.ID, e.Kind)
		}
	}
}

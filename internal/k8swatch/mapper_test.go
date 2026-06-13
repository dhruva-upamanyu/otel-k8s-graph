// SPDX-License-Identifier: Apache-2.0

package k8swatch

import (
	"testing"

	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/graph"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"
)

func entityMeta(d Desired, id string) map[string]string {
	for _, e := range d.Entities {
		if e.ID == id {
			return e.Metadata
		}
	}
	return nil
}

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

// ---- TestMapJob_NonCronJobOwnerIgnored (Task A review follow-up) ----

func TestMapJob_NonCronJobOwnerIgnored(t *testing.T) {
	j := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: "rogue-job", Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Node", Name: "node-a"},
			},
		},
	}
	d := MapJob(j)
	if !hasEntity(d, "job:default/rogue-job", graph.KindJob) {
		t.Error("missing job entity")
	}
	if len(d.Entities) != 1 {
		t.Errorf("expected 1 entity (job only), got %d: %+v", len(d.Entities), d.Entities)
	}
	if len(d.Edges) != 0 {
		t.Errorf("expected no edges for non-CronJob owner, got %d: %+v", len(d.Edges), d.Edges)
	}
}

// ---- HPA tests ----

func TestMapHPA_DeploymentTarget(t *testing.T) {
	minR := int32(2)
	h := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-hpa", Namespace: "prod",
			Labels: map[string]string{"team": "platform"},
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			MinReplicas: &minR,
			MaxReplicas: 10,
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				Kind: "Deployment",
				Name: "web",
			},
		},
	}
	d := MapHPA(h)

	hpaIDv := "hpa:prod/web-hpa"
	depIDv := "deployment:prod/web"
	if !hasEntity(d, hpaIDv, graph.KindHPA) {
		t.Error("missing hpa entity")
	}
	if !hasEntity(d, depIDv, graph.KindDeployment) {
		t.Error("missing deployment entity (upserted target)")
	}
	if !hasEdge(d, hpaIDv, graph.EdgeScales, depIDv) {
		t.Error("missing hpa SCALES deployment edge")
	}
	if !hasEdge(d, depIDv, graph.EdgeScaledBy, hpaIDv) {
		t.Error("missing deployment SCALED_BY hpa edge")
	}
	// Metadata checks on hpa entity
	for _, e := range d.Entities {
		if e.ID != hpaIDv {
			continue
		}
		if e.Metadata["hpa.min_replicas"] != "2" {
			t.Errorf("hpa.min_replicas = %q, want 2", e.Metadata["hpa.min_replicas"])
		}
		if e.Metadata["hpa.max_replicas"] != "10" {
			t.Errorf("hpa.max_replicas = %q, want 10", e.Metadata["hpa.max_replicas"])
		}
		if e.Metadata["hpa.target.kind"] != "Deployment" {
			t.Errorf("hpa.target.kind = %q, want Deployment", e.Metadata["hpa.target.kind"])
		}
		if e.Metadata["hpa.target.name"] != "web" {
			t.Errorf("hpa.target.name = %q, want web", e.Metadata["hpa.target.name"])
		}
		if e.Metadata["label.team"] != "platform" {
			t.Errorf("label.team = %q, want platform", e.Metadata["label.team"])
		}
	}
}

func TestMapHPA_NilMinReplicas(t *testing.T) {
	h := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: "web-hpa", Namespace: "prod"},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			MinReplicas: nil,
			MaxReplicas: 5,
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				Kind: "Deployment",
				Name: "web",
			},
		},
	}
	d := MapHPA(h)
	for _, e := range d.Entities {
		if e.ID == "hpa:prod/web-hpa" {
			if e.Metadata["hpa.min_replicas"] != "1" {
				t.Errorf("nil MinReplicas: hpa.min_replicas = %q, want 1", e.Metadata["hpa.min_replicas"])
			}
		}
	}
}

func TestMapHPA_StatefulSetTarget(t *testing.T) {
	h := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: "db-hpa", Namespace: "data"},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			MinReplicas: ptr.To(int32(1)),
			MaxReplicas: 4,
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				Kind: "StatefulSet",
				Name: "postgres",
			},
		},
	}
	d := MapHPA(h)
	ssIDv := "statefulset:data/postgres"
	hpaIDv := "hpa:data/db-hpa"
	if !hasEntity(d, ssIDv, graph.KindStatefulSet) {
		t.Error("missing statefulset entity")
	}
	if !hasEdge(d, hpaIDv, graph.EdgeScales, ssIDv) {
		t.Error("missing hpa SCALES statefulset edge")
	}
	if !hasEdge(d, ssIDv, graph.EdgeScaledBy, hpaIDv) {
		t.Error("missing statefulset SCALED_BY hpa edge")
	}
}

func TestMapHPA_RolloutTarget(t *testing.T) {
	h := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: "canary-hpa", Namespace: "prod"},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			MinReplicas: ptr.To(int32(2)),
			MaxReplicas: 8,
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				Kind: "Rollout",
				Name: "canary",
			},
		},
	}
	d := MapHPA(h)
	rollIDv := "rollout:prod/canary"
	hpaIDv := "hpa:prod/canary-hpa"
	if !hasEntity(d, rollIDv, graph.KindRollout) {
		t.Error("missing rollout entity")
	}
	if !hasEdge(d, hpaIDv, graph.EdgeScales, rollIDv) {
		t.Error("missing hpa SCALES rollout edge")
	}
	if !hasEdge(d, rollIDv, graph.EdgeScaledBy, hpaIDv) {
		t.Error("missing rollout SCALED_BY hpa edge")
	}
}

func TestMapHPA_UnmodeledTargetNoEdge(t *testing.T) {
	h := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: "rs-hpa", Namespace: "default"},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			MinReplicas: ptr.To(int32(1)),
			MaxReplicas: 3,
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				Kind: "ReplicaSet",
				Name: "my-rs",
			},
		},
	}
	d := MapHPA(h)
	hpaIDv := "hpa:default/rs-hpa"
	if !hasEntity(d, hpaIDv, graph.KindHPA) {
		t.Error("missing hpa entity")
	}
	if len(d.Entities) != 1 {
		t.Errorf("expected 1 entity (hpa only for unmodeled target), got %d: %+v", len(d.Entities), d.Entities)
	}
	if len(d.Edges) != 0 {
		t.Errorf("expected no edges for unmodeled target, got %d: %+v", len(d.Edges), d.Edges)
	}
	// metadata still records the target
	for _, e := range d.Entities {
		if e.ID == hpaIDv {
			if e.Metadata["hpa.target.kind"] != "ReplicaSet" {
				t.Errorf("hpa.target.kind = %q, want ReplicaSet", e.Metadata["hpa.target.kind"])
			}
			if e.Metadata["hpa.target.name"] != "my-rs" {
				t.Errorf("hpa.target.name = %q, want my-rs", e.Metadata["hpa.target.name"])
			}
		}
	}
}

func TestMapHPA_KedaOwned(t *testing.T) {
	h := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name: "keda-hpa", Namespace: "prod",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ScaledObject", Name: "my-scaled-obj"},
			},
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			MinReplicas: ptr.To(int32(1)),
			MaxReplicas: 20,
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				Kind: "Deployment",
				Name: "api",
			},
		},
	}
	d := MapHPA(h)

	hpaIDv := "hpa:prod/keda-hpa"
	soIDv := "scaledobject:prod/my-scaled-obj"
	depIDv := "deployment:prod/api"

	if !hasEntity(d, hpaIDv, graph.KindHPA) {
		t.Error("missing hpa entity")
	}
	if !hasEntity(d, soIDv, graph.KindScaledObject) {
		t.Error("missing scaledobject entity")
	}
	if !hasEntity(d, depIDv, graph.KindDeployment) {
		t.Error("missing deployment entity")
	}
	if !hasEdge(d, soIDv, graph.EdgeManages, hpaIDv) {
		t.Error("missing scaledobject MANAGES hpa edge")
	}
	if !hasEdge(d, hpaIDv, graph.EdgeManagedBy, soIDv) {
		t.Error("missing hpa MANAGED_BY scaledobject edge")
	}
	if !hasEdge(d, hpaIDv, graph.EdgeScales, depIDv) {
		t.Error("missing hpa SCALES deployment edge")
	}
	if !hasEdge(d, depIDv, graph.EdgeScaledBy, hpaIDv) {
		t.Error("missing deployment SCALED_BY hpa edge")
	}
}

// ---- Rollout (unstructured) ----

func rolloutUnstructured(ns, name string, labels, strategy map[string]any) *unstructured.Unstructured {
	obj := map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Rollout",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
		},
	}
	if labels != nil {
		obj["metadata"].(map[string]any)["labels"] = labels
	}
	if strategy != nil {
		obj["spec"] = map[string]any{"strategy": strategy}
	}
	return &unstructured.Unstructured{Object: obj}
}

func TestMapRolloutUnstructured_Canary(t *testing.T) {
	u := rolloutUnstructured("prod", "web", map[string]any{"team": "platform"},
		map[string]any{"canary": map[string]any{"steps": []any{}}})
	d := MapRolloutUnstructured(u)
	id := "rollout:prod/web"
	if !hasEntity(d, id, graph.KindRollout) {
		t.Fatalf("missing rollout entity; %+v", d.Entities)
	}
	md := entityMeta(d, id)
	if md["rollout.strategy"] != "canary" {
		t.Errorf("rollout.strategy = %q, want canary", md["rollout.strategy"])
	}
	if md["label.team"] != "platform" {
		t.Errorf("label.team = %q, want platform", md["label.team"])
	}
}

func TestMapRolloutUnstructured_BlueGreen(t *testing.T) {
	u := rolloutUnstructured("prod", "web", nil,
		map[string]any{"blueGreen": map[string]any{"activeService": "web-active"}})
	d := MapRolloutUnstructured(u)
	md := entityMeta(d, "rollout:prod/web")
	if md["rollout.strategy"] != "blueGreen" {
		t.Errorf("rollout.strategy = %q, want blueGreen", md["rollout.strategy"])
	}
}

func TestMapRolloutUnstructured_NoStrategy(t *testing.T) {
	u := rolloutUnstructured("prod", "web", nil, nil)
	d := MapRolloutUnstructured(u)
	id := "rollout:prod/web"
	if !hasEntity(d, id, graph.KindRollout) {
		t.Fatalf("missing rollout entity")
	}
	md := entityMeta(d, id)
	if _, ok := md["rollout.strategy"]; ok {
		t.Errorf("rollout.strategy should be absent, got %q", md["rollout.strategy"])
	}
}

// ---- ScaledObject (unstructured) ----

func scaledObjectUnstructured(ns, name string, spec map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "keda.sh/v1alpha1",
		"kind":       "ScaledObject",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
			"labels":    map[string]any{"app": "consumer"},
		},
		"spec": spec,
	}}
}

func TestMapScaledObjectUnstructured_Full(t *testing.T) {
	spec := map[string]any{
		"scaleTargetRef":  map[string]any{"kind": "Deployment", "name": "worker"},
		"minReplicaCount": int64(1),
		"maxReplicaCount": int64(20),
		"triggers": []any{
			map[string]any{"type": "cpu"},
			map[string]any{"type": "kafka"},
		},
		"advanced": map[string]any{
			"horizontalPodAutoscalerConfig": map[string]any{
				"behavior": map[string]any{
					"scaleDown": map[string]any{"stabilizationWindowSeconds": int64(300)},
				},
			},
		},
	}
	u := scaledObjectUnstructured("prod", "worker-so", spec)
	d := MapScaledObjectUnstructured(u)

	soID := "scaledobject:prod/worker-so"
	depID := "deployment:prod/worker"
	if !hasEntity(d, soID, graph.KindScaledObject) {
		t.Fatal("missing scaledobject entity")
	}
	if !hasEntity(d, depID, graph.KindDeployment) {
		t.Error("missing deployment target entity")
	}
	if !hasEdge(d, soID, graph.EdgeScales, depID) {
		t.Error("missing SCALES edge")
	}
	if !hasEdge(d, depID, graph.EdgeScaledBy, soID) {
		t.Error("missing SCALED_BY edge")
	}
	md := entityMeta(d, soID)
	if md["keda.target.kind"] != "Deployment" {
		t.Errorf("keda.target.kind = %q", md["keda.target.kind"])
	}
	if md["keda.target.name"] != "worker" {
		t.Errorf("keda.target.name = %q", md["keda.target.name"])
	}
	if md["keda.min_replicas"] != "1" {
		t.Errorf("keda.min_replicas = %q, want 1", md["keda.min_replicas"])
	}
	if md["keda.max_replicas"] != "20" {
		t.Errorf("keda.max_replicas = %q, want 20", md["keda.max_replicas"])
	}
	if md["keda.triggers"] != "cpu,kafka" {
		t.Errorf("keda.triggers = %q, want cpu,kafka", md["keda.triggers"])
	}
	want := `{"scaleDown":{"stabilizationWindowSeconds":300}}`
	if md["keda.scaling_policy"] != want {
		t.Errorf("keda.scaling_policy = %q, want %q", md["keda.scaling_policy"], want)
	}
	if md["label.app"] != "consumer" {
		t.Errorf("label.app = %q, want consumer", md["label.app"])
	}
}

func TestMapScaledObjectUnstructured_DefaultKind(t *testing.T) {
	spec := map[string]any{
		"scaleTargetRef": map[string]any{"name": "worker"},
	}
	u := scaledObjectUnstructured("prod", "worker-so", spec)
	d := MapScaledObjectUnstructured(u)
	soID := "scaledobject:prod/worker-so"
	depID := "deployment:prod/worker"
	md := entityMeta(d, soID)
	if md["keda.target.kind"] != "Deployment" {
		t.Errorf("keda.target.kind = %q, want Deployment (default)", md["keda.target.kind"])
	}
	if !hasEdge(d, soID, graph.EdgeScales, depID) {
		t.Error("missing SCALES edge for defaulted kind")
	}
}

func TestMapScaledObjectUnstructured_MinimalNoTarget(t *testing.T) {
	spec := map[string]any{}
	u := scaledObjectUnstructured("prod", "worker-so", spec)
	d := MapScaledObjectUnstructured(u)
	soID := "scaledobject:prod/worker-so"
	if !hasEntity(d, soID, graph.KindScaledObject) {
		t.Fatal("missing scaledobject entity")
	}
	if len(d.Edges) != 0 {
		t.Errorf("expected no edges, got %+v", d.Edges)
	}
	if len(d.Entities) != 1 {
		t.Errorf("expected only the scaledobject entity, got %+v", d.Entities)
	}
	md := entityMeta(d, soID)
	if _, ok := md["keda.target.kind"]; ok {
		t.Error("keda.target.kind should be absent without a target name")
	}
	if _, ok := md["keda.target.name"]; ok {
		t.Error("keda.target.name should be absent without a target name")
	}
}

func TestMapScaledObjectUnstructured_UnmodeledKind(t *testing.T) {
	spec := map[string]any{
		"scaleTargetRef": map[string]any{"kind": "CustomThing", "name": "thing"},
	}
	u := scaledObjectUnstructured("prod", "worker-so", spec)
	d := MapScaledObjectUnstructured(u)
	soID := "scaledobject:prod/worker-so"
	if len(d.Edges) != 0 {
		t.Errorf("expected no edges for unmodeled kind, got %+v", d.Edges)
	}
	if len(d.Entities) != 1 {
		t.Errorf("expected only the scaledobject entity, got %+v", d.Entities)
	}
	md := entityMeta(d, soID)
	if md["keda.target.kind"] != "CustomThing" {
		t.Errorf("keda.target.kind = %q, want CustomThing", md["keda.target.kind"])
	}
	if md["keda.target.name"] != "thing" {
		t.Errorf("keda.target.name = %q, want thing", md["keda.target.name"])
	}
}

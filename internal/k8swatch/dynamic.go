// SPDX-License-Identifier: Apache-2.0

// This file holds the dynamic-client plumbing for CRD-backed entities:
// Argo Rollouts (argoproj.io/v1alpha1) and KEDA ScaledObjects
// (keda.sh/v1alpha1). These are watched via dynamic informers and mapped
// from *unstructured.Unstructured. CRD absence is handled gracefully: when a
// CRD is not registered in the cluster, the watcher skips its informer
// entirely (see crdAvailable + the wiring in watcher.go).
package k8swatch

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/graph"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

var (
	rolloutGVR = schema.GroupVersionResource{
		Group: "argoproj.io", Version: "v1alpha1", Resource: "rollouts",
	}
	scaledObjectGVR = schema.GroupVersionResource{
		Group: "keda.sh", Version: "v1alpha1", Resource: "scaledobjects",
	}
)

// crdAvailable reports whether the cluster serves the given resource. It
// queries discovery for the group/version and scans for the resource name.
// Any error (group not registered, partial discovery failure, etc.) is
// treated as "not available" — the caller then skips the informer.
func crdAvailable(disc discovery.DiscoveryInterface, gvr schema.GroupVersionResource) bool {
	list, err := disc.ServerResourcesForGroupVersion(gvr.GroupVersion().String())
	if err != nil {
		return false
	}
	for _, r := range list.APIResources {
		if r.Name == gvr.Resource {
			return true
		}
	}
	return false
}

// MapRolloutUnstructured maps an Argo Rollout to a rollout entity. Metadata is
// the label.* map plus "rollout.strategy" ("canary" or "blueGreen") when the
// corresponding spec.strategy.<x> block is present; the key is omitted when
// neither is declared.
func MapRolloutUnstructured(u *unstructured.Unstructured) Desired {
	var d Desired
	ns, name := u.GetNamespace(), u.GetName()
	id := rolloutID(ns, name)

	md := labelMeta(u.GetLabels())
	if md == nil {
		md = map[string]string{}
	}
	if _, ok, _ := unstructured.NestedMap(u.Object, "spec", "strategy", "canary"); ok {
		md["rollout.strategy"] = "canary"
	} else if _, ok, _ := unstructured.NestedMap(u.Object, "spec", "strategy", "blueGreen"); ok {
		md["rollout.strategy"] = "blueGreen"
	}

	d.addEntity(id, graph.KindRollout, name, md)
	return d
}

// MapScaledObjectUnstructured maps a KEDA ScaledObject to a scaledobject
// entity, recording declared scaling configuration in metadata and emitting a
// SCALES/SCALED_BY edge pair for modeled target kinds (Deployment,
// StatefulSet, Rollout). For any other target kind, or when no target name is
// declared, no edge and no target entity are emitted.
func MapScaledObjectUnstructured(u *unstructured.Unstructured) Desired {
	var d Desired
	ns, name := u.GetNamespace(), u.GetName()
	id := scaledObjectID(ns, name)

	md := labelMeta(u.GetLabels())
	if md == nil {
		md = map[string]string{}
	}

	// scaleTargetRef: name is required for an edge; kind defaults to Deployment.
	targetName, _, _ := unstructured.NestedString(u.Object, "spec", "scaleTargetRef", "name")
	targetKind, _, _ := unstructured.NestedString(u.Object, "spec", "scaleTargetRef", "kind")
	if targetName != "" {
		if targetKind == "" {
			targetKind = "Deployment"
		}
		md["keda.target.kind"] = targetKind
		md["keda.target.name"] = targetName
	}

	if v, ok, _ := unstructured.NestedInt64(u.Object, "spec", "minReplicaCount"); ok {
		md["keda.min_replicas"] = strconv.FormatInt(v, 10)
	}
	if v, ok, _ := unstructured.NestedInt64(u.Object, "spec", "maxReplicaCount"); ok {
		md["keda.max_replicas"] = strconv.FormatInt(v, 10)
	}

	if triggers, ok, _ := unstructured.NestedSlice(u.Object, "spec", "triggers"); ok {
		var types []string
		for _, t := range triggers {
			tm, isMap := t.(map[string]any)
			if !isMap {
				continue
			}
			if ts, isStr := tm["type"].(string); isStr && ts != "" {
				types = append(types, ts)
			}
		}
		if len(types) > 0 {
			md["keda.triggers"] = strings.Join(types, ",")
		}
	}

	if behavior, ok, _ := unstructured.NestedMap(u.Object,
		"spec", "advanced", "horizontalPodAutoscalerConfig", "behavior"); ok {
		if b, err := json.Marshal(behavior); err == nil {
			md["keda.scaling_policy"] = string(b)
		}
	}

	d.addEntity(id, graph.KindScaledObject, name, md)

	// SCALES/SCALED_BY edge for modeled target kinds only.
	if targetName != "" {
		var targetID string
		var targetGraphKind graph.Kind
		switch targetKind {
		case "Deployment":
			targetID, targetGraphKind = deploymentID(ns, targetName), graph.KindDeployment
		case "StatefulSet":
			targetID, targetGraphKind = statefulSetID(ns, targetName), graph.KindStatefulSet
		case "Rollout":
			targetID, targetGraphKind = rolloutID(ns, targetName), graph.KindRollout
		default:
			// Unmodeled kind: metadata already records it; no edge emitted.
		}
		if targetID != "" {
			d.addEntity(targetID, targetGraphKind, targetName, nil)
			d.addPair(id, graph.EdgeScales, targetID, graph.EdgeScaledBy)
		}
	}

	return d
}

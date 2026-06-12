// SPDX-License-Identifier: Apache-2.0

// Package k8swatch derives the structural graph from Kubernetes
// objects. This file holds the pure mapping from K8s objects (pods, nodes,
// namespaces, deployments) to graph entities and edges; the informer
// wiring and Redis writes live in watcher.go.
//
// MapNode additionally derives zone and region entities from the well-known
// topology labels (topology.kubernetes.io/zone|region), falling back to
// their legacy failure-domain.beta.kubernetes.io equivalents. The resulting
// hierarchy is: region CONTAINS zone CONTAINS node.
package k8swatch

import (
	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/graph"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

// DesiredEntity is an entity the object implies should exist.
type DesiredEntity struct {
	ID       string
	Kind     graph.Kind
	Name     string
	Metadata map[string]string
}

// DesiredEdge is a directed edge stored on FromID's edge set.
type DesiredEdge struct {
	FromID string
	Edge   graph.Edge
}

// Desired is the full set of entities + edges one object implies.
type Desired struct {
	Entities []DesiredEntity
	Edges    []DesiredEdge
}

func (d *Desired) addEntity(id string, kind graph.Kind, name string, md map[string]string) {
	d.Entities = append(d.Entities, DesiredEntity{ID: id, Kind: kind, Name: name, Metadata: md})
}

// addPair adds forward + reverse edges between two entities.
func (d *Desired) addPair(fromID string, fwd graph.EdgeKind, toID string, rev graph.EdgeKind) {
	d.Edges = append(d.Edges,
		DesiredEdge{FromID: fromID, Edge: graph.Edge{Kind: fwd, ToResource: toID}},
		DesiredEdge{FromID: toID, Edge: graph.Edge{Kind: rev, ToResource: fromID}},
	)
}

func podID(ns, name string) string         { return "pod:" + ns + "/" + name }
func containerID(ns, pod, c string) string { return "container:" + ns + "/" + pod + "/" + c }
func nsID(name string) string              { return "namespace:" + name }
func nodeID(name string) string            { return "node:" + name }
func zoneID(name string) string            { return "zone:" + name }
func regionID(name string) string          { return "region:" + name }
func deploymentID(ns, name string) string  { return "deployment:" + ns + "/" + name }

// nodeTopologyLabel returns the value of a topology label from node labels,
// preferring the modern topology.kubernetes.io/<suffix> form and falling
// back to the legacy failure-domain.beta.kubernetes.io/<suffix> form.
func nodeTopologyLabel(labels map[string]string, suffix string) string {
	if v := labels["topology.kubernetes.io/"+suffix]; v != "" {
		return v
	}
	return labels["failure-domain.beta.kubernetes.io/"+suffix]
}

// MapPod maps a Pod (deploymentName="" if it has no resolvable Deployment).
func MapPod(p *corev1.Pod, deploymentName string) Desired {
	var d Desired
	id := podID(p.Namespace, p.Name)
	d.addEntity(id, graph.KindPod, p.Name, podMetadata(p))

	d.addEntity(nsID(p.Namespace), graph.KindNamespace, p.Namespace, nil)
	d.addPair(nsID(p.Namespace), graph.EdgeContains, id, graph.EdgeRunsIn)

	if p.Spec.NodeName != "" {
		d.addEntity(nodeID(p.Spec.NodeName), graph.KindNode, p.Spec.NodeName, nil)
		d.addPair(nodeID(p.Spec.NodeName), graph.EdgeContains, id, graph.EdgeRunsIn)
	}
	if deploymentName != "" {
		depID := deploymentID(p.Namespace, deploymentName)
		d.addEntity(depID, graph.KindDeployment, deploymentName, nil)
		d.addPair(depID, graph.EdgeManages, id, graph.EdgeManagedBy)
	}
	for _, c := range p.Spec.Containers {
		cID := containerID(p.Namespace, p.Name, c.Name)
		d.addEntity(cID, graph.KindContainer, c.Name, map[string]string{"container.image.name": c.Image})
		d.addPair(id, graph.EdgeContains, cID, graph.EdgeRunsIn)
	}
	return d
}

// MapNode / MapNamespace / MapDeployment map their objects to a single entity
// each. Their edges to pods are emitted by MapPod (so pod churn maintains them).
//
// MapNode also derives zone and region entities from the well-known topology
// labels. A zone is only emitted when the zone label is present; a region is
// only emitted when both zone and region labels are present (region attaches
// to the zone — without a zone there is nothing sensible to link it to).
func MapNode(n *corev1.Node) Desired {
	var d Desired
	nID := nodeID(n.Name)
	d.addEntity(nID, graph.KindNode, n.Name, labelMeta(n.Labels))

	zone := nodeTopologyLabel(n.Labels, "zone")
	if zone == "" {
		return d
	}
	zID := zoneID(zone)
	d.addEntity(zID, graph.KindZone, zone, nil)
	d.addPair(zID, graph.EdgeContains, nID, graph.EdgeRunsIn)

	region := nodeTopologyLabel(n.Labels, "region")
	if region == "" {
		return d
	}
	rID := regionID(region)
	d.addEntity(rID, graph.KindRegion, region, nil)
	d.addPair(rID, graph.EdgeContains, zID, graph.EdgeRunsIn)

	return d
}

func MapNamespace(n *corev1.Namespace) Desired {
	var d Desired
	d.addEntity(nsID(n.Name), graph.KindNamespace, n.Name, labelMeta(n.Labels))
	return d
}

func MapDeployment(dep *appsv1.Deployment) Desired {
	var d Desired
	d.addEntity(deploymentID(dep.Namespace, dep.Name), graph.KindDeployment, dep.Name, labelMeta(dep.Labels))
	return d
}

func podMetadata(p *corev1.Pod) map[string]string {
	m := labelMeta(p.Labels)
	if m == nil {
		m = map[string]string{}
	}
	if p.Spec.NodeName != "" {
		m["k8s.node.name"] = p.Spec.NodeName
	}
	m["k8s.pod.uid"] = string(p.UID)
	return m
}

func labelMeta(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	m := make(map[string]string, len(labels))
	for k, v := range labels {
		m["label."+k] = v
	}
	return m
}

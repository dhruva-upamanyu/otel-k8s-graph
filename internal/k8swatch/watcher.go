// SPDX-License-Identifier: Apache-2.0

package k8swatch

import (
	"context"
	"log/slog"
	"time"

	"github.com/dhruvaupamanyu/otel-k8s-graph/internal/graph"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	appslisters "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/tools/cache"
)

type entityWriter interface {
	UpsertEntity(id string, kind graph.Kind, name string, metadata map[string]string)
	DeleteEntity(id string, kind graph.Kind)
	AddEdge(fromID string, e graph.Edge)
	RemoveEdge(fromID string, e graph.Edge)
}

func edgeKey(from string, e graph.Edge) string {
	return from + "\x00" + string(e.Kind) + "\x00" + e.ToResource + "\x00" + e.Action
}

func applyAdd(w entityWriter, d Desired) {
	for _, e := range d.Entities {
		w.UpsertEntity(e.ID, e.Kind, e.Name, e.Metadata)
	}
	for _, ed := range d.Edges {
		w.AddEdge(ed.FromID, ed.Edge)
	}
}

func applyUpdate(w entityWriter, oldD, newD Desired) {
	newEntities := make(map[string]struct{}, len(newD.Entities))
	for _, e := range newD.Entities {
		w.UpsertEntity(e.ID, e.Kind, e.Name, e.Metadata)
		newEntities[e.ID] = struct{}{}
	}
	// Only delete entities this object OWNS (the pod and its containers, e.g.
	// a removed container). A shared entity that drops out of the new Desired
	// — a node/namespace/deployment the pod moved away from — must NOT be
	// deleted here; it's owned by its own informer, and the edge diff below
	// already removes the stale edge. Deleting it would wipe a live entity.
	for _, e := range oldD.Entities {
		if _, ok := newEntities[e.ID]; ok {
			continue
		}
		if e.Kind == graph.KindPod || e.Kind == graph.KindContainer {
			w.DeleteEntity(e.ID, e.Kind)
		}
	}
	oldEdges := indexEdges(oldD.Edges)
	newEdges := indexEdges(newD.Edges)
	for k, ed := range newEdges {
		if _, ok := oldEdges[k]; !ok {
			w.AddEdge(ed.FromID, ed.Edge)
		}
	}
	for k, ed := range oldEdges {
		if _, ok := newEdges[k]; !ok {
			w.RemoveEdge(ed.FromID, ed.Edge)
		}
	}
}

func applyDelete(w entityWriter, d Desired) {
	for _, e := range d.Entities {
		// Delete only entities this object owns (pod + its containers). Node/
		// namespace/deployment are separate objects with their own DeleteFunc;
		// just drop their edge to the deleted object below.
		if e.Kind == graph.KindPod || e.Kind == graph.KindContainer {
			w.DeleteEntity(e.ID, e.Kind)
		}
	}
	for _, ed := range d.Edges {
		w.RemoveEdge(ed.FromID, ed.Edge)
	}
}

func indexEdges(edges []DesiredEdge) map[string]DesiredEdge {
	m := make(map[string]DesiredEdge, len(edges))
	for _, ed := range edges {
		m[edgeKey(ed.FromID, ed.Edge)] = ed
	}
	return m
}

// Watcher wires SharedInformers to the writer.
type Watcher struct {
	factory  informers.SharedInformerFactory
	rsLister appslisters.ReplicaSetLister
	podInf   cache.SharedIndexInformer
	w        entityWriter
	logger   *slog.Logger
}

// NewWatcher builds informers for the structural resources. resync drives
// periodic re-delivery (self-heal). Handlers write through w.
func NewWatcher(client kubernetes.Interface, w entityWriter, resync time.Duration, logger *slog.Logger) *Watcher {
	if logger == nil {
		logger = slog.Default()
	}
	f := informers.NewSharedInformerFactory(client, resync)
	wt := &Watcher{
		factory:  f,
		rsLister: f.Apps().V1().ReplicaSets().Lister(),
		podInf:   f.Core().V1().Pods().Informer(),
		w:        w,
		logger:   logger,
	}

	// Node/namespace/deployment/statefulset/daemonset/job/cronjob handlers map
	// each object independently, so they can register up front and fire during
	// the initial list.
	wt.registerSingle(f.Core().V1().Nodes().Informer(), func(o any) (Desired, bool) {
		n, ok := o.(*corev1.Node)
		if !ok {
			return Desired{}, false
		}
		return MapNode(n), true
	})
	wt.registerSingle(f.Core().V1().Namespaces().Informer(), func(o any) (Desired, bool) {
		n, ok := o.(*corev1.Namespace)
		if !ok {
			return Desired{}, false
		}
		return MapNamespace(n), true
	})
	wt.registerSingle(f.Apps().V1().Deployments().Informer(), func(o any) (Desired, bool) {
		dep, ok := o.(*appsv1.Deployment)
		if !ok {
			return Desired{}, false
		}
		return MapDeployment(dep), true
	})
	wt.registerSingle(f.Apps().V1().StatefulSets().Informer(), func(o any) (Desired, bool) {
		ss, ok := o.(*appsv1.StatefulSet)
		if !ok {
			return Desired{}, false
		}
		return MapStatefulSet(ss), true
	})
	wt.registerSingle(f.Apps().V1().DaemonSets().Informer(), func(o any) (Desired, bool) {
		ds, ok := o.(*appsv1.DaemonSet)
		if !ok {
			return Desired{}, false
		}
		return MapDaemonSet(ds), true
	})
	wt.registerSingle(f.Batch().V1().Jobs().Informer(), func(o any) (Desired, bool) {
		j, ok := o.(*batchv1.Job)
		if !ok {
			return Desired{}, false
		}
		return MapJob(j), true
	})
	wt.registerSingle(f.Batch().V1().CronJobs().Informer(), func(o any) (Desired, bool) {
		cj, ok := o.(*batchv1.CronJob)
		if !ok {
			return Desired{}, false
		}
		return MapCronJob(cj), true
	})
	wt.registerSingle(f.Autoscaling().V2().HorizontalPodAutoscalers().Informer(), func(o any) (Desired, bool) {
		h, ok := o.(*autoscalingv2.HorizontalPodAutoscaler)
		if !ok {
			return Desired{}, false
		}
		return MapHPA(h), true
	})
	// ReplicaSets are watched only to resolve pod->owner; no handlers.
	// Referenced here so the factory starts and syncs the informer.
	f.Apps().V1().ReplicaSets().Informer()
	return wt
}

// Run starts informers and blocks until ctx is done. The pod handler is
// registered only after caches sync, so pod->deployment resolution via the
// ReplicaSet lister is reliable on the very first events (registering a
// handler on an already-synced informer replays existing pods as Adds).
func (wt *Watcher) Run(ctx context.Context) {
	wt.factory.Start(ctx.Done())
	wt.factory.WaitForCacheSync(ctx.Done())
	wt.logger.Info("graph-k8s: informer caches synced")
	wt.registerPods(wt.podInf)
	<-ctx.Done()
}

func (wt *Watcher) registerPods(inf cache.SharedIndexInformer) {
	inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(o any) { applyAdd(wt.w, wt.podDesired(o.(*corev1.Pod))) },
		UpdateFunc: func(oldO, newO any) {
			applyUpdate(wt.w, wt.podDesired(oldO.(*corev1.Pod)), wt.podDesired(newO.(*corev1.Pod)))
		},
		DeleteFunc: func(o any) {
			p, ok := o.(*corev1.Pod)
			if !ok {
				if t, tok := o.(cache.DeletedFinalStateUnknown); tok {
					p, ok = t.Obj.(*corev1.Pod)
				}
			}
			if ok {
				applyDelete(wt.w, wt.podDesired(p))
			}
		},
	})
}

func (wt *Watcher) registerSingle(inf cache.SharedIndexInformer, mapFn func(any) (Desired, bool)) {
	inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(o any) {
			if d, ok := mapFn(o); ok {
				applyAdd(wt.w, d)
			}
		},
		UpdateFunc: func(oldO, newO any) {
			do, ok1 := mapFn(oldO)
			dn, ok2 := mapFn(newO)
			if ok1 && ok2 {
				applyUpdate(wt.w, do, dn)
			}
		},
		DeleteFunc: func(o any) {
			if d, ok := o.(cache.DeletedFinalStateUnknown); ok {
				o = d.Obj
			}
			if d, ok := mapFn(o); ok {
				applyDelete(wt.w, d)
			}
		},
	})
}

func (wt *Watcher) podDesired(p *corev1.Pod) Desired {
	id, name, kind := wt.ownerForPod(p)
	return MapPod(p, id, name, kind)
}

// ownerForPod resolves the controlling owner of a pod:
//   - ownerRef Kind "ReplicaSet" → RS ownerRef Kind "Deployment" → deployment
//   - ownerRef Kind "ReplicaSet" → RS ownerRef Kind "Rollout"     → rollout
//   - ownerRef Kind "StatefulSet" → statefulset (direct)
//   - ownerRef Kind "DaemonSet"   → daemonset (direct)
//   - ownerRef Kind "Job"         → job (direct)
//   - none → ("", "", "")
func (wt *Watcher) ownerForPod(p *corev1.Pod) (id, name string, kind graph.Kind) {
	for _, ref := range p.OwnerReferences {
		switch ref.Kind {
		case "ReplicaSet":
			rs, err := wt.rsLister.ReplicaSets(p.Namespace).Get(ref.Name)
			if err != nil {
				// RS not yet in cache or deleted; try the next ownerRef.
				continue
			}
			for _, rref := range rs.OwnerReferences {
				switch rref.Kind {
				case "Deployment":
					return deploymentID(p.Namespace, rref.Name), rref.Name, graph.KindDeployment
				case "Rollout":
					return rolloutID(p.Namespace, rref.Name), rref.Name, graph.KindRollout
				}
			}
		case "StatefulSet":
			return statefulSetID(p.Namespace, ref.Name), ref.Name, graph.KindStatefulSet
		case "DaemonSet":
			return daemonSetID(p.Namespace, ref.Name), ref.Name, graph.KindDaemonSet
		case "Job":
			return jobID(p.Namespace, ref.Name), ref.Name, graph.KindJob
		}
	}
	return "", "", ""
}

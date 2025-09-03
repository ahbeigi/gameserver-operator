package controllers

import (
	"context"
	"fmt"
	"sort"
	"time"

	gamev1alpha1 "github.com/ahbeigi/gameserver-operator/api/v1alpha1"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/equality"   // << add this
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"        // used in reconciler struct

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)


//+kubebuilder:rbac:groups=game.example.com,resources=gsdeployments,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=game.example.com,resources=gsdeployments/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=game.example.com,resources=gsdeployments/finalizers,verbs=update
//+kubebuilder:rbac:groups=game.example.com,resources=gameservers,verbs=get;list;watch;create;update;patch;delete

type GSDeploymentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *GSDeploymentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var gsd gamev1alpha1.GSDeployment
	if err := r.Get(ctx, req.NamespacedName, &gsd); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Defaults
	if gsd.Spec.ScaleUpThresholdPercent == 0 { gsd.Spec.ScaleUpThresholdPercent = 80 }
	if gsd.Spec.ScaleDownZeroSeconds == 0 { gsd.Spec.ScaleDownZeroSeconds = 60 }
	if gsd.Spec.PollPath == "" { gsd.Spec.PollPath = "/status" }
	if gsd.Spec.Image == "" { gsd.Spec.Image = "kyon/gameserver:latest" }

	// List children GameServers
	var children gamev1alpha1.GameServerList
	if err := r.List(ctx, &children, client.InNamespace(gsd.Namespace),
		client.MatchingLabels(childLabels(gsd.Name))); err != nil {
		return ctrl.Result{}, err
	}

	used := map[int32]struct{}{}
	ready := int32(0)
	for _, gs := range children.Items {
		used[gs.Spec.Port] = struct{}{}
		if gs.Status.Phase == "Running" {
			ready++
		}
	}

	// Ensure minReplicas
	desired := maxInt32(gsd.Spec.MinReplicas, 0)
	// Current replicas
	cur := int32(len(children.Items))

	// Create missing
	for cur < desired {
		port, ok := allocatePort(used, gsd.Spec.PortRange.Start, gsd.Spec.PortRange.End)
		if !ok { break }
		name := fmt.Sprintf("%s-%d", gsd.Name, port)
		newGS := gamev1alpha1.GameServer{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: gsd.Namespace,
				Labels:    childLabels(gsd.Name),
			},
			Spec: gamev1alpha1.GameServerSpec{
				Image:       gsd.Spec.Image,
				Port:        port,
				PollPath:    gsd.Spec.PollPath,
				Env:         gsd.Spec.Env,
				Resources:   gsd.Spec.Resources,
				NodeSelector: gsd.Spec.NodeSelector,
			},
		}
		if err := ctrl.SetControllerReference(&gsd, &newGS, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, &newGS); err != nil {
			return ctrl.Result{}, err
		}
		used[port] = struct{}{}
		cur++
	}

	// Re-list after potential creates
	if err := r.List(ctx, &children, client.InNamespace(gsd.Namespace),
		client.MatchingLabels(childLabels(gsd.Name))); err != nil {
		return ctrl.Result{}, err
	}

	// Scale Up rule: ANY >= threshold
	scaleUp := false
	for _, gs := range children.Items {
		mp := gs.Status.MaxPlayers
		if mp > 0 && gs.Status.Players*100/mp >= gsd.Spec.ScaleUpThresholdPercent {
			scaleUp = true; break
		}
	}
	if scaleUp && int32(len(children.Items)) < gsd.Spec.MaxReplicas {
		port, ok := allocatePort(used, gsd.Spec.PortRange.Start, gsd.Spec.PortRange.End)
		if ok {
			name := fmt.Sprintf("%s-%d", gsd.Name, port)
			newGS := gamev1alpha1.GameServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: gsd.Namespace,
					Labels:    childLabels(gsd.Name),
				},
				Spec: gamev1alpha1.GameServerSpec{
					Image:       gsd.Spec.Image,
					Port:        port,
					PollPath:    gsd.Spec.PollPath,
					Env:         gsd.Spec.Env,
					Resources:   gsd.Spec.Resources,
					NodeSelector: gsd.Spec.NodeSelector,
				},
			}
			_ = ctrl.SetControllerReference(&gsd, &newGS, r.Scheme)
			if err := r.Create(ctx, &newGS); err == nil {
				used[port] = struct{}{}
			}
		}
	}

	// Scale Down rule: 0 players for > scaleDownZeroSeconds
	if int32(len(children.Items)) > gsd.Spec.MinReplicas {
		var idle []gamev1alpha1.GameServer
		now := time.Now()
		for _, gs := range children.Items {
			if gs.Status.Players == 0 && gs.Status.ZeroSince != nil {
				if now.Sub(gs.Status.ZeroSince.Time) >= time.Duration(gsd.Spec.ScaleDownZeroSeconds)*time.Second {
					idle = append(idle, gs)
				}
			}
		}
		// Delete oldest idle first (by creation timestamp)
		sort.Slice(idle, func(i, j int) bool {
			return idle[i].CreationTimestamp.Before(&idle[j].CreationTimestamp)
		})
		for _, gs := range idle {
			if int32(len(children.Items)) <= gsd.Spec.MinReplicas { break }
			_ = r.Delete(ctx, &gs)
			// pessimistically reduce count so we don't over-delete in this loop
			children.Items = removeGS(children.Items, gs.Name)
		}
	}

	// Update status
	alloc := make([]int32, 0, len(children.Items))
	for _, gs := range children.Items {
		alloc = append(alloc, gs.Spec.Port)
	}
	newStatus := gsd.Status
	newStatus.Replicas = int32(len(children.Items))
	newStatus.ReadyReplicas = ready
	newStatus.AllocatedPorts = alloc
	if !equality.Semantic.DeepEqual(newStatus, gsd.Status) {
		gsd.Status = newStatus
		if err := r.Status().Update(ctx, &gsd); err != nil && !kerrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *GSDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// React immediately to GameServer STATUS updates
	statusChanged := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldObj, ok1 := e.ObjectOld.(*gamev1alpha1.GameServer)
			newObj, ok2 := e.ObjectNew.(*gamev1alpha1.GameServer)
			if !ok1 || !ok2 { return true }
			return !equality.Semantic.DeepEqual(oldObj.Status, newObj.Status)
		},
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&gamev1alpha1.GSDeployment{}).
		Owns(&gamev1alpha1.GameServer{}, builder.WithPredicates(statusChanged)).
		Complete(r)
}

func childLabels(owner string) map[string]string {
	return map[string]string{"game.example.com/owner": owner}
}

func allocatePort(used map[int32]struct{}, start, end int32) (int32, bool) {
	for p := start; p <= end; p++ {
		if _, ok := used[p]; !ok { return p, true }
	}
	return 0, false
}

func removeGS(list []gamev1alpha1.GameServer, name string) []gamev1alpha1.GameServer {
	out := make([]gamev1alpha1.GameServer, 0, len(list))
	for _, it := range list {
		if it.Name != name { out = append(out, it) }
	}
	return out
}

func maxInt32(a, b int32) int32 { if a > b { return a }; return b }

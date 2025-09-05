package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	gamev1alpha1 "github.com/ahbeigi/gameserver-operator/api/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime" // << add this
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

//+kubebuilder:rbac:groups=game.example.com,resources=gameservers,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=game.example.com,resources=gameservers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=game.example.com,resources=gameservers/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=pods;events,verbs=get;list;watch;create;update;patch;delete

type GameServerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	httpc  *http.Client
}

func (r *GameServerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	var gs gamev1alpha1.GameServer
	if err := r.Get(ctx, req.NamespacedName, &gs); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 1) Ensure Pod exists (1:1)
	var pod corev1.Pod
	err := r.Get(ctx, types.NamespacedName{Name: gs.Name, Namespace: gs.Namespace}, &pod)
	if kerrors.IsNotFound(err) {
		pollPath := gs.Spec.PollPath
		if pollPath == "" {
			pollPath = "/status"
		}
		pod = corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      gs.Name,
				Namespace: gs.Namespace,
				Labels:    map[string]string{"app": gs.Name, "game.example.com/owner": gs.Name},
			},
			Spec: corev1.PodSpec{
				HostNetwork:  true,
				DNSPolicy:    corev1.DNSClusterFirstWithHostNet,
				NodeSelector: gs.Spec.NodeSelector,
				Containers: []corev1.Container{{
					Name:  "server",
					Image: defaultIfEmpty(gs.Spec.Image, "kyon/gameserver:latest"),
					Env: append(gs.Spec.Env, corev1.EnvVar{
						Name:  "GAME_PORT",
						Value: fmt.Sprint(gs.Spec.Port),
					}),
					Ports:     []corev1.ContainerPort{{ContainerPort: gs.Spec.Port}},
					Resources: gs.Spec.Resources,
					ReadinessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: pollPath,
								Port: intstr.FromInt(int(gs.Spec.Port)),
							},
						},
						InitialDelaySeconds: 2,
						PeriodSeconds:       5,
						TimeoutSeconds:      2,
					},
				}},
				RestartPolicy: corev1.RestartPolicyAlways,
			},
		}
		_ = ctrl.SetControllerReference(&gs, &pod, r.Scheme)
		if err := r.Create(ctx, &pod); err != nil {
			log.Error(err, "creating Pod")
			return ctrl.Result{}, err
		}
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// 2) Phase from Pod
	phase := "Pending"
	switch pod.Status.Phase {
	case corev1.PodRunning:
		phase = "Running"
	case corev1.PodFailed:
		phase = "Error"
	case corev1.PodSucceeded:
		phase = "Terminating"
	case corev1.PodPending:
		phase = "Pending"
	}

	// 3) Poll /status if Running
	now := metav1.Now()
	reach := metav1.Condition{
		Type:               "Reachable",
		Status:             metav1.ConditionFalse,
		Reason:             "NotReady",
		LastTransitionTime: now,
		ObservedGeneration: gs.Generation,
	}
	if pod.Status.Phase == corev1.PodRunning && pod.Status.HostIP != "" {
		pollPath := defaultIfEmpty(gs.Spec.PollPath, "/status")
		endpoint := fmt.Sprintf("http://%s:%d%s", pod.Status.HostIP, gs.Spec.Port, pollPath)
		if r.httpc == nil {
			r.httpc = &http.Client{Timeout: 2 * time.Second}
		}

		reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
		resp, err := r.httpc.Do(req)
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			var body struct {
				Players    int32 `json:"players"`
				MaxPlayers int32 `json:"maxPlayers"`
			}
			if json.NewDecoder(resp.Body).Decode(&body) == nil {
				old := gs.DeepCopy()
				gs.Status.Endpoint = endpoint
				gs.Status.LastPolled = &now
				gs.Status.Players = body.Players
				gs.Status.MaxPlayers = body.MaxPlayers
				gs.Status.NodeName = pod.Spec.NodeName
				gs.Status.Phase = phase
				if body.Players == 0 {
					if gs.Status.ZeroSince == nil {
						gs.Status.ZeroSince = &now
					}
				} else {
					gs.Status.ZeroSince = nil
				}
				reach.Status = metav1.ConditionTrue
				reach.Reason = "OK"
				reach.Message = "Status polled"
				setOrUpdateCondition(&gs.Status.Conditions, reach)
				if !equality.Semantic.DeepEqual(old.Status, gs.Status) {
					if err := r.Status().Update(ctx, &gs); err != nil {
						return ctrl.Result{}, err
					}
				}
			}
			if resp.Body != nil {
				resp.Body.Close()
			}
		} else {
			gs.Status.Phase = "Unreachable"
			reach.Status = metav1.ConditionFalse
			reach.Reason = "ConnectionError"
			if err != nil {
				reach.Message = err.Error()
			} else {
				reach.Message = fmt.Sprintf("HTTP %d", resp.StatusCode)
			}
			setOrUpdateCondition(&gs.Status.Conditions, reach)
			gs.Status.LastPolled = &now
			_ = r.Status().Update(ctx, &gs)
		}
	} else {
		gs.Status.Phase = phase
		_ = r.Status().Update(ctx, &gs)
	}

	// Requeue to poll every 10s
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *GameServerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gamev1alpha1.GameServer{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}

func setOrUpdateCondition(conds *[]metav1.Condition, c metav1.Condition) {
	found := false
	for i := range *conds {
		if (*conds)[i].Type == c.Type {
			(*conds)[i] = c
			found = true
			break
		}
	}
	if !found {
		*conds = append(*conds, c)
	}
}

func defaultIfEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

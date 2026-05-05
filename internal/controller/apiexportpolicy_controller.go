package controller

import (
	"context"
	"strings"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	platformeshconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/controller/filter"
	"github.com/platform-mesh/golang-commons/logger"
	corev1alpha1 "github.com/platform-mesh/security-operator/api/v1alpha1"
	iclient "github.com/platform-mesh/security-operator/internal/client"
	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/security-operator/internal/fga"
	"github.com/platform-mesh/security-operator/internal/subroutine"
	"github.com/platform-mesh/subroutines/conditions"
	"github.com/platform-mesh/subroutines/lifecycle"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	ctrhandler "sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	"sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"k8s.io/apimachinery/pkg/types"

	"github.com/kcp-dev/logicalcluster/v3"
	kcptenancyv1alpha1 "github.com/kcp-dev/sdk/apis/tenancy/v1alpha1"
)

const (
	orgsWorkspacePath = "root:orgs"
	readyPhase        = "Ready"
)

type APIExportPolicyReconciler struct {
	log         *logger.Logger
	lifecycle   *lifecycle.Lifecycle
	kcpHelper   iclient.KcpClientHelper
}

func NewAPIExportPolicyReconciler(log *logger.Logger, fgaClient openfgav1.OpenFGAServiceClient, mcMgr mcmanager.Manager, cfg *config.Config, storeIDGetter fga.StoreIDGetter, kcpClientHelper iclient.KcpClientHelper) *APIExportPolicyReconciler {
	lc := lifecycle.New(mcMgr, "APIExportPolicyReconciler", func() client.Object {
		return &corev1alpha1.APIExportPolicy{}
	}, subroutine.NewAPIExportPolicySubroutine(fgaClient, mcMgr, cfg, storeIDGetter, kcpClientHelper)).
		WithConditions(conditions.NewManager())

	return &APIExportPolicyReconciler{
		log:       log,
		lifecycle: lc,
		kcpHelper: kcpClientHelper,
	}
}

func (r *APIExportPolicyReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	return r.lifecycle.Reconcile(ctx, req)
}

func (r *APIExportPolicyReconciler) SetupWithManager(mgr mcmanager.Manager, cfg *platformeshconfig.CommonServiceConfig, operatorCfg *config.Config, evp ...predicate.Predicate) error {
	opts := controller.TypedOptions[mcreconcile.Request]{
		MaxConcurrentReconciles: cfg.MaxConcurrentReconciles,
	}
	predicates := append([]predicate.Predicate{filter.DebugResourcesBehaviourPredicate(cfg.DebugLabelValue)}, evp...)

	return mcbuilder.ControllerManagedBy(mgr).
		Named("apiexportpolicy").
		For(&corev1alpha1.APIExportPolicy{}).
		WithOptions(opts).
		WithEventFilter(predicate.And(predicates...)).
		Watches(
			&kcptenancyv1alpha1.Workspace{},
			func(_ multicluster.ClusterName, _ cluster.Cluster) ctrhandler.TypedEventHandler[client.Object, mcreconcile.Request] {
				return handler.TypedEnqueueRequestsFromMapFuncWithClusterPreservation(func(ctx context.Context, obj client.Object) []mcreconcile.Request {
					ws, ok := obj.(*kcptenancyv1alpha1.Workspace)
					if !ok {
						return nil
					}

					// we need to enqueue only when a new org appears
					if ws.Spec.Type.Path != orgsWorkspacePath || ws.Status.Phase != readyPhase {
						return nil
					}

					// List all APIExportPolicy resources and enqueue those with root:orgs:* expression
					return r.enqueueAllAPIExportPolicies(ctx, mgr, operatorCfg)
				})
			},
		).Complete(r)
}

func (r *APIExportPolicyReconciler) enqueueAllAPIExportPolicies(ctx context.Context, mgr mcmanager.Manager, cfg *config.Config) []mcreconcile.Request {
	var policies corev1alpha1.APIExportPolicyList
	if err := r.kcpHelper.List(ctx, &policies); err != nil {
		r.log.Error().Err(err).Msg("failed to list APIExportPolicy resources")
		return nil
	}

	var requests []mcreconcile.Request
	for _, policy := range policies.Items {
		// Check if policy has root:orgs:* expression
		for _, expr := range policy.Spec.AllowPathExpressions {
			trimmedExpr := strings.TrimPrefix(expr, ":")

			if trimmedExpr == "root:orgs:*" {
				clusterName := logicalcluster.From(&policy)
				requests = append(requests, mcreconcile.Request{
					Request: reconcile.Request{
						NamespacedName: types.NamespacedName{
							Name: policy.Name,
						},
					},
					ClusterName: multicluster.ClusterName(clusterName.String()),
				})
				break
			}
		}
	}
	return requests
}

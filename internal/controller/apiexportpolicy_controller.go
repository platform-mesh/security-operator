package controller

import (
	"context"
	"strings"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	platformeshconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/builder"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/multicluster"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/logger"
	corev1alpha1 "github.com/platform-mesh/security-operator/api/v1alpha1"
	iclient "github.com/platform-mesh/security-operator/internal/client"
	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/security-operator/internal/subroutine"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	ctrhandler "sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	"sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/kcp-dev/logicalcluster/v3"
	kcptenancyv1alpha1 "github.com/kcp-dev/sdk/apis/tenancy/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	orgsWorkspacePath = "root:orgs"
	readyPhase        = "Ready"
)

type APIExportPolicyReconciler struct {
	log         *logger.Logger
	mclifecycle *multicluster.LifecycleManager
}

func NewAPIExportPolicyReconciler(log *logger.Logger, fga openfgav1.OpenFGAServiceClient, mcMgr mcmanager.Manager) *APIExportPolicyReconciler {
	return &APIExportPolicyReconciler{
		log: log,
		mclifecycle: builder.NewBuilder("apiexportpolicy", "APIExportPolicyReconciler", []lifecyclesubroutine.Subroutine{
			subroutine.NewAPIExportPolicySubroutine(fga, mcMgr),
		}, log).
			WithConditionManagement().
			BuildMultiCluster(mcMgr),
	}
}

func (r *APIExportPolicyReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	ctxWithCluster := mccontext.WithCluster(ctx, req.ClusterName)
	return r.mclifecycle.Reconcile(ctxWithCluster, req, &corev1alpha1.APIExportPolicy{})
}

func (r *APIExportPolicyReconciler) SetupWithManager(mgr mcmanager.Manager, cfg *platformeshconfig.CommonServiceConfig, operatorCfg *config.Config, evp ...predicate.Predicate) error {
	bld, err := r.mclifecycle.SetupWithManagerBuilder(mgr, cfg.MaxConcurrentReconciles, "apiexportpolicy", &corev1alpha1.APIExportPolicy{}, cfg.DebugLabelValue, r.log, evp...)
	if err != nil {
		return err
	}
	return bld.
		Watches(
			&kcptenancyv1alpha1.Workspace{},
			func(clusterName string, c cluster.Cluster) ctrhandler.TypedEventHandler[client.Object, mcreconcile.Request] {
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
	allClient, err := iclient.GetAllClient(ctx, mgr.GetLocalManager().GetConfig(), mgr.GetLocalManager().GetScheme(), cfg.AuthorizationAPIExportEndpointSliceName)
	if err != nil {
		r.log.Error().Err(err).Msg("failed to create all-cluster client for APIExportPolicy listing")
		return nil
	}

	var policies corev1alpha1.APIExportPolicyList
	if err := allClient.List(ctx, &policies); err != nil {
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
					ClusterName: clusterName.String(),
				})
				break
			}
		}
	}
	return requests
}

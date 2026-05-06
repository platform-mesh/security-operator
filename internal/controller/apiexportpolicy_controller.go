package controller

import (
	"context"
	"strings"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	accountsv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
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

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kcp-dev/logicalcluster/v3"
)

type APIExportPolicyReconciler struct {
	log       *logger.Logger
	lifecycle *lifecycle.Lifecycle
	kcpHelper iclient.KcpClientHelper
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

func (r *APIExportPolicyReconciler) SetupWithManager(mgr mcmanager.Manager, cfg *platformeshconfig.CommonServiceConfig, evp ...predicate.Predicate) error {
	opts := controller.TypedOptions[mcreconcile.Request]{
		MaxConcurrentReconciles: cfg.MaxConcurrentReconciles,
	}
	predicates := append([]predicate.Predicate{filter.DebugResourcesBehaviourPredicate(cfg.DebugLabelValue)}, evp...)

	return mcbuilder.ControllerManagedBy(mgr).
		Named("apiexportpolicy").
		For(&corev1alpha1.APIExportPolicy{},
			mcbuilder.WithClusterFilter(func(clusterName multicluster.ClusterName, _ cluster.Cluster) bool {
				return strings.HasPrefix(string(clusterName), config.SystemProviderName)
			}),
		).
		WithOptions(opts).
		WithEventFilter(predicate.And(predicates...)).
		Watches(
			&accountsv1alpha1.Account{},
			func(_ multicluster.ClusterName, _ cluster.Cluster) ctrhandler.TypedEventHandler[client.Object, mcreconcile.Request] {
				return handler.TypedEnqueueRequestsFromMapFuncWithClusterPreservation(func(ctx context.Context, obj client.Object) []mcreconcile.Request {
					acc, ok := obj.(*accountsv1alpha1.Account)
					if !ok {
						return nil
					}

					// we need to enqueue only when a new org is ready
					if acc.Spec.Type != accountsv1alpha1.AccountTypeOrg || meta.FindStatusCondition(acc.GetConditions(), "Ready").Status != metav1.ConditionTrue {
						return nil
					}

					// List all APIExportPolicy resources and enqueue those with root:orgs:* expression
					return r.enqueueAllAPIExportPolicies(ctx, mgr)
				})
			},
			mcbuilder.WithClusterFilter(func(clusterName multicluster.ClusterName, _ cluster.Cluster) bool {
				return strings.HasPrefix(string(clusterName), config.CoreProviderName)
			}),
		).Complete(r)
}

func (r *APIExportPolicyReconciler) enqueueAllAPIExportPolicies(ctx context.Context, mgr mcmanager.Manager) []mcreconcile.Request {
	var policies corev1alpha1.APIExportPolicyList

	cluster, err := mgr.GetCluster(ctx, multiProviderName(config.SystemProviderName, config.OrgsClusterPath))
	if err != nil {
		r.log.Error().Err(err).Msg("failed to get root:orgs cluster")
		return nil
	}

	err = cluster.GetClient().List(ctx, &policies)
	if err != nil {
		r.log.Error().Err(err).Msg("failed to list all APIExportPolicies")
		return nil
	}

	var requests []mcreconcile.Request
	for _, policy := range policies.Items {
		// Check if policy has root:orgs:* expression
		for _, expr := range policy.Spec.AllowPathExpressions {
			trimmedExpr := strings.TrimPrefix(expr, ":")

			if trimmedExpr == "root:orgs:*" {
				clusterName := multiProviderName(config.SystemProviderName, logicalcluster.From(&policy).String())
				requests = append(requests, mcreconcile.Request{
					Request: reconcile.Request{
						NamespacedName: types.NamespacedName{
							Name: policy.Name,
						},
					},
					ClusterName: clusterName,
				})
				break
			}
		}
	}
	return requests
}

// multiProviderName returns a cluster name with provider prefix and separator for multi provider.
// The multi.Provider prefixes cluster names as "providerName#clusterName"
func multiProviderName(providerName, clusterName string) multicluster.ClusterName {
	return multicluster.ClusterName(providerName + config.ProviderSeparator + clusterName)
}

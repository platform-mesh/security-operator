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
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
)

// StoreReconciler reconciles a Store object
type StoreReconciler struct {
	fga       openfgav1.OpenFGAServiceClient
	log       *logger.Logger
	lifecycle *lifecycle.Lifecycle
}

func NewStoreReconciler(ctx context.Context, log *logger.Logger, fga openfgav1.OpenFGAServiceClient, mcMgr mcmanager.Manager, cfg *config.Config) *StoreReconciler {
	allClient, err := iclient.GetAllClient(ctx, mcMgr.GetLocalManager().GetConfig(), mcMgr.GetLocalManager().GetScheme(), cfg.APIExportEndpointSlices.CorePlatformMeshIO)
	if err != nil {
		log.Fatal().Err(err).Msg("unable to create new client")
	}
	kcpClientHelper := iclient.NewKcpHelper(mcMgr.GetLocalManager().GetConfig(), mcMgr.GetLocalManager().GetScheme())

	lc := lifecycle.New(mcMgr, "StoreReconciler", func() client.Object {
		return &corev1alpha1.Store{}
	},
		subroutine.NewStoreSubroutine(fga, mcMgr),
		subroutine.NewAuthorizationModelSubroutine(fga, mcMgr, allClient, func(cfg *rest.Config) discovery.DiscoveryInterface {
			return discovery.NewDiscoveryClientForConfigOrDie(cfg)
		}, log),
		subroutine.NewTupleSubroutine(fga, mcMgr, kcpClientHelper),
	).WithConditions(conditions.NewManager())

	return &StoreReconciler{
		fga:       fga,
		log:       log,
		lifecycle: lc,
	}
}

func (r *StoreReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	return r.lifecycle.Reconcile(ctx, req)
}

// SetupWithManager sets up the controller with the Manager.
func (r *StoreReconciler) SetupWithManager(mgr mcmanager.Manager, cfg *platformeshconfig.CommonServiceConfig, evp ...predicate.Predicate) error {
	predicates := append([]predicate.Predicate{filter.DebugResourcesBehaviourPredicate(cfg.DebugLabelValue)}, evp...)
	b := mcbuilder.ControllerManagedBy(mgr).
		Named("store").
		For(&corev1alpha1.Store{},
			mcbuilder.WithClusterFilter(func(clusterName string, _ cluster.Cluster) bool {
				return strings.HasPrefix(clusterName, config.SystemProviderName)
			}),
		).
		WithOptions(controller.TypedOptions[mcreconcile.Request]{MaxConcurrentReconciles: cfg.MaxConcurrentReconciles}).
		WithEventFilter(predicate.And(predicates...))

	return b.
		Watches(
			&corev1alpha1.AuthorizationModel{},
			func(clusterName string, c cluster.Cluster) ctrhandler.TypedEventHandler[client.Object, mcreconcile.Request] {
				return handler.TypedEnqueueRequestsFromMapFuncWithClusterPreservation(func(ctx context.Context, obj client.Object) []mcreconcile.Request {
					model, ok := obj.(*corev1alpha1.AuthorizationModel)
					if !ok {
						return nil
					}
					// stores are engaged by system provider, to trigger a reconciliation with multi provider
					// it's required to use provider's prefix for request
					storeClusterName := multiProviderName(config.SystemProviderName, model.Spec.StoreRef.Cluster)

					return []mcreconcile.Request{
						{
							Request: reconcile.Request{
								NamespacedName: types.NamespacedName{
									Name: model.Spec.StoreRef.Name,
								},
							},
							ClusterName: storeClusterName,
						},
					}
				})
			},
			mcbuilder.WithPredicates(predicate.GenerationChangedPredicate{}),
			mcbuilder.WithClusterFilter(func(clusterName string, _ cluster.Cluster) bool {
				return strings.HasPrefix(clusterName, config.CoreProviderName)
			}),
		).Complete(r)
}

// multiProviderName returns a cluster name with provider prefix and separator for multi provider.
// The multi.Provider prefixes cluster names as "providerName#clusterName"
func multiProviderName(providerName, clusterName string) string {
	return providerName + config.ProviderSeparator + clusterName
}

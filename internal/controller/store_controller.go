package controller

import (
	"context"

	"github.com/platform-mesh/golang-commons/controller/lifecycle/builder"
	"github.com/rs/zerolog/log"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	kcpcorev1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	//"github.com/kcp-dev/logicalcluster/v3"
	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	platformeshconfig "github.com/platform-mesh/golang-commons/config"
	lifecyclecontrollerruntime "github.com/platform-mesh/golang-commons/controller/lifecycle/multicluster"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/logger"
	corev1alpha1 "github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/subroutine"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/multicluster-runtime/pkg/handler"
)

// StoreReconciler reconciles a Store object
type StoreReconciler struct {
	lcClientFunc subroutine.NewLogicalClusterClientFunc
	fga          openfgav1.OpenFGAServiceClient
	log          *logger.Logger
	lifecycle    *lifecyclecontrollerruntime.LifecycleManager
}

func NewStoreReconciler(log *logger.Logger, fga openfgav1.OpenFGAServiceClient, lcClientFunc subroutine.NewLogicalClusterClientFunc, mcMgr mcmanager.Manager) *StoreReconciler {
	return &StoreReconciler{
		lcClientFunc: lcClientFunc,
		fga:          fga,
		log:          log,
		lifecycle: builder.NewBuilder("store", "StoreReconciler", []lifecyclesubroutine.Subroutine{
			subroutine.NewStoreSubroutine(fga, mcMgr, lcClientFunc),
			subroutine.NewAuthorizationModelSubroutine(fga, mcMgr, lcClientFunc),
			subroutine.NewTupleSubroutine(fga, mcMgr),
		}, log).WithConditionManagement().
			BuildMultiCluster(mcMgr),
	}
}

func (r *StoreReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	ctxWithCluster := mccontext.WithCluster(ctx, req.ClusterName)
	return r.lifecycle.Reconcile(ctxWithCluster, req, &corev1alpha1.Store{})
}

// SetupWithManager sets up the controller with the Manager.
func (r *StoreReconciler) SetupWithManager(mgr mcmanager.Manager, cfg *platformeshconfig.CommonServiceConfig, evp ...predicate.Predicate) error { // coverage-ignore
	builder, err := r.lifecycle.SetupWithManagerBuilder(mgr, cfg.MaxConcurrentReconciles, "store", &corev1alpha1.Store{}, cfg.DebugLabelValue, r.log, evp...)
	if err != nil {
		return err
	}
	return builder.
		Watches(
			&corev1alpha1.AuthorizationModel{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				model := obj.(*corev1alpha1.AuthorizationModel)

				cluster, err := mgr.GetCluster(ctx, model.Spec.StoreRef.Path)
				if err != nil {
					log.Error().Err(err).Msg("failed to get cluster from manager (store watcher)")
					return nil
				}

				var lc kcpcorev1alpha1.LogicalCluster
				err = cluster.GetClient().Get(ctx, client.ObjectKey{Name: "cluster"}, &lc)
				if err != nil {
					log.Error().Err(err).Msg("failed to get logical cluster")
					return nil
				}

				return []reconcile.Request{
					{
						NamespacedName: types.NamespacedName{
							Name: model.Spec.StoreRef.Name,
						},
					},
				}
			}),
			mcbuilder.WithPredicates(predicate.GenerationChangedPredicate{}),
		).Complete(r)
}

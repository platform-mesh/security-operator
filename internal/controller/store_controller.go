package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	kcpcorev1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"
	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	platformeshconfig "github.com/platform-mesh/golang-commons/config"
	lifecyclecontrollerruntime "github.com/platform-mesh/golang-commons/controller/lifecycle/controllerruntime"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/logger"
	corev1alpha1 "github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/subroutine"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/multicluster-runtime/pkg/handler"
)

// StoreReconciler reconciles a Store object
type StoreReconciler struct {
	lcClientFunc subroutine.NewLogicalClusterClientFunc
	mcMgr        mcmanager.Manager
	fga          openfgav1.OpenFGAServiceClient
	log          *logger.Logger
}

func NewStoreReconciler(log *logger.Logger, clt client.Client, fga openfgav1.OpenFGAServiceClient, lcClientFunc subroutine.NewLogicalClusterClientFunc, mcMgr mcmanager.Manager) *StoreReconciler {
	return &StoreReconciler{
		lcClientFunc: lcClientFunc,
		mcMgr:        mcMgr,
		fga:          fga,
		log:          log,
	}
}

func (r *StoreReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cluster, err := r.mcMgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}
	clusterClient := cluster.GetClient()
	// //TODO use kontext from multi-cluster runtime as it suggested in Complete function

	lm := lifecyclecontrollerruntime.NewLifecycleManager(
		[]lifecyclesubroutine.Subroutine{
			subroutine.NewStoreSubroutine(r.fga, clusterClient, r.lcClientFunc),
			subroutine.NewAuthorizationModelSubroutine(r.fga, clusterClient, r.lcClientFunc),
			subroutine.NewTupleSubroutine(r.fga, clusterClient, r.lcClientFunc),
		},
		"store",
		"StoreReconciler",
		cluster.GetClient(),
		r.log,
	).WithConditionManagement()

	ctxWithCluster := mccontext.WithCluster(ctx, req.ClusterName)
	return lm.Reconcile(ctxWithCluster, req.Request, &corev1alpha1.Store{})
}

// SetupWithManager sets up the controller with the Manager.
func (r *StoreReconciler) SetupWithManager(mgr ctrl.Manager, cfg *platformeshconfig.CommonServiceConfig, log *logger.Logger) error { // coverage-ignore
	builder := mcbuilder.ControllerManagedBy(r.mcMgr).For(&corev1alpha1.Store{})
	return builder.
		Watches(
			&corev1alpha1.AuthorizationModel{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				model := obj.(*corev1alpha1.AuthorizationModel)

				lcClient, err := r.lcClientFunc(logicalcluster.Name(model.Spec.StoreRef.Path))
				if err != nil {
					log.Error().Err(err).Msg("failed to get logical cluster client")
					return nil
				}

				var lc kcpcorev1alpha1.LogicalCluster
				err = lcClient.Get(ctx, client.ObjectKey{Name: "cluster"}, &lc)
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
		).Complete(r)
}

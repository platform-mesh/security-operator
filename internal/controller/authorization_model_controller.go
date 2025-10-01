package controller // coverage-ignore

import (
	"context"

	"github.com/kcp-dev/multicluster-provider/apiexport"
	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	platformeshconfig "github.com/platform-mesh/golang-commons/config"
	lifecyclecontrollerruntime "github.com/platform-mesh/golang-commons/controller/lifecycle/controllerruntime"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/logger"
	corev1alpha1 "github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/subroutine"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
)

type AuthorizationModelReconciler struct {
	provider     *apiexport.Provider
	mcMgr        mcmanager.Manager
	lcClientFunc subroutine.NewLogicalClusterClientFunc
	fga          openfgav1.OpenFGAServiceClient
	log          *logger.Logger
}

func NewAuthorizationModelReconciler(log *logger.Logger, clt client.Client, fga openfgav1.OpenFGAServiceClient, lcClientFunc subroutine.NewLogicalClusterClientFunc, mcMgr mcmanager.Manager, provider *apiexport.Provider) *AuthorizationModelReconciler {
	return &AuthorizationModelReconciler{
		mcMgr:        mcMgr,
		provider:     provider,
		log:          log,
		lcClientFunc: lcClientFunc,
	}
}

func (r *AuthorizationModelReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	// Get a cluster-scoped client for this request
	cluster, err := r.mcMgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}
	clusterClient := cluster.GetClient()

	// Rebuild a lifecycle manager bound to the cluster client
	lm := lifecyclecontrollerruntime.NewLifecycleManager(
		[]lifecyclesubroutine.Subroutine{
			subroutine.NewTupleSubroutine(r.fga, clusterClient, r.lcClientFunc),
		},
		"apibinding",
		"apibinding",
		clusterClient,
		r.log,
	)

	return lm.Reconcile(ctx, ctrl.Request{NamespacedName: req.NamespacedName}, &corev1alpha1.AuthorizationModel{})
}

func (r *AuthorizationModelReconciler) SetupWithManager(mgr ctrl.Manager, cfg *platformeshconfig.CommonServiceConfig, log *logger.Logger) error { // coverage-ignore
	err := mcbuilder.ControllerManagedBy(r.mcMgr).
		For(&corev1alpha1.AuthorizationModel{}).
		Complete(r)
	if err != nil {
		return err
	}

	//Start the apiexport provider with the local manager
	if err := mgr.Add(&providerRunnable{
		provider: r.provider,
		mcMgr:    r.mcMgr,
		log:      r.log,
	}); err != nil {
		r.log.Error().Err(err).Msg("failed to add provider runnable to manager")
		return err
	}
	return err
}

package controller

import (
	"context"

	kcpv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	"github.com/kcp-dev/multicluster-provider/apiexport"
	platformeshconfig "github.com/platform-mesh/golang-commons/config"
	lifecyclecontrollerruntime "github.com/platform-mesh/golang-commons/controller/lifecycle/controllerruntime"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/logger"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/platform-mesh/security-operator/internal/subroutine"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
)

func NewAPIBindingReconciler(cl client.Client, logger *logger.Logger, lcClientFunc subroutine.NewLogicalClusterClientFunc, mcMgr mcmanager.Manager, provider *apiexport.Provider) *APIBindingReconciler {
	return &APIBindingReconciler{
		manager:      mcMgr.GetLocalManager(), // Use the local manager directly
		provider:     provider,
		mcMgr:        mcMgr,
		log:          logger,
		lcClientFunc: lcClientFunc,
	}
}

type APIBindingReconciler struct {
	log          *logger.Logger
	manager      ctrl.Manager // Local controller-runtime manager
	provider     *apiexport.Provider
	mcMgr        mcmanager.Manager
	lcClientFunc subroutine.NewLogicalClusterClientFunc
}

func (r *APIBindingReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cluster, err := r.mcMgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}
	clusterClient := cluster.GetClient()

	// Rebuild a lifecycle manager bound to the cluster client
	lm := lifecyclecontrollerruntime.NewLifecycleManager(
		[]lifecyclesubroutine.Subroutine{
			subroutine.NewAuthorizationModelGenerationSubroutine(clusterClient, r.lcClientFunc),
		},
		"apibinding",
		"apibinding",
		clusterClient,
		r.log,
	)

	return lm.Reconcile(ctx, ctrl.Request{NamespacedName: req.NamespacedName}, &kcpv1alpha1.APIBinding{})
}

func (r *APIBindingReconciler) SetupWithManager(mgr ctrl.Manager, logger *logger.Logger, cfg *platformeshconfig.CommonServiceConfig) error {
	err := mcbuilder.ControllerManagedBy(r.mcMgr).
		For(&kcpv1alpha1.APIBinding{}).
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

	r.log.Info().Msg("Successfully set up multicluster APIBinding controller and provider")
	return nil
}

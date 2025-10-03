package controller

import (
	"context"

	kcpcorev1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	"github.com/kcp-dev/multicluster-provider/initializingworkspaces"
	lifecyclecontrollerruntime "github.com/platform-mesh/golang-commons/controller/lifecycle/multicluster"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/logger"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/security-operator/internal/subroutine"
)

type LogicalClusterReconciler struct {
	mcMgr           mcmanager.Manager
	provider        *initializingworkspaces.Provider
	cfg             config.Config
	restCfg         *rest.Config
	log             *logger.Logger
	inClusterClient client.Client
	orgClient       client.Client
}

func NewLogicalClusterReconciler(log *logger.Logger, restCfg *rest.Config, orgClient client.Client, cfg config.Config, inClusterClient client.Client, mcMgr mcmanager.Manager, provider *initializingworkspaces.Provider) *LogicalClusterReconciler {
	return &LogicalClusterReconciler{
		mcMgr:           mcMgr,
		provider:        provider,
		cfg:             cfg,
		restCfg:         restCfg,
		log:             log,
		inClusterClient: inClusterClient,
		orgClient:       orgClient,
	}
}

func (r *LogicalClusterReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cluster, err := r.mcMgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}
	clusterClient := cluster.GetClient()

	lm := lifecyclecontrollerruntime.NewLifecycleManager(
		[]lifecyclesubroutine.Subroutine{
			subroutine.NewWorkspaceInitializer(clusterClient, r.orgClient, cluster.GetConfig(), r.cfg),
			subroutine.NewWorkspaceAuthConfigurationSubroutine(r.orgClient, r.cfg),
			subroutine.NewRealmSubroutine(r.inClusterClient, r.cfg.BaseDomain),
		},
		"logicalcluster",
		"LogicalClusterReconciler",
		r.mcMgr,
		r.log,
	)
	return lm.Reconcile(ctx, req, &kcpcorev1alpha1.LogicalCluster{})
}
func (r *LogicalClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	err := mcbuilder.ControllerManagedBy(r.mcMgr).
		For(&kcpcorev1alpha1.LogicalCluster{}).
		Complete(r)
	if err != nil {
		return err
	}

	if err := mgr.Add(&providerRunnable{
		provider: r.provider,
		mcMgr:    r.mcMgr,
		log:      r.log,
	}); err != nil {
		r.log.Error().Err(err).Msg("failed to add provider runnable to manager")
		return err
	}

	r.log.Info().Msg("Successfully set up multicluster Initializing controller and provider")
	return nil
}

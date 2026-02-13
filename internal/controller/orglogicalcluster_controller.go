package controller

import (
	"context"

	platformeshconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/builder"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/multicluster"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/security-operator/internal/subroutine"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

type OrgLogicalClusterReconciler struct {
	log *logger.Logger

	mclifecycle *multicluster.LifecycleManager
}

func NewOrgLogicalClusterReconciler(log *logger.Logger, orgClient client.Client, cfg config.Config, inClusterClient client.Client, mgr mcmanager.Manager) *OrgLogicalClusterReconciler {
	var subroutines []lifecyclesubroutine.Subroutine

	if cfg.Initializer.WorkspaceInitializerEnabled {
		subroutines = append(subroutines, subroutine.NewWorkspaceInitializer(orgClient, cfg, mgr, cfg.FGA.CreatorRelation, cfg.FGA.ObjectType))
	}
	if cfg.Initializer.IDPEnabled {
		subroutines = append(subroutines, subroutine.NewIDPSubroutine(orgClient, mgr, cfg))
	}
	if cfg.Initializer.InviteEnabled {
		subroutines = append(subroutines, subroutine.NewInviteSubroutine(orgClient, mgr))
	}
	if cfg.Initializer.WorkspaceAuthEnabled {
		subroutines = append(subroutines, subroutine.NewWorkspaceAuthConfigurationSubroutine(orgClient, inClusterClient, mgr, cfg))
	}
	// RemoveInitializer is always included - it's the final cleanup step
	subroutines = append(subroutines, subroutine.NewRemoveInitializer(mgr, cfg))

	return &OrgLogicalClusterReconciler{
		log: log,
		mclifecycle: builder.NewBuilder("logicalcluster", "OrgLogicalClusterReconciler", subroutines, log).
			WithReadOnly().
			WithStaticThenExponentialRateLimiter().
			BuildMultiCluster(mgr),
	}
}

func (r *OrgLogicalClusterReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	ctxWithCluster := mccontext.WithCluster(ctx, req.ClusterName)
	return r.mclifecycle.Reconcile(ctxWithCluster, req, &kcpcorev1alpha1.LogicalCluster{})
}

func (r *OrgLogicalClusterReconciler) SetupWithManager(mgr mcmanager.Manager, cfg *platformeshconfig.CommonServiceConfig, evp ...predicate.Predicate) error {
	return r.mclifecycle.SetupWithManager(mgr, cfg.MaxConcurrentReconciles, "LogicalCluster", &kcpcorev1alpha1.LogicalCluster{}, cfg.DebugLabelValue, r, r.log, evp...)
}

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
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	mcclient "github.com/kcp-dev/multicluster-provider/client"
	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

// AccountLogicalClusterTerminator acts as a terminator for account workspaces.
type AccountLogicalClusterTerminator struct {
	log *logger.Logger

	mclifecycle *multicluster.LifecycleManager
}

// NewAccountLogicalClusterTerminator returns a new AccountLogicalClusterTerminator.
func NewAccountLogicalClusterTerminator(log *logger.Logger, cfg config.Config, mcc mcclient.ClusterClient, mgr mcmanager.Manager) *AccountLogicalClusterTerminator {
	return &AccountLogicalClusterTerminator{
		log: log,
		mclifecycle: builder.NewBuilder("security", "AccountLogicalClusterTerminator", []lifecyclesubroutine.Subroutine{
			subroutine.NewAccountTuplesTerminatorSubroutine(mcc, mgr),
			subroutine.NewRemoveTerminator(mgr, cfg),
		}, log).
			WithReadOnly().
			WithStaticThenExponentialRateLimiter().
			BuildMultiCluster(mgr),
	}
}

// Reconcile implements the reconcile logic.
func (r *AccountLogicalClusterTerminator) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	ctxWithCluster := mccontext.WithCluster(ctx, req.ClusterName)
	return r.mclifecycle.Reconcile(ctxWithCluster, req, &kcpcorev1alpha1.LogicalCluster{})
}

// SetupWithManager registers the controller with the manager.
func (r *AccountLogicalClusterTerminator) SetupWithManager(mgr mcmanager.Manager, cfg *platformeshconfig.CommonServiceConfig, evp ...predicate.Predicate) error {
	return r.mclifecycle.SetupWithManager(mgr, cfg.MaxConcurrentReconciles, "AccountLogicalClusterTerminator", &kcpcorev1alpha1.LogicalCluster{}, cfg.DebugLabelValue, r, r.log, evp...)
}

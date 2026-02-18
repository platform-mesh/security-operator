package controller

import (
	"context"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
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

// AccountLogicalClusterReconciler acts as an initializer for account workspaces.
type AccountLogicalClusterReconciler struct {
	log *logger.Logger

	mclifecycle *multicluster.LifecycleManager
}

func NewAccountLogicalClusterReconciler(log *logger.Logger, cfg config.Config, fga openfgav1.OpenFGAServiceClient, mcc mcclient.ClusterClient, mgr mcmanager.Manager) *AccountLogicalClusterReconciler {
	return &AccountLogicalClusterReconciler{
		log: log,
		mclifecycle: builder.NewBuilder("security", "AccountLogicalClusterReconciler", []lifecyclesubroutine.Subroutine{
			subroutine.NewAccountTuplesSubroutine(mcc, mgr, fga, cfg.FGA.CreatorRelation, cfg.FGA.ParentRelation, cfg.FGA.ObjectType),
		}, log).
			WithReadOnly().
			WithStaticThenExponentialRateLimiter().
			WithInitializer(cfg.InitializerName()).
			WithTerminator(cfg.TerminatorName()).
			BuildMultiCluster(mgr),
	}
}

func (r *AccountLogicalClusterReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	ctxWithCluster := mccontext.WithCluster(ctx, req.ClusterName)
	return r.mclifecycle.Reconcile(ctxWithCluster, req, &kcpcorev1alpha1.LogicalCluster{})
}

func (r *AccountLogicalClusterReconciler) SetupWithManager(mgr mcmanager.Manager, cfg *platformeshconfig.CommonServiceConfig, evp ...predicate.Predicate) error {
	return r.mclifecycle.SetupWithManager(mgr, cfg.MaxConcurrentReconciles, "AccountLogicalCluster", &kcpcorev1alpha1.LogicalCluster{}, cfg.DebugLabelValue, r, r.log, evp...)
}

package controller

import (
	"context"

	mcclient "github.com/kcp-dev/multicluster-provider/client"
	openfga "github.com/openfga/go-sdk"
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

	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

type AccountLogicalClusterReconciler struct {
	log *logger.Logger

	mclifecycle *multicluster.LifecycleManager
}

func NewAccountLogicalClusterReconciler(log *logger.Logger, fga *openfga.APIClient, cfg config.Config, mcc mcclient.ClusterClient, mgr mcmanager.Manager) *AccountLogicalClusterReconciler {
	return &AccountLogicalClusterReconciler{
		log: log,
		mclifecycle: builder.NewBuilder("security", "AccountLogicalClusterReconciler", []lifecyclesubroutine.Subroutine{
			subroutine.NewAccountTuplesSubroutine(fga, mcc, mgr),
		}, log).
			WithReadOnly().
			WithStaticThenExponentialRateLimiter().
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

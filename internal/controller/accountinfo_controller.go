package controller

import (
	"context"

	accountv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	platformeshconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/builder"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/multicluster"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/security-operator/internal/subroutine"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
)

type AccountInfoReconciler struct {
	log         *logger.Logger
	mclifecycle *multicluster.LifecycleManager
}

func NewAccountInfoReconciler(log *logger.Logger, mcMgr mcmanager.Manager) *AccountInfoReconciler {
	return &AccountInfoReconciler{
		log: log,
		mclifecycle: builder.NewBuilder("accountinfo", "AccountInfoReconciler", []lifecyclesubroutine.Subroutine{
			subroutine.NewAccountInfoFinalizerSubroutine(mcMgr),
		}, log).
			BuildMultiCluster(mcMgr),
	}
}

func (r *AccountInfoReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	ctxWithCluster := mccontext.WithCluster(ctx, req.ClusterName)
	return r.mclifecycle.Reconcile(ctxWithCluster, req, &accountv1alpha1.AccountInfo{})
}

func (r *AccountInfoReconciler) SetupWithManager(mgr mcmanager.Manager, cfg *platformeshconfig.CommonServiceConfig, evp ...predicate.Predicate) error {
	return r.mclifecycle.SetupWithManager(mgr, cfg.MaxConcurrentReconciles, "accountinfo", &accountv1alpha1.AccountInfo{}, cfg.DebugLabelValue, r, r.log, evp...)
}

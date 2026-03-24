package controller

import (
	"context"

	accountv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	platformeshconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/controller/filter"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/security-operator/internal/subroutine"
	"github.com/platform-mesh/subroutines/lifecycle"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
)

type AccountInfoReconciler struct {
	log       *logger.Logger
	lifecycle *lifecycle.Lifecycle
}

func NewAccountInfoReconciler(log *logger.Logger, mcMgr mcmanager.Manager) *AccountInfoReconciler {
	lc := lifecycle.New(mcMgr, "AccountInfoReconciler", func() client.Object {
		return &accountv1alpha1.AccountInfo{}
	}, subroutine.NewAccountInfoFinalizerSubroutine(mcMgr))

	return &AccountInfoReconciler{
		log:       log,
		lifecycle: lc,
	}
}

func (r *AccountInfoReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	return r.lifecycle.Reconcile(ctx, req)
}

func (r *AccountInfoReconciler) SetupWithManager(mgr mcmanager.Manager, cfg *platformeshconfig.CommonServiceConfig, evp ...predicate.Predicate) error {
	opts := controller.TypedOptions[mcreconcile.Request]{
		MaxConcurrentReconciles: cfg.MaxConcurrentReconciles,
	}
	predicates := append([]predicate.Predicate{filter.DebugResourcesBehaviourPredicate(cfg.DebugLabelValue)}, evp...)
	return mcbuilder.ControllerManagedBy(mgr).
		Named("accountinfo").
		For(&accountv1alpha1.AccountInfo{}).
		WithOptions(opts).
		WithEventFilter(predicate.And(predicates...)).
		Complete(r)
}

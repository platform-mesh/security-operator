package controller

import (
	"context"
	"fmt"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	platformeshconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/controller/filter"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/ratelimiter"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/security-operator/internal/subroutine"
	"github.com/platform-mesh/subroutines/lifecycle"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"k8s.io/client-go/util/workqueue"

	mcclient "github.com/kcp-dev/multicluster-provider/client"
	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

// AccountLogicalClusterReconciler acts as an initializer for account workspaces.
type AccountLogicalClusterReconciler struct {
	log         *logger.Logger
	lifecycle   *lifecycle.Lifecycle
	rateLimiter workqueue.TypedRateLimiter[mcreconcile.Request]
}

func NewAccountLogicalClusterReconciler(log *logger.Logger, cfg config.Config, fga openfgav1.OpenFGAServiceClient, mcc mcclient.ClusterClient, mgr mcmanager.Manager) (*AccountLogicalClusterReconciler, error) {
	rl, err := ratelimiter.NewStaticThenExponentialRateLimiter[mcreconcile.Request](ratelimiter.NewConfig())
	if err != nil {
		return nil, fmt.Errorf("creating RateLimiter: %w", err)
	}

	lc := lifecycle.New(mgr, "AccountLogicalClusterReconciler", func() client.Object {
		return &kcpcorev1alpha1.LogicalCluster{}
	}, subroutine.NewAccountTuplesSubroutine(mcc, mgr, fga, cfg.FGA.CreatorRelation, cfg.FGA.ParentRelation, cfg.FGA.ObjectType)).
		// WithReadOnly().
		WithInitializer(cfg.InitializerName()).
		WithTerminator(cfg.TerminatorName())

	return &AccountLogicalClusterReconciler{
		log:         log,
		lifecycle:   lc,
		rateLimiter: rl,
	}, nil
}

func (r *AccountLogicalClusterReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	return r.lifecycle.Reconcile(ctx, req)
}

func (r *AccountLogicalClusterReconciler) SetupWithManager(mgr mcmanager.Manager, cfg *platformeshconfig.CommonServiceConfig, evp ...predicate.Predicate) error {
	opts := controller.TypedOptions[mcreconcile.Request]{
		MaxConcurrentReconciles: cfg.MaxConcurrentReconciles,
		RateLimiter:             r.rateLimiter,
	}
	predicates := append([]predicate.Predicate{filter.DebugResourcesBehaviourPredicate(cfg.DebugLabelValue)}, evp...)
	return mcbuilder.ControllerManagedBy(mgr).
		Named("AccountLogicalCluster").
		For(&kcpcorev1alpha1.LogicalCluster{}).
		WithOptions(opts).
		WithEventFilter(predicate.And(predicates...)).
		Complete(r)
}

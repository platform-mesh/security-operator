package controller

import (
	"context"
	"fmt"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	platformeshconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/controller/filter"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/ratelimiter"
	"github.com/platform-mesh/golang-commons/logger"
	iclient "github.com/platform-mesh/security-operator/internal/client"
	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/security-operator/internal/fga"
	ipredicates "github.com/platform-mesh/security-operator/internal/predicates"
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

	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

// AccountLogicalClusterReconciler acts as an initializer for account workspaces.
type AccountLogicalClusterReconciler struct {
	log             *logger.Logger
	lifecycle       *lifecycle.Lifecycle
	rateLimiter     workqueue.TypedRateLimiter[mcreconcile.Request]
	initializerName string
}

func NewAccountLogicalClusterReconciler(log *logger.Logger, cfg config.Config, fgaClient openfgav1.OpenFGAServiceClient, storeIDGetter fga.StoreIDGetter, mgr mcmanager.Manager) (*AccountLogicalClusterReconciler, error) {
	rl, err := ratelimiter.NewStaticThenExponentialRateLimiter[mcreconcile.Request](ratelimiter.NewConfig())
	if err != nil {
		return nil, fmt.Errorf("creating RateLimiter: %w", err)
	}

	kcpClientHelper := iclient.NewKcpHelper(mgr.GetLocalManager().GetConfig(), mgr.GetLocalManager().GetScheme())
	lc := lifecycle.New(mgr, "AccountLogicalClusterReconciler", func() client.Object {
		return &kcpcorev1alpha1.LogicalCluster{}
	}, subroutine.NewAccountTuplesSubroutine(mgr, fgaClient, storeIDGetter, cfg.FGA.CreatorRelation, cfg.FGA.ParentRelation, cfg.FGA.ObjectType, kcpClientHelper))

	return &AccountLogicalClusterReconciler{
		log:             log,
		lifecycle:       lc,
		rateLimiter:     rl,
		initializerName: cfg.InitializerName(),
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
	predicates := append([]predicate.Predicate{filter.DebugResourcesBehaviourPredicate(cfg.DebugLabelValue), ipredicates.HasInitializerPredicate(r.initializerName)}, evp...)
	return mcbuilder.ControllerManagedBy(mgr).
		Named("AccountLogicalClusterReconciler").
		For(&kcpcorev1alpha1.LogicalCluster{}).
		WithOptions(opts).
		WithEventFilter(predicate.And(predicates...)).
		Complete(r)
}

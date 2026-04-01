package controller

import (
	"context"
	"fmt"

	platformeshconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/controller/filter"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/ratelimiter"
	"github.com/platform-mesh/golang-commons/logger"
	iclient "github.com/platform-mesh/security-operator/internal/client"
	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/security-operator/internal/subroutine"
	"github.com/platform-mesh/subroutines"
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

type OrgLogicalClusterInitializer struct {
	log *logger.Logger

	lifecycle   *lifecycle.Lifecycle
	rateLimiter workqueue.TypedRateLimiter[mcreconcile.Request]
}

func NewOrgLogicalClusterInitializer(log *logger.Logger, orgClient client.Client, cfg config.Config, inClusterClient client.Client, mgr mcmanager.Manager) (*OrgLogicalClusterInitializer, error) {
	rl, err := ratelimiter.NewStaticThenExponentialRateLimiter[mcreconcile.Request](ratelimiter.NewConfig())
	if err != nil {
		return nil, fmt.Errorf("creating RateLimiter: %w", err)
	}
	kcpClientHelper := iclient.NewKcpHelper(mgr.GetLocalManager().GetConfig(), mgr.GetLocalManager().GetScheme())

	var subroutines []subroutines.Subroutine

	if cfg.Initializer.WorkspaceInitializerEnabled {
		subroutines = append(subroutines, subroutine.NewWorkspaceInitializer(orgClient, cfg, mgr, cfg.FGA.CreatorRelation, cfg.FGA.ObjectType, kcpClientHelper))
	}
	if cfg.Initializer.IDPEnabled {
		idpSub, err := subroutine.NewIDPSubroutine(orgClient, mgr, cfg)
		if err != nil {
			return nil, fmt.Errorf("creating IDP subroutine: %w", err)
		}
		subroutines = append(subroutines, idpSub)
	}
	if cfg.Initializer.InviteEnabled {
		inviteSub, err := subroutine.NewInviteSubroutine(orgClient, mgr)
		if err != nil {
			return nil, fmt.Errorf("creating Invite subroutine: %w", err)
		}
		subroutines = append(subroutines, inviteSub)
	}
	if cfg.Initializer.WorkspaceAuthEnabled {
		subroutines = append(subroutines, subroutine.NewWorkspaceAuthConfigurationSubroutine(orgClient, inClusterClient, mgr, cfg))
	}

	lc := lifecycle.New(mgr, "OrgLogicalClusterInitializer", func() client.Object {
		return &kcpcorev1alpha1.LogicalCluster{}
	}, subroutines...).
		WithInitializer(cfg.InitializerName()).
		WithTerminator(cfg.TerminatorName())

	return &OrgLogicalClusterInitializer{
		log:         log,
		lifecycle:   lc,
		rateLimiter: rl,
	}, nil
}

func (r *OrgLogicalClusterInitializer) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	return r.lifecycle.Reconcile(ctx, req)
}

func (r *OrgLogicalClusterInitializer) SetupWithManager(mgr mcmanager.Manager, cfg *platformeshconfig.CommonServiceConfig, evp ...predicate.Predicate) error {
	opts := controller.TypedOptions[mcreconcile.Request]{
		MaxConcurrentReconciles: cfg.MaxConcurrentReconciles,
		RateLimiter:             r.rateLimiter,
	}
	predicates := append([]predicate.Predicate{filter.DebugResourcesBehaviourPredicate(cfg.DebugLabelValue)}, evp...)
	return mcbuilder.ControllerManagedBy(mgr).
		Named("OrgLogicalClusterInitializer").
		For(&kcpcorev1alpha1.LogicalCluster{}).
		WithOptions(opts).
		WithEventFilter(predicate.And(predicates...)).
		Complete(r)
}

package controller // coverage-ignore

import (
	"context"

	platformeshconfig "github.com/platform-mesh/golang-commons/config"
	lifecyclecontrollerruntime "github.com/platform-mesh/golang-commons/controller/lifecycle/controllerruntime"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/security-operator/internal/subroutine/invite"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/kcp"
)

type InviteReconciler struct {
	lifecycle *lifecyclecontrollerruntime.LifecycleManager
}

func NewInviteReconciler(cl client.Client, cfg *config.Config, log *logger.Logger) *InviteReconciler {
	inviteSubroutine, err := invite.New(cfg, cl)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create invite subroutine")
	}

	return &InviteReconciler{
		lifecycle: lifecyclecontrollerruntime.NewLifecycleManager(
			[]lifecyclesubroutine.Subroutine{
				inviteSubroutine,
			},
			"invite",
			"InviteReconciler",
			cl,
			log,
		),
	}
}

func (r *InviteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.lifecycle.Reconcile(ctx, req, &v1alpha1.Invite{})
}

func (r *InviteReconciler) SetupWithManager(mgr ctrl.Manager, cfg *platformeshconfig.CommonServiceConfig, log *logger.Logger) error { // coverage-ignore
	return r.lifecycle.
		WithConditionManagement().
		SetupWithManager(
			mgr,
			cfg.MaxConcurrentReconciles,
			"invite",
			&v1alpha1.Invite{},
			cfg.DebugLabelValue,
			kcp.WithClusterInContext(r),
			log,
		)
}

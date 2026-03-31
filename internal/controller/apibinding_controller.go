package controller

import (
	"context"

	platformeshconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/controller/filter"
	"github.com/platform-mesh/golang-commons/logger"
	iclient "github.com/platform-mesh/security-operator/internal/client"
	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/security-operator/internal/subroutine"
	"github.com/platform-mesh/subroutines/lifecycle"
	"github.com/rs/zerolog/log"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	kcpapisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
)

func NewAPIBindingReconciler(ctx context.Context, logger *logger.Logger, mcMgr mcmanager.Manager, cfg *config.Config) *APIBindingReconciler {
	allclient, err := iclient.GetAllClient(ctx, mcMgr.GetLocalManager().GetConfig(), mcMgr.GetLocalManager().GetScheme(), cfg.APIExportEndpointSlices.CorePlatformMeshIO)
	if err != nil {
		log.Fatal().Err(err).Msg("unable to create new client")
	}

	lc := lifecycle.New(mcMgr, "APIBindingReconciler", func() client.Object {
		return &kcpapisv1alpha2.APIBinding{}
	}, subroutine.NewAuthorizationModelGenerationSubroutine(mcMgr, allclient))

	return &APIBindingReconciler{
		log:       logger,
		lifecycle: lc,
	}
}

type APIBindingReconciler struct {
	log       *logger.Logger
	lifecycle *lifecycle.Lifecycle
}

func (r *APIBindingReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	return r.lifecycle.Reconcile(ctx, req)
}

func (r *APIBindingReconciler) SetupWithManager(mgr mcmanager.Manager, cfg *platformeshconfig.CommonServiceConfig, evp ...predicate.Predicate) error {
	opts := controller.TypedOptions[mcreconcile.Request]{
		MaxConcurrentReconciles: cfg.MaxConcurrentReconciles,
	}
	predicates := append([]predicate.Predicate{filter.DebugResourcesBehaviourPredicate(cfg.DebugLabelValue)}, evp...)
	return mcbuilder.ControllerManagedBy(mgr).
		Named("apibinding").
		For(&kcpapisv1alpha2.APIBinding{}).
		WithOptions(opts).
		WithEventFilter(predicate.And(predicates...)).
		Complete(r)
}

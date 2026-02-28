package controller

import (
	"context"

	platformeshconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/builder"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/multicluster"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/logger"
	iclient "github.com/platform-mesh/security-operator/internal/client"
	"github.com/platform-mesh/security-operator/internal/subroutine"
	"github.com/rs/zerolog/log"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	kcpapisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
)

func NewAPIBindingReconciler(ctx context.Context, logger *logger.Logger, mcMgr mcmanager.Manager) *APIBindingReconciler {
	allclient, err := iclient.NewForAllPlatformMeshResources(ctx, mcMgr.GetLocalManager().GetConfig(), mcMgr.GetLocalManager().GetScheme())
	if err != nil {
		log.Fatal().Err(err).Msg("unable to create new client")
	}

	return &APIBindingReconciler{
		log: logger,
		mclifecycle: builder.NewBuilder("apibinding", "apibinding-controller", []lifecyclesubroutine.Subroutine{
			subroutine.NewAuthorizationModelGenerationSubroutine(mcMgr, allclient),
		}, logger).
			BuildMultiCluster(mcMgr),
	}
}

type APIBindingReconciler struct {
	log         *logger.Logger
	mclifecycle *multicluster.LifecycleManager
}

func (r *APIBindingReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	ctxWithCluster := mccontext.WithCluster(ctx, req.ClusterName)
	return r.mclifecycle.Reconcile(ctxWithCluster, req, &kcpapisv1alpha2.APIBinding{})
}

func (r *APIBindingReconciler) SetupWithManager(mgr mcmanager.Manager, cfg *platformeshconfig.CommonServiceConfig, evp ...predicate.Predicate) error {
	return r.mclifecycle.SetupWithManager(mgr, cfg.MaxConcurrentReconciles, "apibinding-controller", &kcpapisv1alpha2.APIBinding{}, cfg.DebugLabelValue, r, r.log, evp...)
}

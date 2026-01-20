package controller

import (
	"context"
	"net/url"

	platformeshconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/builder"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/multicluster"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/security-operator/internal/subroutine"
	"github.com/rs/zerolog/log"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"

	"github.com/kcp-dev/logicalcluster/v3"
	kcpapisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
)

func GetAllClient(config *rest.Config, schema *runtime.Scheme) (client.Client, error) {
	allCfg := rest.CopyConfig(config)

	platformMeshClient, err := client.New(allCfg, client.Options{
		Scheme: schema,
	})
	if err != nil {
		log.Error().Err(err).Msg("unable to create client from config")
		return nil, err
	}

	var apiExportEndpointSlice kcpapisv1alpha1.APIExportEndpointSlice
	err = platformMeshClient.Get(context.Background(), types.NamespacedName{Name: "core.platform-mesh.io"}, &apiExportEndpointSlice)
	if err != nil {
		log.Error().Err(err).Msg("unable to get APIExportEndpointSlice")
		return nil, err
	}

	virtualWorkspaceUrl, err := url.Parse(apiExportEndpointSlice.Status.APIExportEndpoints[0].URL)
	if err != nil {
		log.Error().Err(err).Msg("unable to parse endpoint URL")
		return nil, err
	}

	parsed, err := url.Parse(allCfg.Host)
	if err != nil {
		log.Error().Err(err).Msg("unable to parse host from config")
		return nil, err
	}

	parsed.Path, err = url.JoinPath(virtualWorkspaceUrl.Path, "clusters", logicalcluster.Wildcard.String())
	if err != nil {
		log.Error().Err(err).Msg("unable to join path")
		return nil, err
	}

	allCfg.Host = parsed.String()

	log.Info().Str("host", allCfg.Host).Msg("using host")

	allClient, err := client.New(allCfg, client.Options{
		Scheme: schema,
	})
	if err != nil {
		return nil, err
	}
	return allClient, nil
}

func NewAPIBindingReconciler(logger *logger.Logger, mcMgr mcmanager.Manager) *APIBindingReconciler {
	allclient, err := GetAllClient(mcMgr.GetLocalManager().GetConfig(), mcMgr.GetLocalManager().GetScheme())
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
	return r.mclifecycle.Reconcile(ctxWithCluster, req, &kcpapisv1alpha1.APIBinding{})
}

func (r *APIBindingReconciler) SetupWithManager(mgr mcmanager.Manager, cfg *platformeshconfig.CommonServiceConfig, evp ...predicate.Predicate) error {
	return r.mclifecycle.SetupWithManager(mgr, cfg.MaxConcurrentReconciles, "apibinding-controller", &kcpapisv1alpha1.APIBinding{}, cfg.DebugLabelValue, r, r.log, evp...)
}

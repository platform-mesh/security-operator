package cmd

import (
	"context"
	"crypto/tls"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	platformeshcontext "github.com/platform-mesh/golang-commons/context"
	iclient "github.com/platform-mesh/security-operator/internal/client"
	"github.com/platform-mesh/security-operator/internal/controller"
	"github.com/platform-mesh/security-operator/internal/fga"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"k8s.io/client-go/rest"

	"github.com/kcp-dev/logicalcluster/v3"
	"github.com/kcp-dev/multicluster-provider/apiexport"
)

var systemCmd = &cobra.Command{
	Use:   "system",
	Short: "System controllers for system.platform-mesh.io apiexport resources",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctrl.SetLogger(log.ComponentLogger("controller-runtime").Logr())

		ctx, _, shutdown := platformeshcontext.StartContext(log, defaultCfg, defaultCfg.ShutdownTimeout)
		defer shutdown()

		restCfg, err := getKubeconfigFromPath(systemCfg.KCP.Kubeconfig)
		if err != nil {
			log.Error().Err(err).Msg("unable to get KCP kubeconfig")
			return err
		}

		opts := ctrl.Options{
			Scheme: scheme,
			Metrics: metricsserver.Options{
				BindAddress: defaultCfg.Metrics.BindAddress,
				TLSOpts: []func(*tls.Config){
					func(c *tls.Config) {
						c.NextProtos = []string{"http/1.1"}
					},
				},
			},
			HealthProbeBindAddress: defaultCfg.HealthProbeBindAddress,
			LeaderElection:         defaultCfg.LeaderElectionEnabled,
			LeaderElectionID:       "security-operator-system.platform-mesh.io",
			BaseContext:            func() context.Context { return ctx },
		}

		if defaultCfg.LeaderElectionEnabled {
			inClusterCfg, err := rest.InClusterConfig()
			if err != nil {
				setupLog.Error(err, "unable to get in-cluster config for leader election")
				return err
			}
			opts.LeaderElectionConfig = inClusterCfg
		}

		provider, err := apiexport.New(restCfg, systemCfg.APIExportEndpointSlices.SystemPlatformMeshIO, apiexport.Options{
			Scheme: scheme,
		})
		if err != nil {
			setupLog.Error(err, "unable to create apiexport provider")
			return err
		}

		mgr, err := mcmanager.New(restCfg, provider, opts)
		if err != nil {
			setupLog.Error(err, "unable to create manager")
			return err
		}

		conn, err := grpc.NewClient(systemCfg.FGA.Target, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Error().Err(err).Msg("unable to create grpc client")
			return err
		}

		fgaClient := openfgav1.NewOpenFGAServiceClient(conn)

		storeIDGetter := fga.NewCachingStoreIDGetter(
			fgaClient,
			systemCfg.FGA.StoreIDCacheTTL,
			ctx,
			log,
		)

		orgClient, err := iclient.NewForLogicalCluster(restCfg, scheme, logicalcluster.Name("root:orgs"))
		if err != nil {
			log.Error().Err(err).Msg("Failed to create org client")
			return err
		}

		idpReconciler, err := controller.NewIdentityProviderConfigurationReconciler(ctx, mgr, orgClient, &systemCfg, log)
		if err != nil {
			log.Error().Err(err).Str("controller", "identityprovider").Msg("unable to create reconciler")
			return err
		}
		if err := idpReconciler.SetupWithManager(mgr, defaultCfg, log); err != nil {
			log.Error().Err(err).Str("controller", "identityprovider").Msg("unable to create controller")
			return err
		}

		if err = controller.NewAPIExportPolicyReconciler(log, fgaClient, mgr, &systemCfg, storeIDGetter).SetupWithManager(mgr, defaultCfg, &systemCfg); err != nil {
			log.Error().Err(err).Str("controller", "apiexportpolicy").Msg("unable to create controller")
			return err
		}

		if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
			log.Error().Err(err).Msg("unable to set up health check")
			return err
		}
		if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
			log.Error().Err(err).Msg("unable to set up ready check")
			return err
		}

		setupLog.Info("starting system manager")

		return mgr.Start(ctx)
	},
}

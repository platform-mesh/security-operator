package cmd

import (
	"context"
	"crypto/tls"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	platformeshcontext "github.com/platform-mesh/golang-commons/context"
	"github.com/platform-mesh/security-operator/internal/controller"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"k8s.io/client-go/rest"

	"github.com/kcp-dev/multicluster-provider/apiexport"
)

var apiExportPolicyCmd = &cobra.Command{
	Use:   "authorization",
	Short: "Controllers for authorization.platform-mesh.io APIExport",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctrl.SetLogger(log.ComponentLogger("controller-runtime").Logr())

		ctx, _, shutdown := platformeshcontext.StartContext(log, defaultCfg, defaultCfg.ShutdownTimeout)
		defer shutdown()

		restCfg, err := getKubeconfigFromPath(authorizationCfg.KCP.Kubeconfig)
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
			LeaderElectionID:       "security-operator-authorization.platform-mesh.io",
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

		provider, err := apiexport.New(restCfg, authorizationCfg.AuthorizationAPIExportEndpointSliceName, apiexport.Options{
			Scheme: scheme,
		})
		if err != nil {
			setupLog.Error(err, "unable to create path-aware provider")
			return err
		}

		mgr, err := mcmanager.New(restCfg, provider, opts)
		if err != nil {
			setupLog.Error(err, "unable to create apiexportpolicy manager")
			return err
		}

		conn, err := grpc.NewClient(authorizationCfg.FGA.Target, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Error().Err(err).Msg("unable to create grpc client")
			return err
		}

		fga := openfgav1.NewOpenFGAServiceClient(conn)

		if err = controller.NewAPIExportPolicyReconciler(log, fga, mgr).SetupWithManager(mgr, defaultCfg); err != nil {
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

		setupLog.Info("starting authorization manager")

		return mgr.Start(ctx)
	},
}

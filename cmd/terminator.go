package cmd

import (
	"crypto/tls"
	"os"
	"strings"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	platformeshconfig "github.com/platform-mesh/golang-commons/config"
	iclient "github.com/platform-mesh/security-operator/internal/client"
	"github.com/platform-mesh/security-operator/internal/terminatingworkspaces"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/security-operator/internal/controller"
	"github.com/platform-mesh/security-operator/internal/predicates"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"k8s.io/client-go/rest"

	"github.com/kcp-dev/logicalcluster/v3"
	kcptenancyv1alphav1 "github.com/kcp-dev/sdk/apis/tenancy/v1alpha1"

	mcclient "github.com/kcp-dev/multicluster-provider/client"
)

var terminatorCfg config.Config

var terminatorCmd = &cobra.Command{
	Use:   "terminator",
	Short: "FGA terminator for account workspaces",
	RunE: func(cmd *cobra.Command, args []string) error {
		kcpCfg, err := getKubeconfigFromPath(terminatorCfg.KCP.Kubeconfig)
		if err != nil {
			log.Error().Err(err).Msg("unable to get KCP kubeconfig")
			os.Exit(1)
		}

		mgrOpts := ctrl.Options{
			Scheme:                 scheme,
			LeaderElection:         defaultCfg.LeaderElection.Enabled,
			LeaderElectionID:       "security-operator-terminator.platform-mesh.io",
			HealthProbeBindAddress: defaultCfg.HealthProbeBindAddress,
			Metrics: server.Options{
				BindAddress: defaultCfg.Metrics.BindAddress,
				TLSOpts: []func(*tls.Config){
					func(c *tls.Config) {
						log.Info().Msg("disabling http/2")
						c.NextProtos = []string{"http/1.1"}
					},
				},
			},
		}
		if defaultCfg.LeaderElection.Enabled {
			inClusterCfg, err := rest.InClusterConfig()
			if err != nil {
				log.Error().Err(err).Msg("unable to create in-cluster config")
				return err
			}
			mgrOpts.LeaderElectionConfig = inClusterCfg
		}

		mcc, err := mcclient.New(kcpCfg, client.Options{Scheme: scheme})
		if err != nil {
			log.Error().Err(err).Msg("Failed to create multicluster client")
			os.Exit(1)
		}
		rootClient, err := iclient.NewForLogicalCluster(kcpCfg, scheme, logicalcluster.Name("root"))
		if err != nil {
			log.Error().Err(err).Msgf("Failed to get root client")
			os.Exit(1)
		}
		var wt kcptenancyv1alphav1.WorkspaceType
		if err := rootClient.Get(cmd.Context(), client.ObjectKey{
			Name: terminatorCfg.WorkspaceTypeName,
		}, &wt); err != nil {
			log.Error().Err(err).Msgf("Failed to get WorkspaceType %s", terminatorCfg.WorkspaceTypeName)
			os.Exit(1)
		}
		if len(wt.Status.VirtualWorkspaces) == 0 {
			log.Error().Err(err).Msgf("No VirtualWorkspaces found in WorkspaceType %s", terminatorCfg.WorkspaceTypeName)
			os.Exit(1)
		}

		provider, err := terminatingworkspaces.New(kcpCfg, terminatorCfg.WorkspaceTypeName,
			terminatingworkspaces.Options{
				Scheme: mgrOpts.Scheme,
			},
		)

		mgr, err := mcmanager.New(kcpCfg, provider, mgrOpts)
		if err != nil {
			log.Error().Err(err).Msg("Failed to create manager")
			os.Exit(1)
		}

		conn, err := grpc.NewClient(terminatorCfg.FGA.Target, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Error().Err(err).Msg("unable to create grpc client")
			os.Exit(1)
		}
		fga := openfgav1.NewOpenFGAServiceClient(conn)

		if err := controller.NewAccountLogicalClusterReconciler(log, terminatorCfg, fga, mcc, mgr).
			SetupWithManager(mgr, defaultCfg, predicate.Not(predicates.LogicalClusterIsAccountTypeOrg())); err != nil {
			log.Error().Err(err).Msg("Unable to create AccountLogicalClusterTerminator")
			os.Exit(1)
		}

		if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
			log.Error().Err(err).Msg("unable to set up health check")
			os.Exit(1)
		}
		if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
			log.Error().Err(err).Msg("unable to set up ready check")
			os.Exit(1)
		}

		setupLog.Info("starting manager")

		return mgr.Start(ctrl.SetupSignalHandler())
	},
}

func init() {
	rootCmd.AddCommand(terminatorCmd)

	terminatorV := viper.NewWithOptions(
		viper.EnvKeyReplacer(strings.NewReplacer("-", "_")),
	)
	terminatorV.AutomaticEnv()
	if err := platformeshconfig.BindConfigToFlags(terminatorV, terminatorCmd, &terminatorCfg); err != nil {
		panic(err)
	}
}

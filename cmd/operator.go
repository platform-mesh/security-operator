package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/url"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	"github.com/kcp-dev/logicalcluster/v3"
	"github.com/kcp-dev/multicluster-provider/apiexport"
	kcpapisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	accountsv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	platformeshcontext "github.com/platform-mesh/golang-commons/context"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/golang-commons/sentry"
	corev1alpha1 "github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/controller"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	kcptenancyv1alphav1 "github.com/kcp-dev/sdk/apis/tenancy/v1alpha1"

	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

var (
	scheme = runtime.NewScheme()
)

const (
	platformMeshWorkspace = "root:platform-mesh-system"
)

type NewLogicalClusterClientFunc func(clusterKey logicalcluster.Name) (client.Client, error)

func getPlatformMeshSystemConfig(cfg *rest.Config) (*rest.Config, error) {
	providerConfig := rest.CopyConfig(cfg)

	parsed, err := url.Parse(providerConfig.Host)
	if err != nil {
		return nil, fmt.Errorf("unable to parse URL: %w", err)
	}

	parsed.Path, err = url.JoinPath("clusters", platformMeshWorkspace)
	if err != nil {
		return nil, fmt.Errorf("failed to join path")
	}
	providerConfig.Host = parsed.String()

	return providerConfig, nil
}

func logicalClusterClientFromKey(mgr ctrl.Manager, log *logger.Logger) NewLogicalClusterClientFunc {
	return func(clusterKey logicalcluster.Name) (client.Client, error) {
		cfg := rest.CopyConfig(config)

		parsed, err := url.Parse(cfg.Host)
		if err != nil {
			log.Error().Err(err).Msg("unable to parse host")
			return nil, err
		}

		parsed.Path = fmt.Sprintf("/clusters/%s", clusterKey)

		cfg.Host = parsed.String()

		return client.New(cfg, client.Options{
			Scheme: scheme,
		})
	}
}

var operatorCmd = &cobra.Command{
	Use: "fga",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctrl.SetLogger(log.ComponentLogger("controller-runtime").Logr())

		ctx, _, shutdown := platformeshcontext.StartContext(log, defaultCfg, defaultCfg.ShutdownTimeout)
		defer shutdown()

		restCfg, err := getKubeconfigFromPath(operatorCfg.KCP.Kubeconfig)
		if err != nil {
			log.Error().Err(err).Msg("unable to get KCP kubeconfig")
			return err
		}

		if operatorCfg.MigrateAuthorizationModels {
			if err := migrateAuthorizationModels(ctx, restCfg, scheme, logicalClusterClientFromKey(restCfg, log)); err != nil {
				log.Error().Err(err).Msg("migration failed")
				return err
			}
		}

		if defaultCfg.Sentry.Dsn != "" {
			err := sentry.Start(ctx,
				defaultCfg.Sentry.Dsn, defaultCfg.Environment, defaultCfg.Region,
				defaultCfg.Image.Name, defaultCfg.Image.Tag,
			)
			if err != nil {
				log.Fatal().Err(err).Msg("Sentry init failed")
			}

			defer platformeshcontext.Recover(log)
		}

		mgrOpts := ctrl.Options{
			Scheme: scheme,
			Metrics: metricsserver.Options{
				BindAddress: defaultCfg.Metrics.BindAddress,
				TLSOpts: []func(*tls.Config){
					func(c *tls.Config) {
						log.Info().Msg("disabling http/2")
						c.NextProtos = []string{"http/1.1"}
					},
				},
			},
			HealthProbeBindAddress: defaultCfg.HealthProbeBindAddress,
			LeaderElection:         defaultCfg.LeaderElection.Enabled,
			LeaderElectionID:       "security-operator.platform-mesh.io",
			BaseContext:            func() context.Context { return ctx },
		}
		if defaultCfg.LeaderElection.Enabled {
			inClusterCfg, err := rest.InClusterConfig()
			if err != nil {
				log.Error().Err(err).Msg("unable to create in-cluster config")
				return err
			}
			mgrOpts.LeaderElectionConfig = inClusterCfg
		}

		if mgrOpts.Scheme == nil {
			log.Error().Err(fmt.Errorf("scheme should not be nil")).Msg("scheme should not be nil")
			return fmt.Errorf("scheme should not be nil")
		}
		providerConfig, err := getPlatformMeshSystemConfig(restCfg)
		if err != nil {
			setupLog.Error(err, "unable to create provider config")
			return err
		}
		provider, err := apiexport.New(providerConfig, operatorCfg.APIExportEndpointSliceName, apiexport.Options{
			Scheme: mgrOpts.Scheme,
		})
		if err != nil {
			setupLog.Error(err, "unable to construct cluster provider")
			return err
		}

		mgr, err := mcmanager.New(restCfg, provider, mgrOpts)
		if err != nil {
			setupLog.Error(err, "Failed to create manager")
			return err
		}

		conn, err := grpc.NewClient(operatorCfg.FGA.Target, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Error().Err(err).Msg("unable to create grpc client")
			return err
		}

		orgClient, err := logicalClusterClientFromKey(mgr.GetLocalManager().GetConfig(), log)(logicalcluster.Name("root:orgs"))
		if err != nil {
			log.Error().Err(err).Msg("Failed to create org client")
			return err
		}

		fga := openfgav1.NewOpenFGAServiceClient(conn)

		if err = controller.NewStoreReconciler(log, fga, mgr).
			SetupWithManager(mgr, defaultCfg); err != nil {
			log.Error().Err(err).Str("controller", "store").Msg("unable to create controller")
			return err
		}
		if err = controller.
			NewAuthorizationModelReconciler(log, fga, mgr).
			SetupWithManager(mgr, defaultCfg); err != nil {
			log.Error().Err(err).Str("controller", "authorizationmodel").Msg("unable to create controller")
			return err
		}
		if err = controller.NewIdentityProviderConfigurationReconciler(ctx, mgr, orgClient, &operatorCfg, log).SetupWithManager(mgr, defaultCfg, log); err != nil {
			log.Error().Err(err).Str("controller", "identityprovider").Msg("unable to create controller")
			return err
		}
		if err = controller.NewInviteReconciler(ctx, mgr, &operatorCfg, log).SetupWithManager(mgr, defaultCfg, log); err != nil {
			log.Error().Err(err).Str("controller", "invite").Msg("unable to create controller")
			return err
		}
		// +kubebuilder:scaffold:builder

		if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
			log.Error().Err(err).Msg("unable to set up health check")
			return err
		}
		if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
			log.Error().Err(err).Msg("unable to set up ready check")
			return err
		}

		setupLog.Info("starting manager")
		if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
			log.Error().Err(err).Msg("problem running manager")
			return err
		}
		return nil
	},
}

// this function can be removed after the operator has migrated the authz models in all environments
func migrateAuthorizationModels(ctx context.Context, config *rest.Config, scheme *runtime.Scheme, getClusterClient NewLogicalClusterClientFunc) error {
	allClient, err := controller.GetAllClient(config, scheme)
	if err != nil {
		return fmt.Errorf("failed to create all-cluster client: %w", err)
	}

	var models corev1alpha1.AuthorizationModelList
	if err := allClient.List(ctx, &models); err != nil {
		return fmt.Errorf("failed to list AuthorizationModels: %w", err)
	}

	for i := range models.Items {
		item := &models.Items[i]

		if item.Spec.StoreRef.Cluster != "" {
			continue
		}

		if item.Spec.StoreRef.Path == "" {
			return fmt.Errorf("AuthorizationModel %s has empty cluster field and no path field to migrate from", item.GetName())
		}

		clusterName := logicalcluster.From(item)
		clusterClient, err := getClusterClient(clusterName)
		if err != nil {
			return fmt.Errorf("failed to create cluster client for AuthorizationModel %s (cluster %s): %w", item.GetName(), clusterName, err)
		}

		original := item.DeepCopy()
		item.Spec.StoreRef.Cluster = item.Spec.StoreRef.Path

		patch := client.MergeFrom(original)
		if err := clusterClient.Patch(ctx, item, patch); err != nil {
			return fmt.Errorf("failed to patch AuthorizationModel %s: %w", item.GetName(), err)
		}
	}

	log.Info().Msg("AuthorizationModel migration completed")
	return nil
}

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kcptenancyv1alphav1.AddToScheme(scheme))
	utilruntime.Must(corev1alpha1.AddToScheme(scheme))
	utilruntime.Must(kcpapisv1alpha1.AddToScheme(scheme))
	utilruntime.Must(kcpcorev1alpha1.AddToScheme(scheme))
	utilruntime.Must(accountsv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

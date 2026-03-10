package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/url"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	accountsv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	platformeshcontext "github.com/platform-mesh/golang-commons/context"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/golang-commons/sentry"
	authorizationv1alpha1 "github.com/platform-mesh/security-operator/api/authorization/v1alpha1"
	corev1alpha1 "github.com/platform-mesh/security-operator/api/v1alpha1"
	iclient "github.com/platform-mesh/security-operator/internal/client"
	"github.com/platform-mesh/security-operator/internal/controller"
	internalwebhook "github.com/platform-mesh/security-operator/internal/webhook"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	"github.com/kcp-dev/logicalcluster/v3"
	"github.com/kcp-dev/multicluster-provider/apiexport"
	pathaware "github.com/kcp-dev/multicluster-provider/path-aware"
	kcpapisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
	kcpapisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
	kcptenancyv1alpha1 "github.com/kcp-dev/sdk/apis/tenancy/v1alpha1"

	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

var (
	scheme = runtime.NewScheme()
)

type NewLogicalClusterClientFunc func(clusterKey logicalcluster.Name) (client.Client, error)

func logicalClusterClientFromKey(config *rest.Config, log *logger.Logger) NewLogicalClusterClientFunc {
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

		coreMgr, err := setupCorePlatformMeshManager(ctx, restCfg)
		if err != nil {
			setupLog.Error(err, "unable to setup core manager")
			return err
		}

		authorizationMgr, err := setupAuthorizationPlatformMeshManager(ctx, restCfg)
		if err != nil {
			setupLog.Error(err, "unable to setup authorization manager")
			return err
		}

		conn, err := grpc.NewClient(operatorCfg.FGA.Target, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Error().Err(err).Msg("unable to create grpc client")
			return err
		}

		orgClient, err := logicalClusterClientFromKey(coreMgr.GetLocalManager().GetConfig(), log)(logicalcluster.Name("root:orgs"))
		if err != nil {
			log.Error().Err(err).Msg("Failed to create org client")
			return err
		}

		fga := openfgav1.NewOpenFGAServiceClient(conn)

		if err = controller.NewStoreReconciler(ctx, log, fga, coreMgr).
			SetupWithManager(coreMgr, defaultCfg); err != nil {
			log.Error().Err(err).Str("controller", "store").Msg("unable to create controller")
			return err
		}
		if err = controller.
			NewAuthorizationModelReconciler(log, fga, coreMgr).
			SetupWithManager(coreMgr, defaultCfg); err != nil {
			log.Error().Err(err).Str("controller", "authorizationmodel").Msg("unable to create controller")
			return err
		}
		if err = controller.NewIdentityProviderConfigurationReconciler(ctx, coreMgr, orgClient, &operatorCfg, log).SetupWithManager(coreMgr, defaultCfg, log); err != nil {
			log.Error().Err(err).Str("controller", "identityprovider").Msg("unable to create controller")
			return err
		}
		if err = controller.NewInviteReconciler(ctx, coreMgr, &operatorCfg, log).SetupWithManager(coreMgr, defaultCfg, log); err != nil {
			log.Error().Err(err).Str("controller", "invite").Msg("unable to create controller")
			return err
		}
		if err = controller.NewAccountInfoReconciler(log, coreMgr).SetupWithManager(coreMgr, defaultCfg); err != nil {
			log.Error().Err(err).Str("controller", "accountinfo").Msg("unable to create controller")
			return err
		}

		if err = controller.NewAPIExportPolicyReconciler(log, fga, authorizationMgr).SetupWithManager(authorizationMgr, defaultCfg); err != nil {
			log.Error().Err(err).Str("controller", "apiexportpolicy").Msg("unable to create controller")
			return err
		}

		if operatorCfg.Webhooks.Enabled {
			log.Info().Msg("validating webhooks are enabled")
			if err := internalwebhook.SetupIdentityProviderConfigurationValidatingWebhookWithManager(ctx, coreMgr.GetLocalManager(), &operatorCfg); err != nil {
				log.Error().Err(err).Str("webhook", "IdentityProviderConfiguration").Msg("unable to create webhook")
				return err
			}
		}
		// +kubebuilder:scaffold:builder

		if err := coreMgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
			log.Error().Err(err).Msg("unable to set up health check")
			return err
		}
		if err := coreMgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
			log.Error().Err(err).Msg("unable to set up ready check")
			return err
		}

		g, gctx := errgroup.WithContext(ctx)

		g.Go(func() error {
			setupLog.Info("starting core manager")
			return coreMgr.Start(gctx)
		})

		g.Go(func() error {
			setupLog.Info("starting authorization manager")
			return authorizationMgr.Start(gctx)
		})

		if err := g.Wait(); err != nil {
			log.Error().Err(err).Msg("failed to run managers")
			return err
		}
		return nil
	},
}

// this function can be removed after the operator has migrated the authz models in all environments
func migrateAuthorizationModels(ctx context.Context, config *rest.Config, scheme *runtime.Scheme, getClusterClient NewLogicalClusterClientFunc) error {
	allClient, err := iclient.NewForAllPlatformMeshResources(ctx, config, scheme)
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
	utilruntime.Must(kcptenancyv1alpha1.AddToScheme(scheme))
	utilruntime.Must(corev1alpha1.AddToScheme(scheme))
	utilruntime.Must(authorizationv1alpha1.AddToScheme(scheme))
	utilruntime.Must(kcpapisv1alpha1.AddToScheme(scheme))
	utilruntime.Must(kcpapisv1alpha2.AddToScheme(scheme))
	utilruntime.Must(kcpcorev1alpha1.AddToScheme(scheme))
	utilruntime.Must(accountsv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func setupCorePlatformMeshManager(ctx context.Context, restCfg *rest.Config) (mcmanager.Manager, error) {
	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: []func(*tls.Config){
			func(c *tls.Config) {
				c.NextProtos = []string{"http/1.1"}
			},
		},
		CertDir: operatorCfg.Webhooks.CertDir,
		Port:    operatorCfg.Webhooks.Port,
	})

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
		LeaderElectionID:       "security-operator.platform-mesh.io",
		BaseContext:            func() context.Context { return ctx },
		WebhookServer:          webhookServer,
	}

	if defaultCfg.LeaderElectionEnabled {
		inClusterCfg, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("getting in-cluster config for leader election: %w", err)
		}
		opts.LeaderElectionConfig = inClusterCfg
	}

	provider, err := apiexport.New(restCfg, operatorCfg.CoreAPIExportEndpointSliceName, apiexport.Options{
		Scheme: opts.Scheme,
	})
	if err != nil {
		return nil, fmt.Errorf("creating apiexport provider: %w", err)
	}

	mgr, err := mcmanager.New(restCfg, provider, opts)
	if err != nil {
		return nil, fmt.Errorf("creating core.platform-mesh.io manager: %w", err)
	}

	return mgr, nil
}

func setupAuthorizationPlatformMeshManager(ctx context.Context, restCfg *rest.Config) (mcmanager.Manager, error) {
	provider, err := pathaware.New(restCfg, operatorCfg.AuthorizationAPIExportEndpointSliceName, apiexport.Options{
		Scheme: scheme,
	})
	if err != nil {
		return nil, fmt.Errorf("creating path-aware provider: %w", err)
	}

	opts := ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
		BaseContext:            func() context.Context { return ctx },
	}

	mgr, err := mcmanager.New(restCfg, provider, opts)
	if err != nil {
		return nil, fmt.Errorf("creating authorization.platform-mesh.io manager: %w", err)
	}

	return mgr, nil
}

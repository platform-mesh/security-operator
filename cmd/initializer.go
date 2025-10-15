package cmd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	openfgav1 "github.com/openfga/api/proto/openfga/v1"

	"github.com/kcp-dev/logicalcluster/v3"
	"github.com/kcp-dev/multicluster-provider/initializingworkspaces"
	pmcontext "github.com/platform-mesh/golang-commons/context"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/platform-mesh/security-operator/internal/controller"
)

// loadCertPool loads PEM certificates from a file or directory into a CertPool.
func loadCertPool(path string) (*x509.CertPool, error) {
	pool, _ := x509.SystemCertPool()
	if pool == nil {
		pool = x509.NewCertPool()
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	add := func(p string) error {
		pem, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if ok := pool.AppendCertsFromPEM(pem); !ok {
			return fs.ErrInvalid
		}
		return nil
	}
	if info.IsDir() {
		return pool, filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			return add(p)
		})
	}
	return pool, add(path)
}

// bearerToken implements PerRPCCredentials with a static bearer token.
type bearerToken string

func (b bearerToken) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + string(b)}, nil
}

func (b bearerToken) RequireTransportSecurity() bool { return true }

var initializerCmd = &cobra.Command{
	Use:   "initializer",
	Short: "FGA initializer for the organization workspacetype",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, _, shutdown := pmcontext.StartContext(log, initializerCfg, defaultCfg.ShutdownTimeout)
		defer shutdown()

		mgrCfg := ctrl.GetConfigOrDie()

		mgrOpts := ctrl.Options{
			Scheme:                 scheme,
			LeaderElection:         defaultCfg.LeaderElection.Enabled,
			LeaderElectionID:       "security-operator-initializer.platform-mesh.io",
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

		provider, err := initializingworkspaces.New(mgrCfg, initializingworkspaces.Options{
			InitializerName: initializerCfg.InitializerName,
			Scheme:          mgrOpts.Scheme,
		})
		if err != nil {
			log.Error().Err(err).Msg("unable to construct cluster provider")
			os.Exit(1)
		}

		mgr, err := mcmanager.New(mgrCfg, provider, mgrOpts)
		if err != nil {
			setupLog.Error(err, "Failed to create manager")
			os.Exit(1)
		}

		runtimeScheme := runtime.NewScheme()
		utilruntime.Must(sourcev1.AddToScheme(runtimeScheme))
		utilruntime.Must(helmv2.AddToScheme(runtimeScheme))

		orgClient, err := logicalClusterClientFromKey(mgr.GetLocalManager(), log)(logicalcluster.Name("root:orgs"))
		if err != nil {
			setupLog.Error(err, "Failed to create org client")
			os.Exit(1)
		}

		inClusterConfig, err := rest.InClusterConfig()
		if err != nil {
			log.Error().Err(err).Msg("Failed to create in cluster config")
			os.Exit(1)
		}

		inClusterClient, err := client.New(inClusterConfig, client.Options{Scheme: scheme})
		if err != nil {
			log.Error().Err(err).Msg("Failed to create in cluster client")
			os.Exit(1)
		}

		if initializerCfg.IDP.AdditionalRedirectURLs == nil {
			initializerCfg.IDP.AdditionalRedirectURLs = []string{}
		}

		if initializerCfg.FGA.Target == "" {
			log.Error().Msg("FGA target is empty; set fga-target in configuration")
			os.Exit(1)
		}

		// Build transport credentials: prefer TLS unless explicitly configured as insecure
		var tcreds credentials.TransportCredentials
		if !initializerCfg.FGA.Insecure {
			var tlsCfg tls.Config
			// Load custom CA if provided
			if initializerCfg.FGA.CACertPath != "" {
				// best-effort CA load; fall back to system pool on failure
				if pool, err := loadCertPool(initializerCfg.FGA.CACertPath); err == nil {
					tlsCfg.RootCAs = pool
				} else {
					log.Warn().Err(err).Msg("failed to load FGA CA bundle; falling back to system roots")
				}
			}
			tcreds = credentials.NewTLS(&tlsCfg)
		} else {
			tcreds = insecure.NewCredentials()
		}

		// Optional per-RPC bearer token
		var dialOpts []grpc.DialOption
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(tcreds))
		if tok := initializerCfg.FGA.BearerToken; tok != "" {
			dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(bearerToken(tok)))
		}

		ctxDial, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		conn, err := grpc.NewClient(initializerCfg.FGA.Target, dialOpts...)
		if err != nil {
			log.Error().Err(err).Msg("unable to create OpenFGA client connection")
			os.Exit(1)
		}
		// Eagerly connect and wait until Ready or timeout to fail fast on bad endpoints.
		conn.Connect()
		ready := false
		for {
			s := conn.GetState()
			if s == connectivity.Ready {
				ready = true
				break
			}
			if !conn.WaitForStateChange(ctxDial, s) {
				break // context expired
			}
		}
		if !ready {
			log.Error().Msg("OpenFGA connection not ready before deadline")
			os.Exit(1)
		}
		defer func() { _ = conn.Close() }()

		fga := openfgav1.NewOpenFGAServiceClient(conn)

		if err := controller.NewLogicalClusterReconciler(log, orgClient, initializerCfg, inClusterClient, mgr, fga).
			SetupWithManager(mgr, defaultCfg); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "LogicalCluster")
			os.Exit(1)
		}

		if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
			setupLog.Error(err, "unable to set up health check")
			os.Exit(1)
		}
		if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
			setupLog.Error(err, "unable to set up ready check")
			os.Exit(1)
		}

		go func() {
			if err := provider.Run(ctx, mgr); err != nil {
				log.Fatal().Err(err).Msg("unable to run provider")
			}
		}()

		setupLog.Info("starting manager")

		return mgr.Start(ctrl.SetupSignalHandler())
	},
}

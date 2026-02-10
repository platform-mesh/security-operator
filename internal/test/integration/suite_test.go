package test

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"testing"
	"time"

	accountv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	platformeshconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/logger"
	securityv1alpha1 "github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/security-operator/internal/controller"
	"github.com/platform-mesh/security-operator/internal/predicates"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/yaml"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	"github.com/kcp-dev/logicalcluster/v3"
	"github.com/kcp-dev/multicluster-provider/apiexport"
	clusterclient "github.com/kcp-dev/multicluster-provider/client"
	"github.com/kcp-dev/multicluster-provider/envtest"
	"github.com/kcp-dev/multicluster-provider/initializingworkspaces"
	apisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
	apisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
	"github.com/kcp-dev/sdk/apis/core"
	corev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
	tenancyv1alpha1 "github.com/kcp-dev/sdk/apis/tenancy/v1alpha1"

	_ "embed"
)

var (
	//go:embed yaml/apiresourceschema-accountinfos.core.platform-mesh.io.yaml
	AccountInfoSchemaYAML []byte

	//go:embed yaml/apiresourceschema-accounts.core.platform-mesh.io.yaml
	AccountSchemaYAML []byte

	//go:embed yaml/apiresourceschema-authorizationmodels.core.platform-mesh.io.yaml
	AuthorizationModelSchemaYAML []byte

	//go:embed yaml/apiresourceschema-stores.core.platform-mesh.io.yaml
	StoreSchemaYAML []byte

	//go:embed yaml/apiexport-core.platform-mesh.io.yaml
	ApiExportPlatformMeshSystemYAML []byte

	//go:embed yaml/apibinding-core-platform-mesh.io.yaml
	ApiBindingCorePlatformMeshYAML []byte

	//go:embed yaml/workspace-type-org.yaml
	WorkspaceTypeOrgYAML []byte

	//go:embed yaml/workspace-type-orgs.yaml
	WorkspaceTypeOrgsYAML []byte

	//go:embed yaml/workspace-type-account.yaml
	WorkspaceTypeAccountYAML []byte
)

func init() {
	utilruntime.Must(apisv1alpha1.AddToScheme(scheme.Scheme))
	utilruntime.Must(corev1alpha1.AddToScheme(scheme.Scheme))
	utilruntime.Must(tenancyv1alpha1.AddToScheme(scheme.Scheme))
	utilruntime.Must(accountv1alpha1.AddToScheme(scheme.Scheme))
	utilruntime.Must(securityv1alpha1.AddToScheme(scheme.Scheme))
	utilruntime.Must(apisv1alpha2.AddToScheme(scheme.Scheme))
}

type IntegrationSuite struct {
	suite.Suite
	env                          *envtest.Environment
	kcpConfig                    *rest.Config
	apiExportEndpointSliceConfig *rest.Config
	platformMeshSysPath          logicalcluster.Path
	platformMeshSystemClient     client.Client
	orgsPath                     logicalcluster.Path
}

func TestIntegrationSuite(t *testing.T) {
	suite.Run(t, new(IntegrationSuite))
}

func (suite *IntegrationSuite) SetupSuite() {

	rootCmd := &cobra.Command{
		Use: "security-operator",
	}
	_, defaultCfg, err := platformeshconfig.NewDefaultConfig(rootCmd)
	suite.Require().NoError(err)

	logcfg := logger.DefaultConfig()
	logcfg.Output = io.Discard

	testLogger, err := logger.New(logcfg)
	require.NoError(suite.T(), err, "failed to create test logger")
	ctrl.SetLogger(testLogger.Logr())

	suite.env = &envtest.Environment{}
	// Set the context in case using an existing KCP instance.
	if os.Getenv("USE_EXISTING_KCP") != "" && os.Getenv("EXISTING_KCP_CONTEXT") == "" {
		suite.env.ExistingKcpContext = "base"
	}

	// Prevents KCP from cleaning up workspace fixtures before shutdown, the
	// instance controlled by envtest is ephemeral anyway.
	if os.Getenv("PRESERVE") == "" {
		suite.Require().NoError(os.Setenv("PRESERVE", "true"))
	}

	suite.kcpConfig, err = suite.env.Start()
	require.NoError(suite.T(), err, "failed to start envtest environment")

	suite.T().Cleanup(func() {
		if err := suite.env.Stop(); err != nil {
			suite.T().Logf("error stopping envtest environment: %v", err)
		}
		suite.T().Log("kcp server has been stopped")
	})

	suite.setupPlatformMesh(suite.T())
	suite.setupControllers(defaultCfg, testLogger)
}

func (suite *IntegrationSuite) setupPlatformMesh(t *testing.T) {
	ctx := suite.T().Context()

	var err error
	cli, err := clusterclient.New(suite.kcpConfig, client.Options{})
	suite.Require().NoError(err)

	rootClient := cli.Cluster(core.RootCluster.Path())

	// create :root:platform-mesh-system ws
	_, platformMeshSystemClusterPath := envtest.NewWorkspaceFixture(suite.T(), cli, core.RootCluster.Path(), envtest.WithName("platform-mesh-system"))
	suite.platformMeshSysPath = platformMeshSystemClusterPath
	suite.platformMeshSystemClient = cli.Cluster(platformMeshSystemClusterPath)

	// register api-resource schemas
	schemas := [][]byte{AccountInfoSchemaYAML, AccountSchemaYAML, AuthorizationModelSchemaYAML, StoreSchemaYAML}
	for _, schemaYAML := range schemas {
		var schema apisv1alpha1.APIResourceSchema
		suite.Require().NoError(yaml.Unmarshal(schemaYAML, &schema))
		err = cli.Cluster(platformMeshSystemClusterPath).Create(ctx, &schema)
		if err != nil && !kerrors.IsAlreadyExists(err) {
			suite.Require().NoError(err)
		}
		suite.T().Logf("created APIResourceSchema: %s", schema.Name)
	}
	suite.Require().NoError(err)

	var apiExport apisv1alpha1.APIExport
	suite.Require().NoError(yaml.Unmarshal(ApiExportPlatformMeshSystemYAML, &apiExport))

	err = cli.Cluster(platformMeshSystemClusterPath).Create(ctx, &apiExport)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}

	var platformMeshBinding apisv1alpha2.APIBinding
	suite.Require().NoError(yaml.Unmarshal(ApiBindingCorePlatformMeshYAML, &platformMeshBinding))

	err = cli.Cluster(platformMeshSystemClusterPath).Create(ctx, &platformMeshBinding)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}
	t.Log("created APIBinding 'core.platform-mesh.io' in platform-mesh-system workspace")
	suite.Assert().Eventually(func() bool {
		var binding apisv1alpha2.APIBinding
		if err := cli.Cluster(platformMeshSystemClusterPath).Get(ctx, client.ObjectKey{Name: platformMeshBinding.Name}, &binding); err != nil {
			return false
		}
		return binding.Status.Phase == apisv1alpha2.APIBindingPhaseBound
	}, 10*time.Second, 200*time.Millisecond, "APIBinding core.platform-mesh.io should be bound")

	// Create WorkspaceTypes in root workspace
	var orgWorkspaceType tenancyv1alpha1.WorkspaceType
	suite.Require().NoError(yaml.Unmarshal(WorkspaceTypeOrgYAML, &orgWorkspaceType))

	err = rootClient.Create(ctx, &orgWorkspaceType)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}
	t.Log("created WorkspaceType 'org' in root workspace")

	var orgsWorkspaceType tenancyv1alpha1.WorkspaceType
	suite.Require().NoError(yaml.Unmarshal(WorkspaceTypeOrgsYAML, &orgsWorkspaceType))

	err = rootClient.Create(ctx, &orgsWorkspaceType)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}
	t.Log("created WorkspaceType 'orgs' in root workspace")

	var accountWorkspaceType tenancyv1alpha1.WorkspaceType
	suite.Require().NoError(yaml.Unmarshal(WorkspaceTypeAccountYAML, &accountWorkspaceType))

	err = rootClient.Create(ctx, &accountWorkspaceType)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}
	t.Log("created WorkspaceType 'account' in root workspace")

	// // Create WorkspaceType 'security' with initializer so org workspaces can extend it and get root:security initializer
	// securityWorkspaceType := &tenancyv1alpha1.WorkspaceType{
	// 	ObjectMeta: metav1.ObjectMeta{Name: "security"},
	// 	Spec: tenancyv1alpha1.WorkspaceTypeSpec{
	// 		Initializer: true,
	// 	},
	// }
	// err = rootClient.Create(ctx, securityWorkspaceType)
	// if err != nil && !kerrors.IsAlreadyExists(err) {
	// 	suite.Require().NoError(err)
	// }
	// t.Log("created WorkspaceType 'security' (initializer) in root workspace")

	// // Patch org WorkspaceType to extend security so new org workspaces get root:security initializer
	// var orgWT tenancyv1alpha1.WorkspaceType
	// suite.Require().NoError(rootClient.Get(ctx, client.ObjectKey{Name: "org"}, &orgWT))
	// orgWT.Spec.Extend = tenancyv1alpha1.WorkspaceTypeExtension{
	// 	With: []tenancyv1alpha1.WorkspaceTypeReference{
	// 		{Name: "security", Path: "root"},
	// 	},
	// }
	// suite.Require().NoError(rootClient.Update(ctx, &orgWT))
	// t.Log("patched WorkspaceType 'org' to extend 'security'")

	// create :root:orgs ws
	orgsWs, orgsClusterPath := envtest.NewWorkspaceFixture(suite.T(), cli, core.RootCluster.Path(), envtest.WithName("orgs"), envtest.WithType(core.RootCluster.Path(), "orgs"))
	suite.orgsPath = orgsClusterPath
	t.Logf("orgs workspace path (%s), cluster id (%s)", orgsClusterPath, orgsWs.Spec.Cluster)

	// // Bind core.platform-mesh.io in orgs so the org LogicalCluster controller can create Store resources there
	// var orgsPlatformMeshBinding apisv1alpha2.APIBinding
	// suite.Require().NoError(yaml.Unmarshal(ApiBindingCorePlatformMeshYAML, &orgsPlatformMeshBinding))
	// err = cli.Cluster(orgsClusterPath).Create(ctx, &orgsPlatformMeshBinding)
	// if err != nil && !kerrors.IsAlreadyExists(err) {
	// 	suite.Require().NoError(err)
	// }
	// t.Log("created APIBinding 'core.platform-mesh.io' in orgs workspace")
	// suite.Assert().Eventually(func() bool {
	// 	var binding apisv1alpha2.APIBinding
	// 	if err := cli.Cluster(orgsClusterPath).Get(ctx, client.ObjectKey{Name: orgsPlatformMeshBinding.Name}, &binding); err != nil {
	// 		return false
	// 	}
	// 	return binding.Status.Phase == apisv1alpha2.APIBindingPhaseBound
	// }, 10*time.Second, 200*time.Millisecond, "APIBinding core.platform-mesh.io in orgs should be bound")

	var endpointSlice apisv1alpha1.APIExportEndpointSlice
	suite.Assert().Eventually(func() bool {
		err := cli.Cluster(platformMeshSystemClusterPath).Get(ctx, client.ObjectKey{Name: "core.platform-mesh.io"}, &endpointSlice)
		if err != nil {
			return false
		}
		return len(endpointSlice.Status.APIExportEndpoints) > 0 && endpointSlice.Status.APIExportEndpoints[0].URL != ""
	}, 10*time.Second, 200*time.Millisecond, "KCP should automatically create APIExportEndpointSlice with populated endpoints")

	suite.Require().NotEmpty(endpointSlice.Status.APIExportEndpoints, "APIExportEndpointSlice should have at least one endpoint")
	suite.Require().NotEqual("", endpointSlice.Status.APIExportEndpoints[0].URL, "APIExportEndpointSlice endpoint URL should not be empty")

	// set up config for virtual workspace
	cfg := rest.CopyConfig(suite.kcpConfig)
	cfg.Host = endpointSlice.Status.APIExportEndpoints[0].URL
	suite.apiExportEndpointSliceConfig = cfg
	t.Logf("created apiExportEndpointSliceConfig with host: %s", suite.apiExportEndpointSliceConfig.Host)
}

func (suite *IntegrationSuite) setupControllers(defaultCfg *platformeshconfig.CommonServiceConfig, testLogger *logger.Logger) {
	ctx := suite.T().Context()

	providerConfig, err := suite.getPlatformMeshSystemConfig(suite.apiExportEndpointSliceConfig)
	suite.Require().NoError(err)

	provider, err := apiexport.New(providerConfig, "core.platform-mesh.io", apiexport.Options{Scheme: scheme.Scheme})
	suite.Require().NoError(err)

	mgr, err := mcmanager.New(providerConfig, provider, mcmanager.Options{
		Scheme: scheme.Scheme,
	})
	suite.Require().NoError(err)

	err = controller.NewAPIBindingReconciler(testLogger, mgr).SetupWithManager(mgr, defaultCfg)
	suite.Require().NoError(err)

	managerCtx, cancel := context.WithCancel(ctx)
	go func() {
		if err := mgr.Start(managerCtx); err != nil {
			suite.T().Logf("controller manager exited with error: %v", err)
		}
	}()

	suite.T().Cleanup(func() {
		cancel()
	})

	// Start org LogicalCluster controller (initializingworkspaces provider + LogicalClusterReconciler)
	suite.setupOrgLogicalClusterController(ctx, defaultCfg, testLogger)
}

func (suite *IntegrationSuite) setupOrgLogicalClusterController(ctx context.Context, defaultCfg *platformeshconfig.CommonServiceConfig, testLogger *logger.Logger) {
	coreModuleFile, err := os.CreateTemp("", "security-operator-core-module-*.fga")
	suite.Require().NoError(err)
	_, err = coreModuleFile.WriteString("model 1.0\n")
	suite.Require().NoError(err)
	suite.Require().NoError(coreModuleFile.Close())
	suite.T().Cleanup(func() { _ = os.Remove(coreModuleFile.Name()) })

	initializerCfg := config.Config{}
	initializerCfg.CoreModulePath = coreModuleFile.Name()
	initializerCfg.WorkspaceTypeName = "security"
	initializerCfg.WorkspacePath = "root"

	rootCfg, err := suite.getRootConfig(suite.kcpConfig)
	suite.Require().NoError(err)
	initProvider, err := initializingworkspaces.New(rootCfg, initializerCfg.WorkspaceTypeName, initializingworkspaces.Options{Scheme: scheme.Scheme})
	suite.Require().NoError(err)

	initMgr, err := mcmanager.New(suite.kcpConfig, initProvider, mcmanager.Options{Scheme: scheme.Scheme})
	suite.Require().NoError(err)

	orgsCli, err := clusterclient.New(suite.kcpConfig, client.Options{Scheme: scheme.Scheme})
	suite.Require().NoError(err)
	orgClient := orgsCli.Cluster(suite.orgsPath)

	inClusterClient, err := client.New(suite.kcpConfig, client.Options{Scheme: scheme.Scheme})
	suite.Require().NoError(err)

	suite.Require().NoError(controller.NewOrgLogicalClusterReconciler(testLogger, orgClient, initializerCfg, inClusterClient, initMgr).
		SetupWithManager(initMgr, defaultCfg, predicates.LogicalClusterIsAccountTypeOrg()))

	initManagerCtx, initCancel := context.WithCancel(ctx)
	go func() {
		if err := initMgr.Start(initManagerCtx); err != nil {
			suite.T().Logf("org LogicalCluster controller manager exited with error: %v", err)
		}
	}()

	suite.T().Cleanup(func() {
		initCancel()
	})
}

func (suite *IntegrationSuite) createAccount(ctx context.Context, client client.Client, accountName string, accountType accountv1alpha1.AccountType, t *testing.T) {
	account := &accountv1alpha1.Account{
		ObjectMeta: metav1.ObjectMeta{
			Name: accountName,
		},
		Spec: accountv1alpha1.AccountSpec{
			Type: accountType,
		},
	}
	err := client.Create(ctx, account)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}
	t.Logf("created account '%s' (type: %s)", accountName, accountType)
}

func (suite *IntegrationSuite) createAccountInfo(ctx context.Context, accountClient client.Client, accountName, orgName string, accountPath, orgPath logicalcluster.Path, t *testing.T) {
	accountInfo := &accountv1alpha1.AccountInfo{
		ObjectMeta: metav1.ObjectMeta{
			Name: "account",
		},
		Spec: accountv1alpha1.AccountInfoSpec{
			Organization: accountv1alpha1.AccountLocation{
				Name:               orgName,
				GeneratedClusterId: orgPath.String(),
				OriginClusterId:    orgPath.String(),
				Path:               orgPath.String(),
				Type:               accountv1alpha1.AccountTypeOrg,
			},
			Account: accountv1alpha1.AccountLocation{
				Name:               accountName,
				GeneratedClusterId: accountPath.String(),
				OriginClusterId:    accountPath.String(),
				Path:               accountPath.String(),
				Type:               accountv1alpha1.AccountTypeAccount,
			},
			FGA: accountv1alpha1.FGAInfo{
				Store: accountv1alpha1.StoreInfo{
					Id: "test-store-id",
				},
			},
		},
	}
	err := accountClient.Create(ctx, accountInfo)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}
	t.Logf("created accountInfo 'account' in %s workspace", accountPath)
}

func (suite *IntegrationSuite) getPlatformMeshSystemConfig(cfg *rest.Config) (*rest.Config, error) {
	providerConfig := rest.CopyConfig(cfg)

	parsed, err := url.Parse(providerConfig.Host)
	if err != nil {
		return nil, fmt.Errorf("unable to parse URL: %w", err)
	}

	parsed.Path, err = url.JoinPath("clusters", suite.platformMeshSysPath.String())
	if err != nil {
		return nil, fmt.Errorf("failed to join path")
	}
	providerConfig.Host = parsed.String()

	return providerConfig, nil
}

func (suite *IntegrationSuite) getRootConfig(cfg *rest.Config) (*rest.Config, error) {
	providerConfig := rest.CopyConfig(cfg)

	parsed, err := url.Parse(providerConfig.Host)
	if err != nil {
		return nil, fmt.Errorf("unable to parse URL: %w", err)
	}

	parsed.Path, err = url.JoinPath("clusters", "root")
	if err != nil {
		return nil, fmt.Errorf("failed to join path")
	}
	providerConfig.Host = parsed.String()

	return providerConfig, nil
}

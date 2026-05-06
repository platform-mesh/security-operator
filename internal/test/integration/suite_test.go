package test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	openfga "github.com/openfga/api/proto/openfga/v1"
	accountv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	acctcfg "github.com/platform-mesh/account-operator/pkg/config"
	acctsetup "github.com/platform-mesh/account-operator/pkg/controllersetup"
	platformeshconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/logger"
	securityv1alpha1 "github.com/platform-mesh/security-operator/api/v1alpha1"
	iclient "github.com/platform-mesh/security-operator/internal/client"
	secconfig "github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/security-operator/internal/controller"
	ifga "github.com/platform-mesh/security-operator/internal/fga"
	"github.com/platform-mesh/security-operator/internal/predicates"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/yaml"

	kapiv1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"

	"github.com/kcp-dev/logicalcluster/v3"
	"github.com/kcp-dev/multicluster-provider/apiexport"
	clusterclient "github.com/kcp-dev/multicluster-provider/client"
	"github.com/kcp-dev/multicluster-provider/envtest"
	pathaware "github.com/kcp-dev/multicluster-provider/path-aware"
	kcpapisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
	kcpapisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
	kcpcore "github.com/kcp-dev/sdk/apis/core"
	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
	kcptenancyv1alpha1 "github.com/kcp-dev/sdk/apis/tenancy/v1alpha1"

	_ "embed"
)

const (
	openfgaImage    = "openfga/openfga:latest"
	openfgaGRPCPort = "8081/tcp"
	openfgaHTTPPort = "8080/tcp"
)

// integrationFGAModule is minimal valid syntax for WorkspaceInitializer/OpenFGA.
const integrationFGAModule = `
module core

type user

type role
  relations
	define assignee: [user,user:*]

type core_platform-mesh_io_account
	relations
		define owner: [role#assignee]
		define member: [role#assignee]
`

const (
	// integrationBootstrapOIDCAudience is the OAuth client_id written into
	// patched AccountInfo.Spec.OIDC.Clients in tests; WorkspaceAuth uses it
	// as a JWT audience alongside production IDP-provisioned clients.
	integrationBootstrapOIDCAudience = "integration-bootstrap-audience"
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

	//go:embed yaml/apiresourceschema-invites.core.platform-mesh.io.yaml
	InviteSchemaYAML []byte

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

	//go:embed yaml/workspace-type-security.yaml
	WorkspaceTypeSecurityYAML []byte

	//go:embed yaml/account-root-org.yaml
	AccountRootOrgYAML []byte
)

func init() {
	utilruntime.Must(kcpapisv1alpha1.AddToScheme(scheme.Scheme))
	utilruntime.Must(kcpcorev1alpha1.AddToScheme(scheme.Scheme))
	utilruntime.Must(kcptenancyv1alpha1.AddToScheme(scheme.Scheme))
	utilruntime.Must(accountv1alpha1.AddToScheme(scheme.Scheme))
	utilruntime.Must(securityv1alpha1.AddToScheme(scheme.Scheme))
	utilruntime.Must(kcpapisv1alpha2.AddToScheme(scheme.Scheme))
	utilruntime.Must(kapiv1.AddToScheme(scheme.Scheme))
}

type IntegrationSuite struct {
	suite.Suite
	env                          *envtest.Environment
	kcpConfig                    *rest.Config
	apiExportEndpointSliceConfig *rest.Config
	platformMeshSysPath          logicalcluster.Path
	platformMeshSystemClient     client.Client
	kcpCli                       clusterclient.ClusterClient
	orgsClusterPath              logicalcluster.Path
	rootClient                   client.Client
	rootOrgsClient               client.Client
	rootOrgsDefaultClient        client.Client

	openFGAContainer testcontainers.Container
	openFGAConn      *grpc.ClientConn
	openFGAClient    openfga.OpenFGAServiceClient
}

func TestIntegrationSuite(t *testing.T) {
	suite.Run(t, new(IntegrationSuite))
}

func (suite *IntegrationSuite) SetupSuite() {
	defaultCfg := platformeshconfig.NewDefaultConfig()

	logcfg := logger.DefaultConfig()
	logcfg.NoJSON = true
	// logcfg.Output = io.Discard

	testLogger, err := logger.New(logcfg)
	require.NoError(suite.T(), err, "failed to create test logger")
	ctrl.SetLogger(testLogger.Logr())

	os.Setenv("KUBECONFIG", "/home/simt/src/security-operator/.kcp/admin.kubeconfig")
	os.Setenv("PRESERVE", "true")
	suite.env = &envtest.Environment{
		UseExistingKcp:     ptr.To(true),
		ExistingKcpContext: "base",
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
	suite.setupOpenFGA()
	coreDir := suite.T().TempDir()
	corePath := filepath.Join(coreDir, "core.fga")
	suite.Require().NoError(os.WriteFile(corePath, []byte(integrationFGAModule), 0o644))
	mgr := suite.setupControllers(defaultCfg, testLogger, corePath)
	suite.setupDefaultOrgAccount()

	suite.Assert().Eventually(func() bool {
		if _, err := mgr.GetCluster(suite.T().Context(), "root:orgs"); err != nil {
			suite.T().Logf("GetCluster root:orgs: %v", err)
			return false
		}
		return true
	}, 10*time.Second, 200*time.Millisecond, "cluster root:orgs should be available via manager")

	suite.awaitAndPatchAccountInfoOIDC(suite.T().Context(), suite.rootOrgsDefaultClient, "default")
}

func (suite *IntegrationSuite) TearDownSuite() {
	suite.tearDownOpenFGA()
}

// setupOpenFGA starts a local OpenFGA testcontainer, opens a gRPC client,
// and assigns suite.openFGAClient for reconcilers that talk to OpenFGA.
func (suite *IntegrationSuite) setupOpenFGA() {
	ctx := suite.T().Context()

	req := testcontainers.ContainerRequest{
		Image:        openfgaImage,
		Cmd:          []string{"run"},
		ExposedPorts: []string{openfgaGRPCPort, openfgaHTTPPort},
		WaitingFor:   wait.ForAll(wait.ForHTTP("/healthz").WithPort(openfgaHTTPPort)),
	}

	var err error
	suite.openFGAContainer, err = testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	suite.Require().NoError(err, "failed to start OpenFGA container")

	host, err := suite.openFGAContainer.Host(ctx)
	suite.Require().NoError(err)
	grpcPort, err := suite.openFGAContainer.MappedPort(ctx, openfgaGRPCPort)
	suite.Require().NoError(err)

	target := fmt.Sprintf("%s:%s", host, grpcPort.Port())
	suite.openFGAConn, err = grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	suite.Require().NoError(err, "failed to create gRPC connection to OpenFGA")
	suite.openFGAClient = openfga.NewOpenFGAServiceClient(suite.openFGAConn)
}

// tearDownOpenFGA releases the OpenFGA gRPC connection and terminates the
// container started by setupOpenFGA.
func (suite *IntegrationSuite) tearDownOpenFGA() {
	if err := suite.openFGAConn.Close(); err != nil {
		suite.T().Logf("failed to close OpenFGA connection: %v", err)
	}
	if err := suite.openFGAContainer.Terminate(suite.T().Context()); err != nil {
		suite.T().Logf("failed to terminate OpenFGA container: %v", err)
	}
}

// setupPlatformMesh prepares KCP for cross-operator tests: creates
// root:platform-mesh-system with APIResourceSchemas, core APIExport and
// APIBinding (patched with tenancy.kcp.io permission-claim identity hashes),
// root WorkspaceTypes, root:orgs and root:orgs:default clients, and the
// virtual-workspace rest.Config used by the API export provider.
func (suite *IntegrationSuite) setupPlatformMesh(t *testing.T) {
	ctx := suite.T().Context()

	var err error
	cli, err := clusterclient.New(suite.kcpConfig, client.Options{})
	suite.Require().NoError(err)

	suite.kcpCli = cli
	rootClient := cli.Cluster(kcpcore.RootCluster.Path())
	suite.rootClient = rootClient

	// create :root:platform-mesh-system ws
	_, platformMeshSystemClusterPath := envtest.NewWorkspaceFixture(suite.T(), cli, kcpcore.RootCluster.Path(), envtest.WithName("platform-mesh-system"))
	suite.platformMeshSysPath = platformMeshSystemClusterPath
	suite.platformMeshSystemClient = cli.Cluster(platformMeshSystemClusterPath)

	// register api-resource schemas
	schemas := [][]byte{AccountInfoSchemaYAML, AccountSchemaYAML, AuthorizationModelSchemaYAML, StoreSchemaYAML, InviteSchemaYAML}
	pmClient := cli.Cluster(platformMeshSystemClusterPath)

	for _, schemaYAML := range schemas {
		var schema kcpapisv1alpha1.APIResourceSchema
		suite.Require().NoError(yaml.Unmarshal(schemaYAML, &schema))
		err = pmClient.Create(ctx, &schema)
		if err != nil && !kerrors.IsAlreadyExists(err) {
			suite.Require().NoError(err)
		}
		suite.T().Logf("created APIResourceSchema: %s", schema.Name)
	}

	var apiExport kcpapisv1alpha1.APIExport
	suite.Require().NoError(yaml.Unmarshal(ApiExportPlatformMeshSystemYAML, &apiExport))

	err = pmClient.Create(ctx, &apiExport)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}

	identityHash := suite.awaitTenancyAPIExportIdentityHash(ctx, rootClient, "tenancy export identity hash on root")

	suite.Assert().Eventually(func() bool {
		var export kcpapisv1alpha1.APIExport
		if err := cli.Cluster(platformMeshSystemClusterPath).Get(ctx, client.ObjectKey{Name: apiExport.Name}, &export); err != nil {
			return false
		}
		appendTenancyAPIExportClaims(&export, identityHash)
		err := cli.Cluster(platformMeshSystemClusterPath).Update(ctx, &export)
		if err != nil {
			suite.T().Logf("APIExport tenancy claims update: %v", err)
			return false
		}
		return true
	}, 10*time.Second, 200*time.Millisecond, "APIExport tenancy permission claims should persist")

	var platformMeshBinding kcpapisv1alpha2.APIBinding
	suite.Require().NoError(yaml.Unmarshal(ApiBindingCorePlatformMeshYAML, &platformMeshBinding))

	err = cli.Cluster(platformMeshSystemClusterPath).Create(ctx, &platformMeshBinding)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}

	suite.Assert().Eventually(func() bool {
		var binding kcpapisv1alpha2.APIBinding
		if err := cli.Cluster(platformMeshSystemClusterPath).Get(ctx, client.ObjectKey{Name: platformMeshBinding.Name}, &binding); err != nil {
			return false
		}
		appendTenancyAPIBindingClaims(&binding, identityHash)
		err := cli.Cluster(platformMeshSystemClusterPath).Update(ctx, &binding)
		if err != nil {
			suite.T().Logf("APIBinding tenancy claims update: %v", err)
			return false
		}
		return true
	}, 10*time.Second, 200*time.Millisecond, "APIBinding tenancy permission claims should persist")

	t.Log("created APIBinding 'core.platform-mesh.io' in platform-mesh-system workspace")
	suite.Assert().Eventually(func() bool {
		var binding kcpapisv1alpha2.APIBinding
		if err := cli.Cluster(platformMeshSystemClusterPath).Get(ctx, client.ObjectKey{Name: platformMeshBinding.Name}, &binding); err != nil {
			return false
		}
		return binding.Status.Phase == kcpapisv1alpha2.APIBindingPhaseBound
	}, 10*time.Second, 200*time.Millisecond, "APIBinding core.platform-mesh.io should be bound")

	// Create WorkspaceTypes in root workspace
	var orgWorkspaceType kcptenancyv1alpha1.WorkspaceType
	suite.Require().NoError(yaml.Unmarshal(WorkspaceTypeOrgYAML, &orgWorkspaceType))

	err = rootClient.Create(ctx, &orgWorkspaceType)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}
	t.Log("created WorkspaceType 'org' in root workspace")

	var orgsWorkspaceType kcptenancyv1alpha1.WorkspaceType
	suite.Require().NoError(yaml.Unmarshal(WorkspaceTypeOrgsYAML, &orgsWorkspaceType))

	err = rootClient.Create(ctx, &orgsWorkspaceType)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}
	t.Log("created WorkspaceType 'orgs' in root workspace")

	var accountWorkspaceType kcptenancyv1alpha1.WorkspaceType
	suite.Require().NoError(yaml.Unmarshal(WorkspaceTypeAccountYAML, &accountWorkspaceType))

	err = rootClient.Create(ctx, &accountWorkspaceType)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}
	t.Log("created WorkspaceType 'account' in root workspace")

	var securityWorkspaceType kcptenancyv1alpha1.WorkspaceType
	suite.Require().NoError(yaml.Unmarshal(WorkspaceTypeSecurityYAML, &securityWorkspaceType))

	err = rootClient.Create(ctx, &securityWorkspaceType)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}
	t.Log("created WorkspaceType 'security' in root workspace")

	// create :root:orgs ws
	orgsWs, orgsClusterPath := envtest.NewWorkspaceFixture(suite.T(), cli, kcpcore.RootCluster.Path(), envtest.WithName("orgs"), envtest.WithType(kcpcore.RootCluster.Path(), "orgs"))
	t.Logf("orgs workspace path (%s), cluster id (%s)", orgsClusterPath, orgsWs.Spec.Cluster)
	suite.orgsClusterPath = orgsClusterPath
	suite.rootOrgsClient = cli.Cluster(orgsClusterPath)
	suite.rootOrgsDefaultClient = cli.Cluster(orgsClusterPath.Join("default"))

	var endpointSlice kcpapisv1alpha1.APIExportEndpointSlice
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

// setupControllers constructs the multicluster manager, registers security
// operator reconcilers (store, org and account logical clusters, API
// binding) and account-operator reconcilers, then runs the manager in a
// background goroutine with suite-scoped cleanup.
func (suite *IntegrationSuite) setupControllers(defaultCfg *platformeshconfig.CommonServiceConfig, testLogger *logger.Logger, coreModulePath string) mcmanager.Manager {
	ctx := suite.T().Context()

	providerConfig, err := suite.getPlatformMeshSystemConfig(suite.apiExportEndpointSliceConfig)
	suite.Require().NoError(err)

	provider, err := pathaware.New(providerConfig, "core.platform-mesh.io", apiexport.Options{Scheme: scheme.Scheme})
	suite.Require().NoError(err)

	mgr, err := mcmanager.New(providerConfig, provider, mcmanager.Options{Scheme: scheme.Scheme})
	suite.Require().NoError(err)

	operatorCfg := secconfig.NewConfig()
	operatorCfg.FGA.Target = suite.openFGAConn.Target()
	operatorCfg.CoreModulePath = coreModulePath
	operatorCfg.Initializer.IDPEnabled = false
	operatorCfg.Initializer.InviteEnabled = false
	operatorCfg.Initializer.WorkspaceInitializerEnabled = true
	operatorCfg.Initializer.WorkspaceAuthEnabled = true
	suite.Require().NotEmpty(operatorCfg.InitializerName())

	storeIDGetter := ifga.NewCachingStoreIDGetter(suite.openFGAClient, operatorCfg.FGA.StoreIDCacheTTL, ctx, testLogger)
	kcpCombinedGetter := iclient.NewManagerKCPClientGetter(mgr)

	storeReconciler := controller.NewStoreReconciler(ctx, testLogger, suite.openFGAClient, mgr, kcpCombinedGetter, &operatorCfg)
	suite.Require().NoError(storeReconciler.SetupWithManager(mgr, defaultCfg))

	runtimeClient := mgr.GetLocalManager().GetClient()
	orgOpts := controller.ControllerOptions{Name: "OrgLogicalClusterReconciler", InitializerName: operatorCfg.InitializerName()}
	orgReconciler, err := controller.NewOrgLogicalClusterController(testLogger, kcpCombinedGetter, operatorCfg, runtimeClient, mgr, orgOpts)
	suite.Require().NoError(err)
	err = orgReconciler.SetupWithManager(mgr, defaultCfg, predicates.LogicalClusterIsAccountTypeOrg(), predicates.HasInitializerPredicate(operatorCfg.InitializerName()))
	suite.Require().NoError(err)

	alcOpts := controller.ControllerOptions{Name: "AccountLogicalClusterReconciler", InitializerName: operatorCfg.InitializerName()}
	alcReconciler, err := controller.NewAccountLogicalClusterController(testLogger, operatorCfg, suite.openFGAClient, storeIDGetter, mgr, kcpCombinedGetter, alcOpts)
	suite.Require().NoError(err)
	err = alcReconciler.SetupWithManager(mgr, defaultCfg, predicate.Not(predicates.LogicalClusterIsAccountTypeOrg()), predicates.HasInitializerPredicate(operatorCfg.InitializerName()))
	suite.Require().NoError(err)

	suite.Require().NoError(controller.NewAPIBindingReconciler(testLogger, mgr, iclient.NewManagerKCPClientGetter(mgr), &operatorCfg).SetupWithManager(mgr, defaultCfg))

	accountOpCfg := acctcfg.NewOperatorConfig()
	accountOpCfg.Kcp.ProviderWorkspace = kcpcore.RootCluster.Path().String()

	suite.Require().NoError(acctsetup.InstallAccountAndAccountInfoReconcilers(testLogger, mgr, accountOpCfg, defaultCfg))

	managerCtx, cancel := context.WithCancel(ctx)
	go func() {
		if err := mgr.Start(managerCtx); err != nil {
			suite.T().Logf("controller manager exited with error: %v", err)
		}
	}()

	suite.T().Cleanup(func() {
		cancel()
	})

	return mgr
}

// createAccount ensures a cluster-scoped Account exists (idempotent create);
// used by tests that need an Account CR without duplicating object literals.
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

// createAccountInfo ensures the singleton AccountInfo named "account" exists
// with synthetic org/account locations and FGA store metadata for tests that
// bypass full account-operator reconciliation.
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

// getPlatformMeshSystemConfig returns a copy of cfg whose Host targets the
// root:platform-mesh-system logical cluster for API export virtual-workspace
// requests.
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

// awaitTenancyAPIExportIdentityHash blocks until the root tenancy.kcp.io
// APIExport has Status.IdentityHash set, which is required when declaring
// workspaces and workspacetypes permission claims on other exports.
func (suite *IntegrationSuite) awaitTenancyAPIExportIdentityHash(ctx context.Context, rootCli client.Client, waitMsg string) string {
	var tenancyExport kcpapisv1alpha1.APIExport
	suite.Assert().Eventually(func() bool {
		if err := rootCli.Get(ctx, types.NamespacedName{Name: "tenancy.kcp.io"}, &tenancyExport); err != nil {
			return false
		}
		return tenancyExport.Status.IdentityHash != ""
	}, 120*time.Second, 250*time.Millisecond, waitMsg)

	suite.Require().NotEmpty(tenancyExport.Status.IdentityHash)
	return tenancyExport.Status.IdentityHash
}

// setupDefaultOrgAccount applies the embedded YAML Account for the default
// org under root:orgs so suite and tests can assume that org seed exists.
func (suite *IntegrationSuite) setupDefaultOrgAccount() {
	ctx := suite.T().Context()
	var account accountv1alpha1.Account
	suite.Require().NoError(yaml.Unmarshal(AccountRootOrgYAML, &account))
	err := suite.rootOrgsClient.Create(ctx, &account)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}
	suite.T().Logf("ensured Account %s in root:orgs", account.Name)
}

// awaitOrgWorkspaceFromAccount waits until account-operator has created the
// Workspace named wsName under root:orgs (matching an org-type Account CR)
// and Workspace.Status.Phase is Ready.
func (suite *IntegrationSuite) awaitOrgWorkspaceFromAccount(ctx context.Context, wsName string, timeout time.Duration, tick time.Duration) logicalcluster.Path {
	var ws kcptenancyv1alpha1.Workspace
	suite.Require().Eventually(func() bool {
		err := suite.rootOrgsClient.Get(ctx, client.ObjectKey{Name: wsName}, &ws)
		if err != nil {
			return false
		}
		return ws.Status.Phase == kcpcorev1alpha1.LogicalClusterPhaseReady
	}, timeout, tick,
		"workspace %s under root:orgs should be created and Ready by Account reconciler", wsName)

	return suite.orgsClusterPath.Join(wsName)
}

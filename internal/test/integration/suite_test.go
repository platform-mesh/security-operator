package test

import (
	"context"
	_ "embed"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	eventsv1 "k8s.io/api/events/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	apisv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcpapiv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	apisv1alpha2 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha2"
	"github.com/kcp-dev/kcp/sdk/apis/core"
	corev1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	kcptenancyv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	tenancyv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	topologyv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/topology/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"
	clusterclient "github.com/kcp-dev/multicluster-provider/client"
	"github.com/kcp-dev/multicluster-provider/envtest"

	accountv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/golang-commons/logger"
	securityv1alpha1 "github.com/platform-mesh/security-operator/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
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

var (
	env       *envtest.Environment
	kcpConfig *rest.Config
)

func init() {
	utilruntime.Must(apisv1alpha1.AddToScheme(scheme.Scheme))
	utilruntime.Must(corev1alpha1.AddToScheme(scheme.Scheme))
	utilruntime.Must(tenancyv1alpha1.AddToScheme(scheme.Scheme))
	utilruntime.Must(topologyv1alpha1.AddToScheme(scheme.Scheme))
	utilruntime.Must(eventsv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(accountv1alpha1.AddToScheme(scheme.Scheme))
	utilruntime.Must(securityv1alpha1.AddToScheme(scheme.Scheme))
}

type IntegrationSuite struct {
	suite.Suite
	rootConfig                   *rest.Config
	apiExportEndpointSliceConfig *rest.Config
	platformMeshSysPath          logicalcluster.Path
	testAccount1Path             logicalcluster.Path
	platformMeshSystemClient     client.Client
	tenancyIdentityHash          string
}

func TestIntegrationSuite(t *testing.T) {
	suite.Run(t, new(IntegrationSuite))
}

func (suite *IntegrationSuite) SetupSuite() {
	logcfg := logger.DefaultConfig()
	logcfg.Level = "warn"
	logcfg.NoJSON = true
	testLogger, err := logger.New(logcfg)
	require.NoError(suite.T(), err, "failed to create test logger")
	ctrl.SetLogger(testLogger.Logr())

	env = &envtest.Environment{}

	kcpConfig, err = env.Start()
	require.NoError(suite.T(), err, "failed to start envtest environment")

	suite.T().Cleanup(func() {
		if err := env.Stop(); err != nil {
			suite.T().Logf("error stopping envtest environment: %v", err)
		}
		suite.T().Log("kcp server has been stopped")
	})

	suite.setupPlatformMesh(suite.T())
}

func (suite *IntegrationSuite) setupPlatformMesh(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var err error
	cli, err := clusterclient.New(kcpConfig, client.Options{})
	suite.Require().NoError(err)

	rootClient := cli.Cluster(core.RootCluster.Path())

	// create :root:platform-mesh-system ws
	_, clusterPath := envtest.NewWorkspaceFixture(suite.T(), cli, core.RootCluster.Path(), envtest.WithName("platform-mesh-system"))
	suite.platformMeshSysPath = clusterPath
	suite.platformMeshSystemClient = cli.Cluster(clusterPath)

	// register api-resource schemas
	schemas := [][]byte{AccountInfoSchemaYAML, AccountSchemaYAML, AuthorizationModelSchemaYAML, StoreSchemaYAML}
	for _, schemaYAML := range schemas {
		var schema kcpapiv1alpha1.APIResourceSchema
		suite.Require().NoError(yaml.Unmarshal(schemaYAML, &schema))
		err = cli.Cluster(clusterPath).Create(ctx, &schema)
		if err != nil && !kerrors.IsAlreadyExists(err) {
			suite.Require().NoError(err)
		}
		suite.T().Logf("created APIResourceSchema: %s", schema.Name)
	}
	suite.Require().NoError(err)

	var apiExport apisv1alpha1.APIExport
	suite.Require().NoError(yaml.Unmarshal(ApiExportPlatformMeshSystemYAML, &apiExport))

	err = cli.Cluster(clusterPath).Create(ctx, &apiExport)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}

	var platformMeshBinding apisv1alpha2.APIBinding
	suite.Require().NoError(yaml.Unmarshal(ApiBindingCorePlatformMeshYAML, &platformMeshBinding))

	err = cli.Cluster(clusterPath).Create(ctx, &platformMeshBinding)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}
	t.Log("created APIBinding 'core.platform-mesh.io' in platform-mesh-system workspace")

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

	// create :root:orgs ws
	orgsWs, orgsClusterPath := envtest.NewWorkspaceFixture(suite.T(), cli, core.RootCluster.Path(), envtest.WithName("orgs"), envtest.WithType(core.RootCluster.Path(), kcptenancyv1alpha1.WorkspaceTypeName("orgs")))
	t.Logf("orgs workspace path (%s), cluster id (%s)", orgsClusterPath, orgsWs.Spec.Cluster)

	var endpointSlice kcpapiv1alpha1.APIExportEndpointSlice
	suite.Assert().Eventually(func() bool {
		err := cli.Cluster(clusterPath).Get(ctx, client.ObjectKey{Name: "core.platform-mesh.io"}, &endpointSlice)
		if err != nil {
			return false
		}
		return len(endpointSlice.Status.APIExportEndpoints) > 0 && endpointSlice.Status.APIExportEndpoints[0].URL != ""
	}, 10*time.Second, 200*time.Millisecond, "KCP should automatically create APIExportEndpointSlice with populated endpoints")

	suite.Require().NotEmpty(endpointSlice.Status.APIExportEndpoints, "APIExportEndpointSlice should have at least one endpoint")
	suite.Require().NotEqual("", endpointSlice.Status.APIExportEndpoints[0].URL, "APIExportEndpointSlice endpoint URL should not be empty")

	// set up config for virtual workspace
	cfg := rest.CopyConfig(kcpConfig)
	cfg.Host = endpointSlice.Status.APIExportEndpoints[0].URL
	suite.apiExportEndpointSliceConfig = cfg
	t.Logf("created apiExportEndpointSliceConfig with host: %s", suite.apiExportEndpointSliceConfig.Host)
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

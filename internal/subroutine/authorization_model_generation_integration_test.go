package subroutine_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	kcpapiv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcpcorev1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	kcptenancyv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	conditionsv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/third_party/conditions/apis/conditions/v1alpha1"
	"github.com/kcp-dev/multicluster-provider/envtest"
	accountv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	securityv1alpha1 "github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultTestTimeout  = 2 * time.Minute
	defaultTickInterval = 500 * time.Millisecond
)

type ConnectionTestSuite struct {
	suite.Suite
	rootConfig *rest.Config
	scheme     *runtime.Scheme
	testEnv    *envtest.Environment
}

func TestConnectionTestSuite(t *testing.T) {
	suite.Run(t, new(ConnectionTestSuite))
}

// to run integration tests the KCP_CUBECONFIG env variable has to be set up
func (suite *ConnectionTestSuite) SetupSuite() {
	suite.T().Logf("Setting up test suite...")

	suite.scheme = runtime.NewScheme()
	utilruntime.Must(accountv1alpha1.AddToScheme(suite.scheme))
	utilruntime.Must(kcptenancyv1alpha1.AddToScheme(suite.scheme))
	utilruntime.Must(kcpcorev1alpha1.AddToScheme(suite.scheme))
	utilruntime.Must(kcpapiv1alpha1.AddToScheme(suite.scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(suite.scheme))
	utilruntime.Must(securityv1alpha1.AddToScheme(suite.scheme))

	useExistingKcp := true
	if envValue, err := strconv.ParseBool(os.Getenv("USE_EXISTING_KCP")); err == nil {
		useExistingKcp = envValue
	}
	var restConfig *rest.Config

	if useExistingKcp {
		kubeconfigPath := os.Getenv("KCP_KUBECONFIG")
		if kubeconfigPath == "" {
			suite.T().Skipf("skipping integration test: KCP_KUBECONFIG environment variable is required when USE_EXISTING_KCP=true")
			return
		}

		suite.T().Logf("Using existing KCP cluster (USE_EXISTING_KCP=true)")
		suite.T().Logf("Loading kubeconfig from: %s", kubeconfigPath)

		kubeconfig, err := clientcmd.LoadFromFile(kubeconfigPath)
		if err != nil {
			suite.T().Skipf("skipping integration test: unable to load KCP kubeconfig from %s: %v", kubeconfigPath, err)
			return
		}

		clientConfig := clientcmd.NewDefaultClientConfig(*kubeconfig, nil)
		originalConfig, err := clientConfig.ClientConfig()
		if err != nil {
			suite.T().Skipf("skipping integration test: unable to create client config from kubeconfig %s: %v", kubeconfigPath, err)
			return
		}

		restConfig = rest.CopyConfig(originalConfig)

		// The envtest package expects the base URL without /clusters/ path
		if strings.Contains(restConfig.Host, "/clusters/") {
			parsed, err := url.Parse(restConfig.Host)
			if err != nil {
				suite.T().Skipf("skipping integration test: unable to parse host URL %s: %v", restConfig.Host, err)
				return
			}

			parsed.Path = ""
			restConfig.Host = parsed.String()
			suite.T().Logf("Stripped workspace path from host, using base URL: %s", restConfig.Host)
		}
	} else {
		suite.T().Logf("Starting local KCP server (USE_EXISTING_KCP=false or unset)")
	}

	suite.testEnv = &envtest.Environment{
		Scheme:          suite.scheme,
		Config:          restConfig,
		UseExistingKcp:  &useExistingKcp,
		AttachKcpOutput: os.Getenv("TEST_ATTACH_KCP_OUTPUT") == "true",
	}

	var err error
	suite.rootConfig, err = suite.testEnv.Start()
	if err != nil {
		suite.T().Skipf("skipping integration tests: unable to start KCP: %v", err)
		return
	}

	suite.T().Logf("KCP test environment started successfully")
}

func (suite *ConnectionTestSuite) TearDownSuite() {
	if suite.testEnv != nil {
		if err := suite.testEnv.Stop(); err != nil {
			suite.T().Logf("error stopping test environment: %v", err)
		}
	}
}

func (suite *ConnectionTestSuite) TestOrganizationCreation() {
	ctx := context.Background()
	orgName := "integration"
	creator := "olezhka1629@gmail.com"

	orgsClient, err := buildWorkspaceClient(suite.rootConfig, "root:orgs", suite.scheme)
	suite.Require().NoError(err)

	account := &accountv1alpha1.Account{
		ObjectMeta: metav1.ObjectMeta{
			Name: orgName,
		},
		Spec: accountv1alpha1.AccountSpec{
			Type:    accountv1alpha1.AccountTypeOrg,
			Creator: &creator,
		},
	}

	err = orgsClient.Create(ctx, account)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}

	createdAccount := &accountv1alpha1.Account{}
	suite.Assert().Eventually(func() bool {
		if err := orgsClient.Get(ctx, types.NamespacedName{Name: orgName}, createdAccount); err != nil {
			return false
		}
		return createdAccount.Spec.Type == accountv1alpha1.AccountTypeOrg
	}, defaultTestTimeout, defaultTickInterval)

	suite.Assert().Eventually(func() bool {
		if err := orgsClient.Get(ctx, types.NamespacedName{Name: orgName}, createdAccount); err != nil {
			return false
		}
		return meta.IsStatusConditionTrue(createdAccount.Status.Conditions, "WorkspaceSubroutine_Ready")
	}, defaultTestTimeout, defaultTickInterval)
}

func (suite *ConnectionTestSuite) TestAuthorizationModelGenerationAndFinalization() {
	ctx := suite.T().Context()
	orgName := "integration"
	creator := "olezhka1629@gmail.com"
	account1Name := "test-account-1"
	account2Name := "test-account-2"

	orgsClient, err := buildWorkspaceClient(suite.rootConfig, "root:orgs", suite.scheme)
	suite.Require().NoError(err)

	orgAccount := &accountv1alpha1.Account{
		ObjectMeta: metav1.ObjectMeta{
			Name: orgName,
		},
		Spec: accountv1alpha1.AccountSpec{
			Type:    accountv1alpha1.AccountTypeOrg,
			Creator: &creator,
		},
	}
	err = orgsClient.Create(ctx, orgAccount)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}

	orgWorkspacePath := fmt.Sprintf("root:orgs:%s", orgName)
	orgClient, err := buildWorkspaceClient(suite.rootConfig, orgWorkspacePath, suite.scheme)
	suite.Require().NoError(err)

	account1 := &accountv1alpha1.Account{
		ObjectMeta: metav1.ObjectMeta{
			Name: account1Name,
		},
		Spec: accountv1alpha1.AccountSpec{
			Type: accountv1alpha1.AccountTypeAccount,
		},
	}
	err = orgClient.Create(ctx, account1)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}

	account2 := &accountv1alpha1.Account{
		ObjectMeta: metav1.ObjectMeta{
			Name: account2Name,
		},
		Spec: accountv1alpha1.AccountSpec{
			Type: accountv1alpha1.AccountTypeAccount,
		},
	}
	err = orgClient.Create(ctx, account2)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}

	platformMeshSystemClient, err := buildWorkspaceClient(suite.rootConfig, "root:platform-mesh-system", suite.scheme)
	suite.Require().NoError(err)

	group := "integration.test.example.com"
	plural := "integrationtestresources"
	singular := "integrationtestresource"
	schemaName := fmt.Sprintf("v0000001.%s.%s", plural, group)

	resourceSchema := createAPIResourceSchema(
		schemaName,
		group,
		plural,
		singular,
		apiextensionsv1.NamespaceScoped,
	)

	err = platformMeshSystemClient.Create(ctx, resourceSchema)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}

	exportName := "integration-test.example.com"
	apiExport := createAPIExport(exportName, []string{schemaName})

	err = platformMeshSystemClient.Create(ctx, apiExport)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}

	account1WorkspacePath := fmt.Sprintf("root:orgs:%s:%s", orgName, account1Name)
	account2WorkspacePath := fmt.Sprintf("root:orgs:%s:%s", orgName, account2Name)

	account1Client, err := buildWorkspaceClient(suite.rootConfig, account1WorkspacePath, suite.scheme)
	suite.Require().NoError(err)

	account2Client, err := buildWorkspaceClient(suite.rootConfig, account2WorkspacePath, suite.scheme)
	suite.Require().NoError(err)

	binding1Name := "integration-test-binding-1"
	binding2Name := "integration-test-binding-2"
	exportPath := "root:platform-mesh-system"

	binding1 := &kcpapiv1alpha1.APIBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: binding1Name,
		},
		Spec: kcpapiv1alpha1.APIBindingSpec{
			Reference: kcpapiv1alpha1.BindingReference{
				Export: &kcpapiv1alpha1.ExportBindingReference{
					Name: exportName,
					Path: exportPath,
				},
			},
		},
	}
	err = account1Client.Create(ctx, binding1)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}

	binding2 := &kcpapiv1alpha1.APIBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: binding2Name,
		},
		Spec: kcpapiv1alpha1.APIBindingSpec{
			Reference: kcpapiv1alpha1.BindingReference{
				Export: &kcpapiv1alpha1.ExportBindingReference{
					Name: exportName,
					Path: exportPath,
				},
			},
		},
	}
	err = account2Client.Create(ctx, binding2)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}

	suite.Assert().Eventually(func() bool {
		var binding kcpapiv1alpha1.APIBinding
		if err := account1Client.Get(ctx, types.NamespacedName{Name: binding1Name}, &binding); err != nil {
			return false
		}
		if binding.Status.APIExportClusterName == "" {
			return false
		}
		return isConditionTrue(binding.Status.Conditions, conditionsv1alpha1.ReadyCondition)
	}, defaultTestTimeout, defaultTickInterval)

	suite.Assert().Eventually(func() bool {
		var binding kcpapiv1alpha1.APIBinding
		if err := account2Client.Get(ctx, types.NamespacedName{Name: binding2Name}, &binding); err != nil {
			return false
		}
		if binding.Status.APIExportClusterName == "" {
			return false
		}
		return isConditionTrue(binding.Status.Conditions, conditionsv1alpha1.ReadyCondition)
	}, defaultTestTimeout, defaultTickInterval)

	expectedModelName := "integrationtestresources-integration"
	suite.Assert().Eventually(func() bool {
		var authModel securityv1alpha1.AuthorizationModel
		if err := platformMeshSystemClient.Get(ctx, types.NamespacedName{Name: expectedModelName}, &authModel); err != nil {
			return false
		}
		return authModel.Spec.Model != "" && authModel.Spec.StoreRef.Name != ""
	}, defaultTestTimeout, defaultTickInterval)

	// finalize test
	err = account1Client.Get(ctx, types.NamespacedName{Name: binding1Name}, binding1)
	suite.Require().NoError(err)

	err = account1Client.Delete(ctx, binding1)
	suite.Require().NoError(err)

	suite.Assert().Eventually(func() bool {
		err := account1Client.Get(ctx, types.NamespacedName{Name: binding1Name}, &kcpapiv1alpha1.APIBinding{})
		return kerrors.IsNotFound(err)
	}, defaultTestTimeout, defaultTickInterval)

	suite.Assert().Eventually(func() bool {
		var authModel securityv1alpha1.AuthorizationModel
		if err := platformMeshSystemClient.Get(ctx, types.NamespacedName{Name: expectedModelName}, &authModel); err != nil {
			return false
		}
		return true
	}, defaultTestTimeout, defaultTickInterval)

	err = account2Client.Get(ctx, types.NamespacedName{Name: binding2Name}, binding2)
	suite.Require().NoError(err)

	err = account2Client.Delete(ctx, binding2)
	suite.Require().NoError(err)

	suite.Assert().Eventually(func() bool {
		err := account2Client.Get(ctx, types.NamespacedName{Name: binding2Name}, &kcpapiv1alpha1.APIBinding{})
		return kerrors.IsNotFound(err)
	}, defaultTestTimeout, defaultTickInterval)

	suite.Assert().Eventually(func() bool {
		err := platformMeshSystemClient.Get(ctx, types.NamespacedName{Name: expectedModelName}, &securityv1alpha1.AuthorizationModel{})
		return kerrors.IsNotFound(err)
	}, defaultTestTimeout, defaultTickInterval)
}

func buildWorkspaceClient(rootConfig *rest.Config, workspacePath string, scheme *runtime.Scheme) (client.Client, error) {
	cfg := rest.CopyConfig(rootConfig)

	parsed, err := url.Parse(cfg.Host)
	if err != nil {
		return nil, fmt.Errorf("failed to parse host URL: %w", err)
	}

	parsed.Path = fmt.Sprintf("/clusters/%s", workspacePath)
	cfg.Host = parsed.String()

	return client.New(cfg, client.Options{
		Scheme: scheme,
	})
}

func isConditionTrue(conditions conditionsv1alpha1.Conditions, conditionType conditionsv1alpha1.ConditionType) bool {
	for _, cond := range conditions {
		if cond.Type == conditionType && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func createAPIExport(name string, schemaNames []string) *kcpapiv1alpha1.APIExport {
	return &kcpapiv1alpha1.APIExport{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: kcpapiv1alpha1.APIExportSpec{
			LatestResourceSchemas: schemaNames,
			PermissionClaims: []kcpapiv1alpha1.PermissionClaim{
				{
					GroupResource: kcpapiv1alpha1.GroupResource{
						Group:    "apis.kcp.io",
						Resource: "apibindings",
					},
					All: true,
				},
			},
		},
	}
}

func createAPIResourceSchema(name, group, plural, singular string, scope apiextensionsv1.ResourceScope) *kcpapiv1alpha1.APIResourceSchema {
	kind := strings.ToUpper(singular[:1]) + singular[1:]
	listKind := kind + "List"

	return &kcpapiv1alpha1.APIResourceSchema{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: kcpapiv1alpha1.APIResourceSchemaSpec{
			Group: group,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:     kind,
				ListKind: listKind,
				Plural:   plural,
				Singular: singular,
			},
			Scope: scope,
			Versions: []kcpapiv1alpha1.APIResourceVersion{
				{
					Name:    "v1alpha1",
					Served:  true,
					Storage: true,
					Schema: runtime.RawExtension{
						Raw: []byte(fmt.Sprintf(`{
							"description": "%s is a test resource for integration tests",
							"type": "object",
							"properties": {
								"apiVersion": {"type": "string"},
								"kind": {"type": "string"},
								"metadata": {"type": "object"},
								"spec": {"type": "object"}
							}
						}`, kind)),
					},
				},
			},
		},
	}
}

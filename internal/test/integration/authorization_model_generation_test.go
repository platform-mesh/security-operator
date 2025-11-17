package test

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcpapiv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	"github.com/kcp-dev/kcp/sdk/apis/core"
	kcptenancyv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"
	"github.com/kcp-dev/multicluster-provider/apiexport"
	clusterclient "github.com/kcp-dev/multicluster-provider/client"
	"github.com/kcp-dev/multicluster-provider/envtest"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	accountv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	securityv1alpha1 "github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/subroutine"
)

func (suite *IntegrationSuite) TestAuthorizationModelGeneration_Process() {
	ctx := suite.T().Context()
	cli, err := clusterclient.New(kcpConfig, client.Options{})
	suite.Require().NoError(err)

	resourceSchemaName := "v1.testresources.process.test.example.com"
	suite.createTestAPIResourceSchema(ctx, suite.platformMeshSystemClient, resourceSchemaName, "process.test.example.com", "testresources", "testresource", apiextensionsv1.NamespaceScoped)

	apiExportName := "process-test.example.com"
	suite.createTestAPIExport(ctx, suite.platformMeshSystemClient, apiExportName, []string{resourceSchemaName})

	orgsPath := logicalcluster.NewPath("root:orgs")

	_, testOrgPath := envtest.NewWorkspaceFixture(suite.T(), cli, orgsPath, envtest.WithName("generator-test-process"), envtest.WithType(core.RootCluster.Path(), kcptenancyv1alpha1.WorkspaceTypeName("org")))

	_, testAccountPath := envtest.NewWorkspaceFixture(suite.T(), cli, testOrgPath, envtest.WithName("test-account"), envtest.WithType(core.RootCluster.Path(), kcptenancyv1alpha1.WorkspaceTypeName("account")))

	testAccountClient := cli.Cluster(testAccountPath)

	suite.createAccountInfo(ctx, testAccountClient, "test-account", "generator-test-process", testAccountPath, testOrgPath, suite.T())

	apiBinding := suite.createTestAPIBinding(ctx, testAccountClient, apiExportName, suite.platformMeshSysPath.String(), apiExportName)

	provider, err := apiexport.New(suite.apiExportEndpointSliceConfig, apiexport.Options{Scheme: scheme.Scheme})
	suite.Require().NoError(err)

	mgr := suite.createMulticlusterManager(kcpConfig, provider)

	eg, egCtx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return mgr.Start(egCtx)
	})
	eg.Go(func() error {
		return provider.Run(egCtx, mgr)
	})

	go func() {
		if err := eg.Wait(); err != nil {
			suite.T().Errorf("manager or provider failed: %v", err)
		}
	}()

	bindingClusterName := logicalcluster.From(apiBinding).String()
	suite.T().Logf("waiting for clusters to be discovered by the provider")
	suite.Assert().Eventually(func() bool {
		_, err := mgr.GetCluster(ctx, bindingClusterName)
		if err == nil {
			suite.T().Logf("discovered cluster: %s", bindingClusterName)
			return true
		}
		return false
	}, 5*time.Second, 200*time.Millisecond, "failed to discover cluster %s", bindingClusterName)

	allClient := suite.createAllClient(suite.apiExportEndpointSliceConfig)

	bindingCtx := mccontext.WithCluster(ctx, bindingClusterName)
	subroutine := subroutine.NewAuthorizationModelGenerationSubroutine(mgr, allClient)

	_, opErr := subroutine.Process(bindingCtx, apiBinding)
	if opErr != nil {
		suite.T().Logf("process error: %v", opErr.Err())
	}
	suite.Require().Nil(opErr, "process should succeed")
}

func (suite *IntegrationSuite) TestAuthorizationModelGeneration_Finalize() {
	ctx := suite.T().Context()
	cli, err := clusterclient.New(kcpConfig, client.Options{})
	suite.Require().NoError(err)

	pluralResourceSchemaName := "testresources"
	resourceSchemaName := "v1.testresources.generator.test.example.com"
	suite.createTestAPIResourceSchema(ctx, suite.platformMeshSystemClient, resourceSchemaName, "generator.test.example.com", pluralResourceSchemaName, "testresource", apiextensionsv1.NamespaceScoped)

	apiExportName := "generator-test.example.com"
	suite.createTestAPIExport(ctx, suite.platformMeshSystemClient, apiExportName, []string{resourceSchemaName})

	orgsPath := logicalcluster.NewPath("root:orgs")

	const (
		testAccount1Name = "test-account-1"
		testAccount2Name = "test-account-2"
		testOrgName      = "generator-test-finalize"
	)

	_, testOrgPath := envtest.NewWorkspaceFixture(suite.T(), cli, orgsPath, envtest.WithName(testOrgName), envtest.WithType(core.RootCluster.Path(), kcptenancyv1alpha1.WorkspaceTypeName("org")))
	testClient := cli.Cluster(testOrgPath)

	suite.createAccount(ctx, testClient, testAccount1Name, accountv1alpha1.AccountTypeAccount, suite.T())
	suite.createAccount(ctx, testClient, testAccount2Name, accountv1alpha1.AccountTypeAccount, suite.T())

	_, testAccount1Path := envtest.NewWorkspaceFixture(suite.T(), cli, testOrgPath, envtest.WithName(testAccount1Name), envtest.WithType(core.RootCluster.Path(), kcptenancyv1alpha1.WorkspaceTypeName("account")))
	_, testAccount2Path := envtest.NewWorkspaceFixture(suite.T(), cli, testOrgPath, envtest.WithName(testAccount2Name), envtest.WithType(core.RootCluster.Path(), kcptenancyv1alpha1.WorkspaceTypeName("account")))

	testAccount1Client := cli.Cluster(testAccount1Path)
	testAccount2Client := cli.Cluster(testAccount2Path)

	suite.createAccountInfo(ctx, testAccount1Client, testAccount1Name, testOrgName, testAccount1Path, testOrgPath, suite.T())
	suite.createAccountInfo(ctx, testAccount2Client, testAccount2Name, testOrgName, testAccount2Path, testOrgPath, suite.T())

	apiBinding1 := suite.createTestAPIBinding(ctx, testAccount1Client, apiExportName, suite.platformMeshSysPath.String(), apiExportName)
	apiBinding2 := suite.createTestAPIBinding(ctx, testAccount2Client, apiExportName, suite.platformMeshSysPath.String(), apiExportName)

	provider, err := apiexport.New(suite.apiExportEndpointSliceConfig, apiexport.Options{Scheme: scheme.Scheme})
	suite.Require().NoError(err)

	mgr := suite.createMulticlusterManager(kcpConfig, provider)

	eg, egCtx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return mgr.Start(egCtx)
	})
	eg.Go(func() error {
		return provider.Run(egCtx, mgr)
	})

	go func() {
		if err := eg.Wait(); err != nil {
			suite.T().Errorf("manager or provider failed: %v", err)
		}
	}()

	binding1ClusterName := logicalcluster.From(apiBinding1).String()
	binding2ClusterName := logicalcluster.From(apiBinding2).String()

	suite.T().Log("waiting for clusters to be discovered by the provider")
	suite.Assert().Eventually(func() bool {
		_, err1 := mgr.GetCluster(ctx, binding1ClusterName)
		_, err2 := mgr.GetCluster(ctx, binding2ClusterName)
		return err1 == nil && err2 == nil
	}, 5*time.Second, 200*time.Millisecond, "failed to discover clusters")

	allClient := suite.createAllClient(suite.apiExportEndpointSliceConfig)
	subroutine := subroutine.NewAuthorizationModelGenerationSubroutine(mgr, allClient)

	binding1Ctx := mccontext.WithCluster(ctx, binding1ClusterName)
	_, opErr := subroutine.Process(binding1Ctx, apiBinding1)
	if opErr != nil {
		suite.T().Logf("process binding1 error: %v", opErr.Err())
	}
	suite.Require().Nil(opErr, "process for binding1 should succeed")

	binding2Ctx := mccontext.WithCluster(ctx, binding2ClusterName)
	_, opErr = subroutine.Process(binding2Ctx, apiBinding2)
	if opErr != nil {
		suite.T().Logf("process binding2 error: %v", opErr.Err())
	}
	suite.Require().Nil(opErr, "process for binding2 should succeed")

	expectedModelName := fmt.Sprintf("%s-%s", pluralResourceSchemaName, testOrgName)
	var authModel securityv1alpha1.AuthorizationModel
	err = suite.platformMeshSystemClient.Get(ctx, client.ObjectKey{Name: expectedModelName}, &authModel)
	suite.Require().NoError(err, "authorizationModel should exist after both Process calls")

	finalize1Ctx := mccontext.WithCluster(ctx, binding1ClusterName)
	_, opErr = subroutine.Finalize(finalize1Ctx, apiBinding1)
	if opErr != nil {
		suite.T().Logf("finalize binding1 error: %v", opErr.Err())
	}
	suite.Require().Nil(opErr, "finalize for binding1 should succeed")

	err = testAccount1Client.Delete(ctx, apiBinding1)
	suite.Require().NoError(err)

	suite.Assert().Eventually(func() bool {
		var binding kcpapiv1alpha1.APIBinding
		err := testAccount1Client.Get(ctx, client.ObjectKey{Name: apiExportName}, &binding)
		return kerrors.IsNotFound(err)
	}, 5*time.Second, 200*time.Millisecond, "APIBinding1 should be deleted")

	err = suite.platformMeshSystemClient.Get(ctx, client.ObjectKey{Name: expectedModelName}, &authModel)
	suite.Require().NoError(err, "authorizationModel should still exist after deleting first binding")

	finalize2Ctx := mccontext.WithCluster(ctx, binding2ClusterName)
	_, opErr = subroutine.Finalize(finalize2Ctx, apiBinding2)
	if opErr != nil {
		suite.T().Logf("finalize binding2 error: %v", opErr.Err())
	}
	suite.Require().Nil(opErr, "finalize for binding2 should succeed")

	err = testAccount2Client.Delete(ctx, apiBinding2)
	suite.Require().NoError(err)

	suite.Assert().Eventually(func() bool {
		var binding kcpapiv1alpha1.APIBinding
		err := testAccount2Client.Get(ctx, client.ObjectKey{Name: apiExportName}, &binding)
		return kerrors.IsNotFound(err)
	}, 5*time.Second, 200*time.Millisecond, "APIBinding2 should be deleted")

	suite.Assert().Eventually(func() bool {
		var model securityv1alpha1.AuthorizationModel
		err := suite.platformMeshSystemClient.Get(ctx, client.ObjectKey{Name: expectedModelName}, &model)
		return kerrors.IsNotFound(err)
	}, 5*time.Second, 200*time.Millisecond, "authorizationModel should be deleted after deleting both bindings")
}

func (suite *IntegrationSuite) createTestAPIResourceSchema(ctx context.Context, client client.Client, name, group, plural, singular string, scope apiextensionsv1.ResourceScope) {
	kind := strings.ToUpper(singular[:1]) + singular[1:]
	listKind := kind + "List"

	schema := &kcpapiv1alpha1.APIResourceSchema{
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
						Raw: []byte(`{
							"description": "TestResource is a test resource for integration tests",
							"type": "object",
							"properties": {
								"apiVersion": {"type": "string"},
								"kind": {"type": "string"},
								"metadata": {"type": "object"},
								"spec": {"type": "object"}
							}
						}`),
					},
				},
			},
		},
	}

	err := client.Create(ctx, schema)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}
	suite.T().Logf("created test APIResourceSchema: %s", name)
}

func (suite *IntegrationSuite) createTestAPIExport(ctx context.Context, client client.Client, name string, resourceSchemas []string) {
	apiExport := &kcpapiv1alpha1.APIExport{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: kcpapiv1alpha1.APIExportSpec{
			LatestResourceSchemas: resourceSchemas,
			PermissionClaims: []kcpapiv1alpha1.PermissionClaim{
				{GroupResource: kcpapiv1alpha1.GroupResource{Group: "apis.kcp.io", Resource: "apibindings"}, All: true, IdentityHash: ""},
				{GroupResource: kcpapiv1alpha1.GroupResource{Group: "apis.kcp.io", Resource: "apiexports"}, All: true, IdentityHash: ""},
				{GroupResource: kcpapiv1alpha1.GroupResource{Group: "apis.kcp.io", Resource: "apiresourceschemas"}, All: true, IdentityHash: ""},
			},
		},
	}

	err := client.Create(ctx, apiExport)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}
	suite.T().Logf("created test APIExport: %s", name)
}

func (suite *IntegrationSuite) createTestAPIBinding(ctx context.Context, client client.Client, name, exportPath, exportName string) *kcpapiv1alpha1.APIBinding {
	binding := &kcpapiv1alpha1.APIBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: kcpapiv1alpha1.APIBindingSpec{
			Reference: kcpapiv1alpha1.BindingReference{
				Export: &kcpapiv1alpha1.ExportBindingReference{
					Path: exportPath,
					Name: exportName,
				},
			},
		},
	}

	err := client.Create(ctx, binding)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}
	suite.T().Logf("created APIBinding '%s'", name)
	return binding
}

func (suite *IntegrationSuite) createMulticlusterManager(baseConfig *rest.Config, provider *apiexport.Provider) mcmanager.Manager {
	rootConfig := rest.CopyConfig(baseConfig)
	rootParsed, err := url.Parse(rootConfig.Host)
	suite.Require().NoError(err)
	rootParsed.Path, err = url.JoinPath(rootParsed.Path, suite.platformMeshSysPath.RequestPath())
	suite.Require().NoError(err)
	rootConfig.Host = rootParsed.String()

	mgr, err := mcmanager.New(rootConfig, provider, mcmanager.Options{
		Scheme: scheme.Scheme,
	})
	suite.Require().NoError(err)
	return mgr
}

func (suite *IntegrationSuite) createAllClient(providerConfig *rest.Config) client.Client {
	allCfg := rest.CopyConfig(providerConfig)
	allCfgParsed, err := url.Parse(providerConfig.Host)
	suite.Require().NoError(err)
	allCfgParsed.Path, err = url.JoinPath(allCfgParsed.Path, "clusters", logicalcluster.Wildcard.String())
	suite.Require().NoError(err)
	allCfg.Host = allCfgParsed.String()

	allClient, err := client.New(allCfg, client.Options{Scheme: scheme.Scheme})
	suite.Require().NoError(err)
	return allClient
}

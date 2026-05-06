package test

import (
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	accountv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/account-operator/pkg/subroutines/manageaccountinfo"
	securityv1alpha1 "github.com/platform-mesh/security-operator/api/v1alpha1"

	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultTestOrgInitializerTimeout = 180 * time.Second
	defaultTestOrgInitializerTick    = 500 * time.Millisecond
)

// TestOrgLogicalCluster_InitializerProducesStoreAndOIDC registers an org-type
// Account under root:orgs with creator set. account-operator creates the
// nested org Workspace; WorkspaceSubroutine is the only producer of that
// Workspace in production, so this test waits for it instead of envtest
// fixtures. It patches AccountInfo OIDC for WorkspaceAuth, then asserts Store
// provisioning and OIDC client map state after initializers complete.
func (suite *IntegrationSuite) TestOrgLogicalCluster_InitializerProducesStoreAndOIDC() {
	ctx := suite.T().Context()

	const orgWSName = "e2e-init-org-store"
	creator := "e2e-org-owner@integration.test.example"
	account := accountv1alpha1.Account{
		ObjectMeta: metav1.ObjectMeta{Name: orgWSName},
		Spec: accountv1alpha1.AccountSpec{
			Type:        accountv1alpha1.AccountTypeOrg,
			Creator:     &creator,
			DisplayName: orgWSName,
		},
	}
	err := suite.rootOrgsClient.Create(ctx, &account)
	if err != nil && !errors.IsAlreadyExists(err) {
		suite.Require().NoError(err)
	}

	orgClusterPath := suite.awaitOrgWorkspaceFromAccount(ctx, orgWSName, defaultTestOrgInitializerTimeout, defaultTestOrgInitializerTick)
	orgClient := suite.kcpCli.Cluster(orgClusterPath)
	suite.awaitAndPatchAccountInfoOIDC(ctx, orgClient, orgWSName)

	var lc kcpcorev1alpha1.LogicalCluster

	suite.Assert().Eventually(func() bool {
		key := types.NamespacedName{Name: kcpcorev1alpha1.LogicalClusterName}
		if err := orgClient.Get(ctx, key, &lc); err != nil {
			return false
		}
		return lc.Status.Phase == kcpcorev1alpha1.LogicalClusterPhaseReady && len(lc.Status.Initializers) == 0
	}, defaultTestOrgInitializerTimeout, defaultTestOrgInitializerTick, "LogicalCluster Ready with Initializers cleared from status")

	suite.Assert().Eventually(func() bool {
		var st securityv1alpha1.Store
		if err := orgClient.Get(ctx, client.ObjectKey{Name: orgWSName}, &st); err != nil {
			return false
		}
		return st.Status.StoreID != ""
	}, defaultTestOrgInitializerTimeout, defaultTestOrgInitializerTick, "OpenFGA store ID populated")

	var ai accountv1alpha1.AccountInfo
	keyAI := types.NamespacedName{Name: manageaccountinfo.DefaultAccountInfoName}
	suite.Require().NoError(orgClient.Get(ctx, keyAI, &ai))
	suite.Require().NotNil(ai.Spec.OIDC)
	c, ok := ai.Spec.OIDC.Clients[orgWSName]
	suite.Require().True(ok, "workspace client key expected in OIDC.Clients map")
	suite.Require().Equal(integrationBootstrapOIDCAudience, c.ClientID)
}

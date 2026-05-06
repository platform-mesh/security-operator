package test

import (
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	accountv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

// TestOrgLogicalCluster_WorkspaceProducesReadyLogicalCluster creates an org
// Account under root:orgs; account-operator creates the child org Workspace
// (not envtest), then the test patches AccountInfo OIDC for WorkspaceAuth and
// asserts the nested LogicalCluster completes the root:security initializer.
func (suite *IntegrationSuite) TestOrgLogicalCluster_WorkspaceProducesReadyLogicalCluster() {
	ctx := suite.T().Context()

	const orgWSName = "lc-integration-org-ws-ready"
	creator := "lc-org-owner@integration.test.example"
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

	orgClusterPath := suite.awaitOrgWorkspaceFromAccount(ctx, orgWSName, 180*time.Second, 400*time.Millisecond)
	orgClient := suite.kcpCli.Cluster(orgClusterPath)
	suite.awaitAndPatchAccountInfoOIDC(ctx, orgClient, orgWSName)

	key := client.ObjectKey{Name: kcpcorev1alpha1.LogicalClusterName}
	var lc kcpcorev1alpha1.LogicalCluster
	suite.Assert().Eventually(func() bool {
		if err := orgClient.Get(ctx, key, &lc); err != nil {
			return false
		}
		return lc.Status.Phase == kcpcorev1alpha1.LogicalClusterPhaseReady && len(lc.Status.Initializers) == 0
	}, 5*time.Minute, 500*time.Millisecond, `LogicalCluster "cluster" should become Ready once initializers finish`)
}

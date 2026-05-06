package test

import (
	"context"
	"fmt"
	"time"

	accountv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/account-operator/pkg/subroutines/manageaccountinfo"
	secconfig "github.com/platform-mesh/security-operator/internal/config"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// awaitAndPatchAccountInfoOIDC waits until the org-scoped AccountInfo
// singleton exists, then patches Spec.OIDC with a test issuer URL and a
// Clients map entry so WorkspaceAuthConfiguration can build JWT audiences
// without IDPSubroutine (production uses Keycloak provisioning instead).
func (suite *IntegrationSuite) awaitAndPatchAccountInfoOIDC(ctx context.Context, orgScopedClient client.Client, workspaceName string) {
	baseDomain := secconfig.NewConfig().BaseDomain
	desired := accountv1alpha1.OIDCInfo{
		IssuerURL: fmt.Sprintf("https://%s/keycloak/realms/%s", baseDomain, workspaceName),
		Clients: map[string]accountv1alpha1.ClientInfo{
			workspaceName: {ClientID: integrationBootstrapOIDCAudience},
		},
	}
	key := types.NamespacedName{Name: manageaccountinfo.DefaultAccountInfoName}

	suite.Require().Eventually(func() bool {
		var ai accountv1alpha1.AccountInfo
		if err := orgScopedClient.Get(ctx, key, &ai); err != nil {
			return false
		}

		cloned := ai.DeepCopy()
		cloned.Spec.OIDC = desired.DeepCopy()

		if err := orgScopedClient.Patch(ctx, cloned, client.MergeFrom(&ai)); err != nil {
			suite.T().Logf("patch AccountInfo OIDC: %v", err)
			return false
		}
		return true
	}, 20*time.Minute, 250*time.Millisecond, "persist AccountInfo OIDC for workspace %s", workspaceName)
}

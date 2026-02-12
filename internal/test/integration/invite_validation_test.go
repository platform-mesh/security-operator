package test

import (
	"fmt"

	securityv1alpha1 "github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/stretchr/testify/require"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (suite *IntegrationSuite) TestInviteEmailValidation() {
	ctx := suite.T().Context()

	invalid := &securityv1alpha1.Invite{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "invalid-email-",
		},
		Spec: securityv1alpha1.InviteSpec{
			Email: "not-an-email",
		},
	}

	err := suite.platformMeshSystemClient.Create(ctx, invalid)
	require.Error(suite.T(), err)
	require.Truef(
		suite.T(),
		kerrors.IsInvalid(err) || kerrors.IsBadRequest(err),
		"expected validation error when creating Invite with invalid spec.email, got: %v",
		err,
	)

	valid := &securityv1alpha1.Invite{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "valid-email-",
		},
		Spec: securityv1alpha1.InviteSpec{
			Email: "user@example.com",
		},
	}

	require.NoError(suite.T(), suite.platformMeshSystemClient.Create(ctx, valid))
	suite.T().Cleanup(func() {
		if err := suite.platformMeshSystemClient.Delete(ctx, valid); err != nil && !kerrors.IsNotFound(err) {
			suite.T().Logf("failed to delete Invite %q: %v", valid.Name, err)
		}
	})

	require.NotEmpty(suite.T(), valid.Name, fmt.Sprintf("expected server to assign name for Invite, got: %q", valid.Name))
}

package webhook

import (
	"context"
	"testing"

	"github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type fakeRealmChecker struct {
	exists bool
	err    error
}

func (f fakeRealmChecker) RealmExists(ctx context.Context, realmName string) (bool, error) {
	return f.exists, f.err
}

func TestIdentityProviderConfigurationValidator_ValidateCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("master realm is denied", func(t *testing.T) {
		v := &identityProviderConfigurationValidator{keycloakClient: fakeRealmChecker{exists: false}}
		_, err := v.ValidateCreate(ctx, &v1alpha1.IdentityProviderConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "master"}})
		require.Error(t, err)
	})

	t.Run("existing realm is denied", func(t *testing.T) {
		v := &identityProviderConfigurationValidator{keycloakClient: fakeRealmChecker{exists: true}}
		_, err := v.ValidateCreate(ctx, &v1alpha1.IdentityProviderConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "org-1"}})
		require.Error(t, err)
	})

	t.Run("non-existing realm is allowed", func(t *testing.T) {
		v := &identityProviderConfigurationValidator{keycloakClient: fakeRealmChecker{exists: false}}
		_, err := v.ValidateCreate(ctx, &v1alpha1.IdentityProviderConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "org-2"}})
		require.NoError(t, err)
	})

	t.Run("empty realm name is denied", func(t *testing.T) {
		v := &identityProviderConfigurationValidator{keycloakClient: fakeRealmChecker{exists: false}}
		_, err := v.ValidateCreate(ctx, &v1alpha1.IdentityProviderConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "  "}})
		require.Error(t, err)
	})
}

func TestIdentityProviderConfigurationValidator_ValidateUpdate(t *testing.T) {
	v := &identityProviderConfigurationValidator{keycloakClient: fakeRealmChecker{exists: true}}
	_, err := v.ValidateUpdate(context.Background(), &v1alpha1.IdentityProviderConfiguration{}, &v1alpha1.IdentityProviderConfiguration{})
	require.NoError(t, err)
}

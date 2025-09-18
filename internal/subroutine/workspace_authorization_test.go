package subroutine

import (
	"context"
	"errors"
	"testing"

	kcpv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	kcptenancyv1alphav1 "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/subroutine/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestWorkspaceAuthSubroutine_Process(t *testing.T) {
	tests := []struct {
		name           string
		logicalCluster *kcpv1alpha1.LogicalCluster
		baseDomain     string
		setupMocks     func(*mocks.MockClient)
		expectError    bool
		expectedResult ctrl.Result
	}{
		{
			name: "success - create new WorkspaceAuthenticationConfiguration",
			logicalCluster: &kcpv1alpha1.LogicalCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"kcp.io/path": "root:orgs:test-workspace",
					},
				},
			},
			baseDomain: "test.domain",
			setupMocks: func(m *mocks.MockClient) {
				m.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "test-workspace"}, mock.AnythingOfType("*v1alpha1.WorkspaceAuthenticationConfiguration"), mock.Anything).
					Return(apierrors.NewNotFound(kcptenancyv1alphav1.Resource("workspaceauthenticationconfigurations"), "test-workspace")).Once()

				m.EXPECT().Create(mock.Anything, mock.AnythingOfType("*v1alpha1.WorkspaceAuthenticationConfiguration"), mock.Anything).
					RunAndReturn(func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
						wac := obj.(*kcptenancyv1alphav1.WorkspaceAuthenticationConfiguration)
						assert.Equal(t, "test-workspace", wac.Name)
						assert.Equal(t, "https://test.domain:8443/keycloak/realms/test-workspace", wac.Spec.JWT[0].Issuer.URL)
						assert.Equal(t, []string{"test-workspace"}, wac.Spec.JWT[0].Issuer.Audiences)
						assert.Equal(t, kcptenancyv1alphav1.AudienceMatchPolicyMatchAny, wac.Spec.JWT[0].Issuer.AudienceMatchPolicy)
						assert.Equal(t, "groups", wac.Spec.JWT[0].ClaimMappings.Groups.Claim)
						assert.Equal(t, "email", wac.Spec.JWT[0].ClaimMappings.Username.Claim)
						return nil
					}).Once()
			},
			expectError:    false,
			expectedResult: ctrl.Result{},
		},
		{
			name: "success - update existing WorkspaceAuthenticationConfiguration",
			logicalCluster: &kcpv1alpha1.LogicalCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"kcp.io/path": "root:orgs:existing-workspace",
					},
				},
			},
			baseDomain: "example.com",
			setupMocks: func(m *mocks.MockClient) {
				m.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "existing-workspace"}, mock.AnythingOfType("*v1alpha1.WorkspaceAuthenticationConfiguration"), mock.Anything).
					RunAndReturn(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
						wac := obj.(*kcptenancyv1alphav1.WorkspaceAuthenticationConfiguration)
						wac.Name = "existing-workspace"
						wac.Spec = kcptenancyv1alphav1.WorkspaceAuthenticationConfigurationSpec{
							JWT: []kcptenancyv1alphav1.JWTAuthenticator{
								{
									Issuer: kcptenancyv1alphav1.Issuer{
										URL: "https://old.domain:8443/keycloak/realms/existing-workspace",
									},
								},
							},
						}
						return nil
					}).Once()

				m.EXPECT().Update(mock.Anything, mock.AnythingOfType("*v1alpha1.WorkspaceAuthenticationConfiguration"), mock.Anything).
					RunAndReturn(func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
						wac := obj.(*kcptenancyv1alphav1.WorkspaceAuthenticationConfiguration)
						assert.Equal(t, "existing-workspace", wac.Name)
						assert.Equal(t, "https://example.com:8443/keycloak/realms/existing-workspace", wac.Spec.JWT[0].Issuer.URL)
						assert.Equal(t, []string{"existing-workspace"}, wac.Spec.JWT[0].Issuer.Audiences)
						return nil
					}).Once()
			},
			expectError:    false,
			expectedResult: ctrl.Result{},
		},
		{
			name: "error - missing workspace path annotation",
			logicalCluster: &kcpv1alpha1.LogicalCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			baseDomain:     "test.domain",
			setupMocks:     func(m *mocks.MockClient) {},
			expectError:    true,
			expectedResult: ctrl.Result{},
		},
		{
			name: "error - empty workspace path annotation",
			logicalCluster: &kcpv1alpha1.LogicalCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"kcp.io/path": "",
					},
				},
			},
			baseDomain:     "test.domain",
			setupMocks:     func(m *mocks.MockClient) {},
			expectError:    true,
			expectedResult: ctrl.Result{},
		},
		{
			name: "error - create fails",
			logicalCluster: &kcpv1alpha1.LogicalCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"kcp.io/path": "root:orgs:test-workspace",
					},
				},
			},
			baseDomain: "test.domain",
			setupMocks: func(m *mocks.MockClient) {
				m.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "test-workspace"}, mock.AnythingOfType("*v1alpha1.WorkspaceAuthenticationConfiguration"), mock.Anything).
					Return(apierrors.NewNotFound(kcptenancyv1alphav1.Resource("workspaceauthenticationconfigurations"), "test-workspace")).Once()

				m.EXPECT().Create(mock.Anything, mock.AnythingOfType("*v1alpha1.WorkspaceAuthenticationConfiguration"), mock.Anything).
					Return(errors.New("create failed")).Once()
			},
			expectError:    true,
			expectedResult: ctrl.Result{},
		},
		{
			name: "error - update fails",
			logicalCluster: &kcpv1alpha1.LogicalCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"kcp.io/path": "root:orgs:test-workspace",
					},
				},
			},
			baseDomain: "test.domain",
			setupMocks: func(m *mocks.MockClient) {
				m.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "test-workspace"}, mock.AnythingOfType("*v1alpha1.WorkspaceAuthenticationConfiguration"), mock.Anything).
					RunAndReturn(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
						wac := obj.(*kcptenancyv1alphav1.WorkspaceAuthenticationConfiguration)
						wac.Name = "test-workspace"
						return nil
					}).Once()

				m.EXPECT().Update(mock.Anything, mock.AnythingOfType("*v1alpha1.WorkspaceAuthenticationConfiguration"), mock.Anything).
					Return(errors.New("update failed")).Once()
			},
			expectError:    true,
			expectedResult: ctrl.Result{},
		},
		{
			name: "error - get fails with non-not-found error",
			logicalCluster: &kcpv1alpha1.LogicalCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"kcp.io/path": "root:orgs:test-workspace",
					},
				},
			},
			baseDomain: "test.domain",
			setupMocks: func(m *mocks.MockClient) {
				m.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "test-workspace"}, mock.AnythingOfType("*v1alpha1.WorkspaceAuthenticationConfiguration"), mock.Anything).
					Return(errors.New("get failed")).Once()
			},
			expectError:    true,
			expectedResult: ctrl.Result{},
		},
		{
			name: "success - workspace path with single element",
			logicalCluster: &kcpv1alpha1.LogicalCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"kcp.io/path": "single-workspace",
					},
				},
			},
			baseDomain: "test.domain",
			setupMocks: func(m *mocks.MockClient) {
				m.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "single-workspace"}, mock.AnythingOfType("*v1alpha1.WorkspaceAuthenticationConfiguration"), mock.Anything).
					Return(apierrors.NewNotFound(kcptenancyv1alphav1.Resource("workspaceauthenticationconfigurations"), "single-workspace")).Once()

				m.EXPECT().Create(mock.Anything, mock.AnythingOfType("*v1alpha1.WorkspaceAuthenticationConfiguration"), mock.Anything).
					RunAndReturn(func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
						wac := obj.(*kcptenancyv1alphav1.WorkspaceAuthenticationConfiguration)
						assert.Equal(t, "single-workspace", wac.Name)
						assert.Equal(t, "https://test.domain:8443/keycloak/realms/single-workspace", wac.Spec.JWT[0].Issuer.URL)
						return nil
					}).Once()
			},
			expectError:    false,
			expectedResult: ctrl.Result{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := mocks.NewMockClient(t)
			if tt.setupMocks != nil {
				tt.setupMocks(mockClient)
			}

			subroutine := NewWorkspaceAuthConfigurationSubroutine(mockClient, tt.baseDomain)

			result, opErr := subroutine.Process(context.Background(), tt.logicalCluster)

			if tt.expectError {
				assert.NotNil(t, opErr)
			} else {
				assert.Nil(t, opErr)
			}

			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

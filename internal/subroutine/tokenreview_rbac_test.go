package subroutine

import (
	"context"
	"testing"

	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/security-operator/internal/subroutine/mocks"
	"github.com/platform-mesh/subroutines"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"

	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

const testGatewayHomeClusterID = "gateway-home-cluster-id"

func TestTokenReviewRBACSubroutine_GetName(t *testing.T) {
	sub := NewTokenReviewRBACSubroutine(nil)
	assert.Equal(t, "TokenReviewRBAC", sub.GetName())
}

func TestCrossWorkspaceGatewayCallerGroup(t *testing.T) {
	subject := crossWorkspaceGatewayCallerGroup("abc123cluster")
	assert.Equal(t, "Group", subject.Kind)
	assert.Equal(t, "system:cluster:abc123cluster", subject.Name)
}

func TestTokenReviewRBACSubroutine_reconcile_orgWorkspace_createsRBAC(t *testing.T) {
	const orgPath = "root:orgs:org1"
	lc := orgLogicalCluster(orgPath)
	wsClient := newRBACFakeClient(t)

	kcpHelper := mocks.NewMockKCPClientGetter(t)
	expectGatewayHomeClusterLookup(t, kcpHelper)
	kcpHelper.EXPECT().NewClientForLogicalCluster(mock.Anything, orgPath).Return(wsClient, nil).Once()
	kcpHelper.EXPECT().NewClientForLogicalCluster(mock.Anything, config.OrgsClusterPath).Return(wsClient, nil).Once()

	sub := NewTokenReviewRBACSubroutine(kcpHelper)

	result, err := sub.reconcile(context.Background(), lc)
	require.NoError(t, err)
	assert.Equal(t, subroutines.OK(), result)

	assertGatewayTokenReviewRBAC(t, wsClient, testGatewayHomeClusterID)
}

func TestTokenReviewRBACSubroutine_reconcile_accountWorkspace_createsRBAC(t *testing.T) {
	const accountPath = "root:orgs:org1:account1"
	lc := orgLogicalCluster(accountPath)
	wsClient := newRBACFakeClient(t)

	kcpHelper := mocks.NewMockKCPClientGetter(t)
	expectGatewayHomeClusterLookup(t, kcpHelper)
	kcpHelper.EXPECT().NewClientForLogicalCluster(mock.Anything, accountPath).Return(wsClient, nil).Once()
	kcpHelper.EXPECT().NewClientForLogicalCluster(mock.Anything, config.OrgsClusterPath).Return(wsClient, nil).Once()

	sub := NewTokenReviewRBACSubroutine(kcpHelper)

	result, err := sub.reconcile(context.Background(), lc)
	require.NoError(t, err)
	assert.Equal(t, subroutines.OK(), result)

	assertGatewayTokenReviewRBAC(t, wsClient, testGatewayHomeClusterID)
}

func TestTokenReviewRBACSubroutine_reconcile_nestedAccountWorkspace_createsRBAC(t *testing.T) {
	const nestedAccountPath = "root:orgs:org1:account1:account2"
	lc := orgLogicalCluster(nestedAccountPath)
	wsClient := newRBACFakeClient(t)

	kcpHelper := mocks.NewMockKCPClientGetter(t)
	expectGatewayHomeClusterLookup(t, kcpHelper)
	kcpHelper.EXPECT().NewClientForLogicalCluster(mock.Anything, nestedAccountPath).Return(wsClient, nil).Once()
	kcpHelper.EXPECT().NewClientForLogicalCluster(mock.Anything, config.OrgsClusterPath).Return(wsClient, nil).Once()

	sub := NewTokenReviewRBACSubroutine(kcpHelper)

	_, err := sub.reconcile(context.Background(), lc)
	require.NoError(t, err)
	assertGatewayTokenReviewRBAC(t, wsClient, testGatewayHomeClusterID)
}

func TestTokenReviewRBACSubroutine_reconcile_atOrgsPath_skipsParentBindings(t *testing.T) {
	lc := orgLogicalCluster(config.OrgsClusterPath)
	wsClient := newRBACFakeClient(t)

	kcpHelper := mocks.NewMockKCPClientGetter(t)
	expectGatewayHomeClusterLookup(t, kcpHelper)
	kcpHelper.EXPECT().NewClientForLogicalCluster(mock.Anything, config.OrgsClusterPath).Return(wsClient, nil).Once()

	sub := NewTokenReviewRBACSubroutine(kcpHelper)

	_, err := sub.reconcile(context.Background(), lc)
	require.NoError(t, err)
	assertGatewayTokenReviewRBAC(t, wsClient, testGatewayHomeClusterID)
}

func TestTokenReviewRBACSubroutine_EnsureGatewayTokenReviewRBACInOrgsParent(t *testing.T) {
	wsClient := newRBACFakeClient(t)

	kcpHelper := mocks.NewMockKCPClientGetter(t)
	expectGatewayHomeClusterLookup(t, kcpHelper)
	kcpHelper.EXPECT().NewClientForLogicalCluster(mock.Anything, config.OrgsClusterPath).Return(wsClient, nil).Once()

	sub := NewTokenReviewRBACSubroutine(kcpHelper)
	require.NoError(t, sub.EnsureGatewayTokenReviewRBACInOrgsParent(context.Background()))
	assertGatewayTokenReviewRBAC(t, wsClient, testGatewayHomeClusterID)
}

func TestTokenReviewRBACSubroutine_reconcile_missingPath(t *testing.T) {
	lc := &kcpcorev1alpha1.LogicalCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
	sub := NewTokenReviewRBACSubroutine(nil)

	_, err := sub.reconcile(context.Background(), lc)
	require.ErrorContains(t, err, "no kcp.io/path annotation")
}

func TestClusterIDFromWorkspacePath(t *testing.T) {
	kcpHelper := mocks.NewMockKCPClientGetter(t)
	homeClient := newRBACFakeClient(t)
	require.NoError(t, homeClient.Create(context.Background(), gatewayHomeLogicalCluster(testGatewayHomeClusterID)))

	kcpHelper.EXPECT().NewClientForLogicalCluster(mock.Anything, gatewayHomeWorkspacePath).Return(homeClient, nil).Once()

	id, err := clusterIDFromWorkspacePath(context.Background(), kcpHelper, gatewayHomeWorkspacePath)
	require.NoError(t, err)
	assert.Equal(t, testGatewayHomeClusterID, id)
}

func TestClusterIDFromWorkspacePath_missingAnnotation(t *testing.T) {
	kcpHelper := mocks.NewMockKCPClientGetter(t)
	homeClient := newRBACFakeClient(t)
	require.NoError(t, homeClient.Create(context.Background(), &kcpcorev1alpha1.LogicalCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
			Annotations: map[string]string{
				"kcp.io/path": gatewayHomeWorkspacePath,
			},
		},
	}))

	kcpHelper.EXPECT().NewClientForLogicalCluster(mock.Anything, gatewayHomeWorkspacePath).Return(homeClient, nil).Once()

	_, err := clusterIDFromWorkspacePath(context.Background(), kcpHelper, gatewayHomeWorkspacePath)
	require.ErrorContains(t, err, "kcp.io/cluster annotation not found")
}

func TestGatewayHomeClusterID_retriesAfterTransientFailure(t *testing.T) {
	kcpHelper := mocks.NewMockKCPClientGetter(t)
	failClient := newRBACFakeClient(t)
	successClient := newRBACFakeClient(t)
	require.NoError(t, successClient.Create(context.Background(), gatewayHomeLogicalCluster(testGatewayHomeClusterID)))

	kcpHelper.EXPECT().NewClientForLogicalCluster(mock.Anything, gatewayHomeWorkspacePath).Return(failClient, nil).Once()
	kcpHelper.EXPECT().NewClientForLogicalCluster(mock.Anything, gatewayHomeWorkspacePath).Return(successClient, nil).Once()

	sub := NewTokenReviewRBACSubroutine(kcpHelper)

	_, err := sub.gatewayHomeClusterID(context.Background())
	require.Error(t, err)

	id, err := sub.gatewayHomeClusterID(context.Background())
	require.NoError(t, err)
	assert.Equal(t, testGatewayHomeClusterID, id)

	// Successful lookup is cached; no further API calls.
	id, err = sub.gatewayHomeClusterID(context.Background())
	require.NoError(t, err)
	assert.Equal(t, testGatewayHomeClusterID, id)
}

func orgLogicalCluster(path string) *kcpcorev1alpha1.LogicalCluster {
	return &kcpcorev1alpha1.LogicalCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
			Annotations: map[string]string{
				"kcp.io/path": path,
			},
		},
	}
}

func gatewayHomeLogicalCluster(clusterID string) *kcpcorev1alpha1.LogicalCluster {
	return &kcpcorev1alpha1.LogicalCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
			Annotations: map[string]string{
				"kcp.io/path":    gatewayHomeWorkspacePath,
				"kcp.io/cluster": clusterID,
			},
		},
	}
}

func expectGatewayHomeClusterLookup(t *testing.T, kcpHelper *mocks.MockKCPClientGetter) {
	t.Helper()
	homeClient := newRBACFakeClient(t)
	require.NoError(t, homeClient.Create(context.Background(), gatewayHomeLogicalCluster(testGatewayHomeClusterID)))
	kcpHelper.EXPECT().NewClientForLogicalCluster(mock.Anything, gatewayHomeWorkspacePath).Return(homeClient, nil).Once()
}

func newRBACFakeClient(t *testing.T) client.Client {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, kcpcorev1alpha1.AddToScheme(s))
	require.NoError(t, rbacv1.AddToScheme(s))
	return fake.NewClientBuilder().WithScheme(s).Build()
}

func assertGatewayTokenReviewRBAC(t *testing.T, wsClient client.Client, gatewayHomeClusterID string) {
	t.Helper()
	ctx := context.Background()
	expectedGroup := "system:cluster:" + gatewayHomeClusterID

	var clusterRole rbacv1.ClusterRole
	require.NoError(t, wsClient.Get(ctx, types.NamespacedName{Name: gatewayTokenReviewClusterRoleName}, &clusterRole))
	require.Len(t, clusterRole.Rules, 1)
	assert.Equal(t, "tokenreviews", clusterRole.Rules[0].Resources[0])

	var tokenReviewBinding rbacv1.ClusterRoleBinding
	require.NoError(t, wsClient.Get(ctx, types.NamespacedName{Name: gatewayTokenReviewClusterRoleBindingName}, &tokenReviewBinding))
	require.Len(t, tokenReviewBinding.Subjects, 1)
	assert.Equal(t, expectedGroup, tokenReviewBinding.Subjects[0].Name)

	var workspaceAccessBinding rbacv1.ClusterRoleBinding
	require.NoError(t, wsClient.Get(ctx, types.NamespacedName{Name: gatewayTokenReviewWorkspaceAccessBindingName}, &workspaceAccessBinding))
	assert.Equal(t, gatewayTokenReviewWorkspaceAccessClusterRole, workspaceAccessBinding.RoleRef.Name)
	assert.Equal(t, expectedGroup, workspaceAccessBinding.Subjects[0].Name)
}

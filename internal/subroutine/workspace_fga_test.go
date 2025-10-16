package subroutine_test

import (
	"context"
	"testing"

	kcpcorev1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"
	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	accountsv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/subroutine"
	"github.com/platform-mesh/security-operator/internal/subroutine/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
)

func TestWorkspaceFGA_Requeue_WhenAccountInfoMissing(t *testing.T) {
	mgr := mocks.NewMockManager(t)
	wsCluster := mocks.NewMockCluster(t)
	wsClient := mocks.NewMockClient(t)

	wsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError)
	wsCluster.EXPECT().GetClient().Return(wsClient)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(wsCluster, nil)

	fga := mocks.NewMockOpenFGAServiceClient(t)
	sub := subroutine.NewWorkspaceFGASubroutine(nil, mgr, fga, "core_platform-mesh_io_account", "parent", "owner")

	lc := &kcpcorev1alpha1.LogicalCluster{}
	ctx := mccontext.WithCluster(context.Background(), "ws")
	res, opErr := sub.Process(ctx, lc)
	assert.Nil(t, opErr)
	assert.True(t, res.Requeue)
}

func TestWorkspaceFGA_Requeue_WhenAccountInfoIncomplete(t *testing.T) {
	mgr := mocks.NewMockManager(t)
	wsCluster := mocks.NewMockCluster(t)
	wsClient := mocks.NewMockClient(t)

	wsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ai := obj.(*accountsv1alpha1.AccountInfo)
			ai.Spec.Account.Name = "" // missing
			return nil
		},
	)
	wsCluster.EXPECT().GetClient().Return(wsClient)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(wsCluster, nil)

	fga := mocks.NewMockOpenFGAServiceClient(t)
	sub := subroutine.NewWorkspaceFGASubroutine(nil, mgr, fga, "core_platform-mesh_io_account", "parent", "owner")

	lc := &kcpcorev1alpha1.LogicalCluster{}
	ctx := mccontext.WithCluster(context.Background(), "ws")
	res, opErr := sub.Process(ctx, lc)
	assert.Nil(t, opErr)
	assert.True(t, res.Requeue)
}

func TestWorkspaceFGA_WritesParentAndOwnerTuples(t *testing.T) {
	mgr := mocks.NewMockManager(t)

	wsCluster := mocks.NewMockCluster(t)
	wsClient := mocks.NewMockClient(t)
	wsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ai := obj.(*accountsv1alpha1.AccountInfo)
			ai.Spec.Account.Name = "acc"
			ai.Spec.Account.OriginClusterId = "root:orgs"
			ai.Spec.FGA.Store.Id = "store-1"
			ai.Spec.ParentAccount = &accountsv1alpha1.AccountLocation{Name: "org", OriginClusterId: "root:orgs"}
			return nil
		},
	)
	wsCluster.EXPECT().GetClient().Return(wsClient)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(wsCluster, nil)

	ownerCluster := mocks.NewMockCluster(t)
	ownerClient := mocks.NewMockClient(t)
	ownerClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			a := obj.(*accountsv1alpha1.Account)
			creator := "user@example.com"
			a.Spec.Creator = &creator
			return nil
		},
	)
	ownerCluster.EXPECT().GetClient().Return(ownerClient)
	mgr.EXPECT().GetCluster(mock.Anything, mock.Anything).Return(ownerCluster, nil)

	fga := mocks.NewMockOpenFGAServiceClient(t)
	fga.EXPECT().Write(mock.Anything, mock.Anything).Return(&openfgav1.WriteResponse{}, nil).Times(3)

	sub := subroutine.NewWorkspaceFGASubroutine(nil, mgr, fga, "core_platform-mesh_io_account", "parent", "owner")

	lc := &kcpcorev1alpha1.LogicalCluster{}
	lc.Spec.Owner = &kcpcorev1alpha1.LogicalClusterOwner{}
	lc.Spec.Owner.Cluster = logicalcluster.Name("ws-owner").String()
	lc.Spec.Owner.Name = "acc"
	ctx := mccontext.WithCluster(context.Background(), "ws")
	res, opErr := sub.Process(ctx, lc)
	assert.Nil(t, opErr)
	assert.False(t, res.Requeue)
}

func TestWorkspaceFGA_InvalidCreator_ReturnsError(t *testing.T) {
	mgr := mocks.NewMockManager(t)
	wsCluster := mocks.NewMockCluster(t)
	wsClient := mocks.NewMockClient(t)
	wsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ai := obj.(*accountsv1alpha1.AccountInfo)
			ai.Spec.Account.Name = "acc"
			ai.Spec.Account.OriginClusterId = "root:orgs"
			ai.Spec.FGA.Store.Id = "store-1"
			return nil
		},
	)
	wsCluster.EXPECT().GetClient().Return(wsClient)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(wsCluster, nil)

	ownerCluster := mocks.NewMockCluster(t)
	ownerClient := mocks.NewMockClient(t)
	ownerClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			a := obj.(*accountsv1alpha1.Account)
			creator := "system:serviceaccount:ns:name"
			a.Spec.Creator = &creator
			return nil
		},
	)
	ownerCluster.EXPECT().GetClient().Return(ownerClient)
	mgr.EXPECT().GetCluster(mock.Anything, mock.Anything).Return(ownerCluster, nil)

	fga := mocks.NewMockOpenFGAServiceClient(t)
	sub := subroutine.NewWorkspaceFGASubroutine(nil, mgr, fga, "core_platform-mesh_io_account", "parent", "owner")

	lc := &kcpcorev1alpha1.LogicalCluster{}
	lc.Spec.Owner = &kcpcorev1alpha1.LogicalClusterOwner{}
	lc.Spec.Owner.Cluster = logicalcluster.Name("ws-owner").String()
	lc.Spec.Owner.Name = "acc"
	ctx := mccontext.WithCluster(context.Background(), "ws")
	_, opErr := sub.Process(ctx, lc)
	assert.NotNil(t, opErr)
}

func TestWorkspaceFGA_OwnerAccountGetError_Requeues(t *testing.T) {
	mgr := mocks.NewMockManager(t)
	wsCluster := mocks.NewMockCluster(t)
	wsClient := mocks.NewMockClient(t)
	wsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ai := obj.(*accountsv1alpha1.AccountInfo)
			ai.Spec.Account.Name = "acc"
			ai.Spec.Account.OriginClusterId = "root:orgs"
			ai.Spec.FGA.Store.Id = "store-1"
			return nil
		},
	)
	wsCluster.EXPECT().GetClient().Return(wsClient)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(wsCluster, nil)

	ownerCluster := mocks.NewMockCluster(t)
	ownerClient := mocks.NewMockClient(t)
	ownerClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError)
	ownerCluster.EXPECT().GetClient().Return(ownerClient)
	mgr.EXPECT().GetCluster(mock.Anything, mock.Anything).Return(ownerCluster, nil)

	fga := mocks.NewMockOpenFGAServiceClient(t)
	sub := subroutine.NewWorkspaceFGASubroutine(nil, mgr, fga, "core_platform-mesh_io_account", "parent", "owner")

	lc := &kcpcorev1alpha1.LogicalCluster{}
	lc.Spec.Owner = &kcpcorev1alpha1.LogicalClusterOwner{}
	lc.Spec.Owner.Cluster = logicalcluster.Name("ws-owner").String()
	lc.Spec.Owner.Name = "acc"
	ctx := mccontext.WithCluster(context.Background(), "ws")
	res, opErr := sub.Process(ctx, lc)
	assert.Nil(t, opErr)
	assert.True(t, res.Requeue)
}

func TestWorkspaceFGA_GetClusterError_ReturnsOperatorError(t *testing.T) {
	mgr := mocks.NewMockManager(t)
	wsCluster := mocks.NewMockCluster(t)
	wsClient := mocks.NewMockClient(t)
	wsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ai := obj.(*accountsv1alpha1.AccountInfo)
			ai.Spec.Account.Name = "acc"
			ai.Spec.Account.OriginClusterId = "root:orgs"
			ai.Spec.FGA.Store.Id = "store-1"
			return nil
		},
	)
	wsCluster.EXPECT().GetClient().Return(wsClient)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(wsCluster, nil)

	mgr.EXPECT().GetCluster(mock.Anything, mock.Anything).Return(nil, assert.AnError)

	fga := mocks.NewMockOpenFGAServiceClient(t)
	sub := subroutine.NewWorkspaceFGASubroutine(nil, mgr, fga, "core_platform-mesh_io_account", "parent", "owner")

	lc := &kcpcorev1alpha1.LogicalCluster{}
	lc.Spec.Owner = &kcpcorev1alpha1.LogicalClusterOwner{}
	lc.Spec.Owner.Cluster = logicalcluster.Name("ws-owner").String()
	lc.Spec.Owner.Name = "acc"
	ctx := mccontext.WithCluster(context.Background(), "ws")
	_, opErr := sub.Process(ctx, lc)
	assert.NotNil(t, opErr)
}

func TestWorkspaceFGA_OnlyParentTuple_WhenNoCreator(t *testing.T) {
	mgr := mocks.NewMockManager(t)
	wsCluster := mocks.NewMockCluster(t)
	wsClient := mocks.NewMockClient(t)
	wsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ai := obj.(*accountsv1alpha1.AccountInfo)
			ai.Spec.Account.Name = "acc"
			ai.Spec.Account.OriginClusterId = "root:orgs"
			ai.Spec.FGA.Store.Id = "store-1"
			ai.Spec.ParentAccount = &accountsv1alpha1.AccountLocation{Name: "org", OriginClusterId: "root:orgs"}
			return nil
		},
	)
	wsCluster.EXPECT().GetClient().Return(wsClient)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(wsCluster, nil)

	ownerCluster := mocks.NewMockCluster(t)
	ownerClient := mocks.NewMockClient(t)
	ownerClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			a := obj.(*accountsv1alpha1.Account)
			a.Spec.Creator = nil // no creator set
			return nil
		},
	)
	ownerCluster.EXPECT().GetClient().Return(ownerClient)
	mgr.EXPECT().GetCluster(mock.Anything, mock.Anything).Return(ownerCluster, nil)

	fga := mocks.NewMockOpenFGAServiceClient(t)
	fga.EXPECT().Write(mock.Anything, mock.Anything).Return(&openfgav1.WriteResponse{}, nil).Once()

	sub := subroutine.NewWorkspaceFGASubroutine(nil, mgr, fga, "core_platform-mesh_io_account", "parent", "owner")

	lc := &kcpcorev1alpha1.LogicalCluster{}
	lc.Spec.Owner = &kcpcorev1alpha1.LogicalClusterOwner{}
	lc.Spec.Owner.Cluster = logicalcluster.Name("ws-owner").String()
	lc.Spec.Owner.Name = "acc"
	ctx := mccontext.WithCluster(context.Background(), "ws")
	res, opErr := sub.Process(ctx, lc)
	assert.Nil(t, opErr)
	assert.False(t, res.Requeue)
}

func TestWorkspaceFGA_WriteTupleError_Propagates(t *testing.T) {
	mgr := mocks.NewMockManager(t)
	wsCluster := mocks.NewMockCluster(t)
	wsClient := mocks.NewMockClient(t)
	wsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ai := obj.(*accountsv1alpha1.AccountInfo)
			ai.Spec.Account.Name = "acc"
			ai.Spec.Account.OriginClusterId = "root:orgs"
			ai.Spec.FGA.Store.Id = "store-1"
			ai.Spec.ParentAccount = &accountsv1alpha1.AccountLocation{Name: "org", OriginClusterId: "root:orgs"}
			return nil
		},
	)
	wsCluster.EXPECT().GetClient().Return(wsClient)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(wsCluster, nil)

	fga := mocks.NewMockOpenFGAServiceClient(t)
	fga.EXPECT().Write(mock.Anything, mock.Anything).Return(nil, assert.AnError).Once()

	sub := subroutine.NewWorkspaceFGASubroutine(nil, mgr, fga, "core_platform-mesh_io_account", "parent", "owner")

	lc := &kcpcorev1alpha1.LogicalCluster{}
	lc.Spec.Owner = &kcpcorev1alpha1.LogicalClusterOwner{}
	lc.Spec.Owner.Cluster = logicalcluster.Name("ws-owner").String()
	lc.Spec.Owner.Name = "acc"
	ctx := mccontext.WithCluster(context.Background(), "ws")
	_, opErr := sub.Process(ctx, lc)
	assert.NotNil(t, opErr)
}

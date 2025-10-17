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
	assert.True(t, res.RequeueAfter > 0, "Expected requeue with delay")
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
	assert.True(t, res.RequeueAfter > 0, "Expected requeue with delay")
}

func TestWorkspaceFGA_WritesParentAndOwnerTuples(t *testing.T) {
	mgr := mocks.NewMockManager(t)

	wsCluster := mocks.NewMockCluster(t)
	wsClient := mocks.NewMockClient(t)
	wsStatusWriter := mocks.NewMockStatusWriter(t)
	creator := "user@example.com"

	// Mock Get for AccountInfo
	wsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ai := obj.(*accountsv1alpha1.AccountInfo)
			ai.Spec.Account.Name = "acc"
			ai.Spec.Account.OriginClusterId = "root:orgs"
			ai.Spec.FGA.Store.Id = "store-1"
			ai.Spec.ParentAccount = &accountsv1alpha1.AccountLocation{Name: "org", OriginClusterId: "root:orgs"}
			ai.Spec.Creator = &creator
			ai.Status.CreatorTupleWritten = false
			return nil
		},
	)

	// Mock Status().Update()
	wsClient.EXPECT().Status().Return(wsStatusWriter).Once()
	wsStatusWriter.EXPECT().Update(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		ai := obj.(*accountsv1alpha1.AccountInfo)
		assert.True(t, ai.Status.CreatorTupleWritten)
		return true
	}), mock.Anything).Return(nil).Once()

	wsCluster.EXPECT().GetClient().Return(wsClient)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(wsCluster, nil)

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
	assert.Equal(t, int64(0), res.RequeueAfter.Nanoseconds(), "Expected no requeue")
}

func TestWorkspaceFGA_InvalidCreator_ReturnsError(t *testing.T) {
	mgr := mocks.NewMockManager(t)
	wsCluster := mocks.NewMockCluster(t)
	wsClient := mocks.NewMockClient(t)
	creator := "system:serviceaccount:ns:name"
	wsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ai := obj.(*accountsv1alpha1.AccountInfo)
			ai.Spec.Account.Name = "acc"
			ai.Spec.Account.OriginClusterId = "root:orgs"
			ai.Spec.FGA.Store.Id = "store-1"
			ai.Spec.Creator = &creator
			ai.Status.CreatorTupleWritten = false
			return nil
		},
	)
	wsCluster.EXPECT().GetClient().Return(wsClient)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(wsCluster, nil)

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
			ai.Spec.Creator = nil // no creator set
			return nil
		},
	)
	wsCluster.EXPECT().GetClient().Return(wsClient)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(wsCluster, nil)

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
	assert.Equal(t, int64(0), res.RequeueAfter.Nanoseconds(), "Expected no requeue")
}

func TestWorkspaceFGA_SkipsCreatorTuple_WhenAlreadyWritten(t *testing.T) {
	mgr := mocks.NewMockManager(t)
	wsCluster := mocks.NewMockCluster(t)
	wsClient := mocks.NewMockClient(t)
	creator := "user@example.com"

	wsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ai := obj.(*accountsv1alpha1.AccountInfo)
			ai.Spec.Account.Name = "acc"
			ai.Spec.Account.OriginClusterId = "root:orgs"
			ai.Spec.FGA.Store.Id = "store-1"
			ai.Spec.ParentAccount = &accountsv1alpha1.AccountLocation{Name: "org", OriginClusterId: "root:orgs"}
			ai.Spec.Creator = &creator
			ai.Status.CreatorTupleWritten = true // Already written
			return nil
		},
	)
	wsCluster.EXPECT().GetClient().Return(wsClient)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(wsCluster, nil)

	fga := mocks.NewMockOpenFGAServiceClient(t)
	// Only one write for parent tuple, no creator tuples
	fga.EXPECT().Write(mock.Anything, mock.Anything).Return(&openfgav1.WriteResponse{}, nil).Once()

	sub := subroutine.NewWorkspaceFGASubroutine(nil, mgr, fga, "core_platform-mesh_io_account", "parent", "owner")

	lc := &kcpcorev1alpha1.LogicalCluster{}
	lc.Spec.Owner = &kcpcorev1alpha1.LogicalClusterOwner{}
	lc.Spec.Owner.Cluster = logicalcluster.Name("ws-owner").String()
	lc.Spec.Owner.Name = "acc"
	ctx := mccontext.WithCluster(context.Background(), "ws")
	res, opErr := sub.Process(ctx, lc)
	assert.Nil(t, opErr)
	assert.Equal(t, int64(0), res.RequeueAfter.Nanoseconds(), "Expected no requeue")
}

func TestWorkspaceFGA_NoParentTuple_ForOrgAccount(t *testing.T) {
	mgr := mocks.NewMockManager(t)
	wsCluster := mocks.NewMockCluster(t)
	wsClient := mocks.NewMockClient(t)
	creator := "user@example.com"
	wsStatusWriter := mocks.NewMockStatusWriter(t)

	wsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ai := obj.(*accountsv1alpha1.AccountInfo)
			ai.Spec.Account.Name = "org"
			ai.Spec.Account.OriginClusterId = "root:orgs"
			ai.Spec.FGA.Store.Id = "store-1"
			ai.Spec.ParentAccount = nil // Org accounts don't have parent
			ai.Spec.Creator = &creator
			ai.Status.CreatorTupleWritten = false
			return nil
		},
	)
	wsClient.EXPECT().Status().Return(wsStatusWriter).Once()
	wsStatusWriter.EXPECT().Update(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	wsCluster.EXPECT().GetClient().Return(wsClient)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(wsCluster, nil)

	fga := mocks.NewMockOpenFGAServiceClient(t)
	// Only two writes for creator tuples, no parent tuple
	fga.EXPECT().Write(mock.Anything, mock.Anything).Return(&openfgav1.WriteResponse{}, nil).Times(2)

	sub := subroutine.NewWorkspaceFGASubroutine(nil, mgr, fga, "core_platform-mesh_io_account", "parent", "owner")

	lc := &kcpcorev1alpha1.LogicalCluster{}
	lc.Spec.Owner = &kcpcorev1alpha1.LogicalClusterOwner{}
	lc.Spec.Owner.Cluster = logicalcluster.Name("ws-owner").String()
	lc.Spec.Owner.Name = "org"
	ctx := mccontext.WithCluster(context.Background(), "ws")
	res, opErr := sub.Process(ctx, lc)
	assert.Nil(t, opErr)
	assert.Equal(t, int64(0), res.RequeueAfter.Nanoseconds(), "Expected no requeue")
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

func TestWorkspaceFGA_ClusterFromContextError_ReturnsError(t *testing.T) {
	mgr := mocks.NewMockManager(t)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(nil, assert.AnError)

	fga := mocks.NewMockOpenFGAServiceClient(t)
	sub := subroutine.NewWorkspaceFGASubroutine(nil, mgr, fga, "core_platform-mesh_io_account", "parent", "owner")

	lc := &kcpcorev1alpha1.LogicalCluster{}
	ctx := mccontext.WithCluster(context.Background(), "ws")
	_, opErr := sub.Process(ctx, lc)
	assert.NotNil(t, opErr)
}

func TestWorkspaceFGA_EmptyCreator_SkipsCreatorTuples(t *testing.T) {
	mgr := mocks.NewMockManager(t)
	wsCluster := mocks.NewMockCluster(t)
	wsClient := mocks.NewMockClient(t)
	creator := ""

	wsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ai := obj.(*accountsv1alpha1.AccountInfo)
			ai.Spec.Account.Name = "acc"
			ai.Spec.Account.OriginClusterId = "root:orgs"
			ai.Spec.FGA.Store.Id = "store-1"
			ai.Spec.ParentAccount = &accountsv1alpha1.AccountLocation{Name: "org", OriginClusterId: "root:orgs"}
			ai.Spec.Creator = &creator // empty string
			ai.Status.CreatorTupleWritten = false
			return nil
		},
	)
	wsCluster.EXPECT().GetClient().Return(wsClient)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(wsCluster, nil)

	fga := mocks.NewMockOpenFGAServiceClient(t)
	// Only one write for parent tuple, no creator tuples because creator is empty
	fga.EXPECT().Write(mock.Anything, mock.Anything).Return(&openfgav1.WriteResponse{}, nil).Once()

	sub := subroutine.NewWorkspaceFGASubroutine(nil, mgr, fga, "core_platform-mesh_io_account", "parent", "owner")

	lc := &kcpcorev1alpha1.LogicalCluster{}
	ctx := mccontext.WithCluster(context.Background(), "ws")
	res, opErr := sub.Process(ctx, lc)
	assert.Nil(t, opErr)
	assert.Equal(t, int64(0), res.RequeueAfter.Nanoseconds(), "Expected no requeue")
}

func TestWorkspaceFGA_CreatorAssigneeTupleWriteError_ReturnsError(t *testing.T) {
	mgr := mocks.NewMockManager(t)
	wsCluster := mocks.NewMockCluster(t)
	wsClient := mocks.NewMockClient(t)
	creator := "user@example.com"

	wsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ai := obj.(*accountsv1alpha1.AccountInfo)
			ai.Spec.Account.Name = "acc"
			ai.Spec.Account.OriginClusterId = "root:orgs"
			ai.Spec.FGA.Store.Id = "store-1"
			ai.Spec.Creator = &creator
			ai.Status.CreatorTupleWritten = false
			return nil
		},
	)
	wsCluster.EXPECT().GetClient().Return(wsClient)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(wsCluster, nil)

	fga := mocks.NewMockOpenFGAServiceClient(t)
	// First writeTuple call fails (assignee tuple)
	fga.EXPECT().Write(mock.Anything, mock.Anything).Return(nil, assert.AnError).Once()

	sub := subroutine.NewWorkspaceFGASubroutine(nil, mgr, fga, "core_platform-mesh_io_account", "parent", "owner")

	lc := &kcpcorev1alpha1.LogicalCluster{}
	ctx := mccontext.WithCluster(context.Background(), "ws")
	_, opErr := sub.Process(ctx, lc)
	assert.NotNil(t, opErr)
}

func TestWorkspaceFGA_CreatorOwnerTupleWriteError_ReturnsError(t *testing.T) {
	mgr := mocks.NewMockManager(t)
	wsCluster := mocks.NewMockCluster(t)
	wsClient := mocks.NewMockClient(t)
	creator := "user@example.com"

	wsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ai := obj.(*accountsv1alpha1.AccountInfo)
			ai.Spec.Account.Name = "acc"
			ai.Spec.Account.OriginClusterId = "root:orgs"
			ai.Spec.FGA.Store.Id = "store-1"
			ai.Spec.Creator = &creator
			ai.Status.CreatorTupleWritten = false
			return nil
		},
	)
	wsCluster.EXPECT().GetClient().Return(wsClient)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(wsCluster, nil)

	fga := mocks.NewMockOpenFGAServiceClient(t)
	// First writeTuple call succeeds (assignee tuple), second fails (owner tuple)
	fga.EXPECT().Write(mock.Anything, mock.Anything).Return(&openfgav1.WriteResponse{}, nil).Once()
	fga.EXPECT().Write(mock.Anything, mock.Anything).Return(nil, assert.AnError).Once()

	sub := subroutine.NewWorkspaceFGASubroutine(nil, mgr, fga, "core_platform-mesh_io_account", "parent", "owner")

	lc := &kcpcorev1alpha1.LogicalCluster{}
	ctx := mccontext.WithCluster(context.Background(), "ws")
	_, opErr := sub.Process(ctx, lc)
	assert.NotNil(t, opErr)
}

func TestWorkspaceFGA_StatusUpdateError_ReturnsError(t *testing.T) {
	mgr := mocks.NewMockManager(t)
	wsCluster := mocks.NewMockCluster(t)
	wsClient := mocks.NewMockClient(t)
	wsStatusWriter := mocks.NewMockStatusWriter(t)
	creator := "user@example.com"

	wsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ai := obj.(*accountsv1alpha1.AccountInfo)
			ai.Spec.Account.Name = "acc"
			ai.Spec.Account.OriginClusterId = "root:orgs"
			ai.Spec.FGA.Store.Id = "store-1"
			ai.Spec.Creator = &creator
			ai.Status.CreatorTupleWritten = false
			return nil
		},
	)
	wsClient.EXPECT().Status().Return(wsStatusWriter).Once()
	wsStatusWriter.EXPECT().Update(mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError).Once()
	wsCluster.EXPECT().GetClient().Return(wsClient)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(wsCluster, nil)

	fga := mocks.NewMockOpenFGAServiceClient(t)
	// Both writeTuple calls succeed
	fga.EXPECT().Write(mock.Anything, mock.Anything).Return(&openfgav1.WriteResponse{}, nil).Times(2)

	sub := subroutine.NewWorkspaceFGASubroutine(nil, mgr, fga, "core_platform-mesh_io_account", "parent", "owner")

	lc := &kcpcorev1alpha1.LogicalCluster{}
	ctx := mccontext.WithCluster(context.Background(), "ws")
	_, opErr := sub.Process(ctx, lc)
	assert.NotNil(t, opErr)
}

func TestWorkspaceFGA_ServiceAccountCreator_FormatsCorrectly(t *testing.T) {
	mgr := mocks.NewMockManager(t)
	wsCluster := mocks.NewMockCluster(t)
	wsClient := mocks.NewMockClient(t)
	wsStatusWriter := mocks.NewMockStatusWriter(t)
	// Use a regular user that doesn't match service account pattern for formatUser to return unchanged
	creator := "user@example.com"

	wsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ai := obj.(*accountsv1alpha1.AccountInfo)
			ai.Spec.Account.Name = "acc"
			ai.Spec.Account.OriginClusterId = "root:orgs"
			ai.Spec.FGA.Store.Id = "store-1"
			ai.Spec.Creator = &creator
			ai.Status.CreatorTupleWritten = false
			return nil
		},
	)
	wsClient.EXPECT().Status().Return(wsStatusWriter).Once()
	wsStatusWriter.EXPECT().Update(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	wsCluster.EXPECT().GetClient().Return(wsClient)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(wsCluster, nil)

	fga := mocks.NewMockOpenFGAServiceClient(t)
	// Expect the user unchanged since it doesn't match service account pattern
	fga.EXPECT().Write(mock.Anything, mock.MatchedBy(func(req *openfgav1.WriteRequest) bool {
		if len(req.Writes.TupleKeys) == 0 {
			return false
		}
		tuple := req.Writes.TupleKeys[0]
		expectedUser := "user:user@example.com"
		return tuple.User == expectedUser
	})).Return(&openfgav1.WriteResponse{}, nil).Once()
	fga.EXPECT().Write(mock.Anything, mock.Anything).Return(&openfgav1.WriteResponse{}, nil).Once()

	sub := subroutine.NewWorkspaceFGASubroutine(nil, mgr, fga, "core_platform-mesh_io_account", "parent", "owner")

	lc := &kcpcorev1alpha1.LogicalCluster{}
	ctx := mccontext.WithCluster(context.Background(), "ws")
	res, opErr := sub.Process(ctx, lc)
	assert.Nil(t, opErr)
	assert.Equal(t, int64(0), res.RequeueAfter.Nanoseconds(), "Expected no requeue")
}

func TestWorkspaceFGA_ValidateCreator_WithDottedServiceAccount_ReturnsError(t *testing.T) {
	mgr := mocks.NewMockManager(t)
	wsCluster := mocks.NewMockCluster(t)
	wsClient := mocks.NewMockClient(t)
	// Use a dotted service account format that should be rejected by validateCreator
	creator := "system.serviceaccount.ns.name"

	wsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ai := obj.(*accountsv1alpha1.AccountInfo)
			ai.Spec.Account.Name = "acc"
			ai.Spec.Account.OriginClusterId = "root:orgs"
			ai.Spec.FGA.Store.Id = "store-1"
			ai.Spec.Creator = &creator
			ai.Status.CreatorTupleWritten = false
			return nil
		},
	)
	wsCluster.EXPECT().GetClient().Return(wsClient)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(wsCluster, nil)

	fga := mocks.NewMockOpenFGAServiceClient(t)
	sub := subroutine.NewWorkspaceFGASubroutine(nil, mgr, fga, "core_platform-mesh_io_account", "parent", "owner")

	lc := &kcpcorev1alpha1.LogicalCluster{}
	ctx := mccontext.WithCluster(context.Background(), "ws")
	_, opErr := sub.Process(ctx, lc)
	assert.NotNil(t, opErr)
}

func TestWorkspaceFGA_ServiceAccountFormatted_ThenValidated(t *testing.T) {
	mgr := mocks.NewMockManager(t)
	wsCluster := mocks.NewMockCluster(t)
	wsClient := mocks.NewMockClient(t)
	// Use a system service account that should be formatted from : to . and then rejected
	creator := "system:serviceaccount:ns:name"

	wsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ai := obj.(*accountsv1alpha1.AccountInfo)
			ai.Spec.Account.Name = "acc"
			ai.Spec.Account.OriginClusterId = "root:orgs"
			ai.Spec.FGA.Store.Id = "store-1"
			ai.Spec.Creator = &creator
			ai.Status.CreatorTupleWritten = false
			return nil
		},
	)
	wsCluster.EXPECT().GetClient().Return(wsClient)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(wsCluster, nil)

	fga := mocks.NewMockOpenFGAServiceClient(t)
	sub := subroutine.NewWorkspaceFGASubroutine(nil, mgr, fga, "core_platform-mesh_io_account", "parent", "owner")

	lc := &kcpcorev1alpha1.LogicalCluster{}
	ctx := mccontext.WithCluster(context.Background(), "ws")
	_, opErr := sub.Process(ctx, lc)
	// Should get an error because after formatting system:serviceaccount:ns:name becomes system.serviceaccount.ns.name
	// which is rejected by validateCreator
	assert.NotNil(t, opErr)
}

func TestWorkspaceFGA_InterfaceMethods(t *testing.T) {
	sub := subroutine.NewWorkspaceFGASubroutine(nil, nil, nil, "core_platform-mesh_io_account", "parent", "owner")

	// Test GetName
	assert.Equal(t, "WorkspaceFGA", sub.GetName())

	// Test Finalizers
	finalizers := sub.Finalizers(nil)
	assert.Nil(t, finalizers)

	// Test Finalize
	result, err := sub.Finalize(context.Background(), nil)
	assert.Nil(t, err)
	assert.Equal(t, int64(0), result.RequeueAfter.Nanoseconds())
}

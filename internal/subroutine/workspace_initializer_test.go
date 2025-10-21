package subroutine_test

import (
	"context"
	"os"
	"testing"

	kcpcorev1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/security-operator/internal/subroutine"
	"github.com/platform-mesh/security-operator/internal/subroutine/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
)

func TestWorkspaceInitializer_ErrorWhenOwnerClusterEmpty(t *testing.T) {
	// Create temp file for core module
	tmpFile, err := os.CreateTemp("", "coreModule*.fga")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString("model\n  schema 1.1")
	tmpFile.Close()

	mgr := mocks.NewMockManager(t)
	orgsClient := mocks.NewMockClient(t)
	fga := mocks.NewMockOpenFGAServiceClient(t)
	wsCluster := mocks.NewMockCluster(t)
	wsClient := mocks.NewMockClient(t)

	cfg := config.Config{
		CoreModulePath: tmpFile.Name(),
	}
	cfg.FGA.ObjectType = "core_platform-mesh_io_account"
	cfg.FGA.ParentRelation = "parent"
	cfg.FGA.CreatorRelation = "owner"

	wsCluster.EXPECT().GetClient().Return(wsClient)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(wsCluster, nil)

	sub := subroutine.NewWorkspaceInitializer(orgsClient, cfg, mgr, fga)

	lc := &kcpcorev1alpha1.LogicalCluster{}
	lc.Name = "test-workspace"
	lc.Spec.Owner.Cluster = "" // Empty cluster

	ctx := mccontext.WithCluster(context.Background(), "ws")
	_, opErr := sub.Process(ctx, lc)

	assert.NotNil(t, opErr, "Expected error when owner.cluster is empty")
}

func TestWorkspaceInitializer_ErrorWhenOwnerClusterNotFound(t *testing.T) {
	// Create temp file for core module
	tmpFile, err := os.CreateTemp("", "coreModule*.fga")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString("model\n  schema 1.1")
	tmpFile.Close()

	mgr := mocks.NewMockManager(t)
	orgsClient := mocks.NewMockClient(t)
	fga := mocks.NewMockOpenFGAServiceClient(t)
	wsCluster := mocks.NewMockCluster(t)
	wsClient := mocks.NewMockClient(t)

	cfg := config.Config{
		CoreModulePath: tmpFile.Name(),
	}
	cfg.FGA.ObjectType = "core_platform-mesh_io_account"
	cfg.FGA.ParentRelation = "parent"
	cfg.FGA.CreatorRelation = "owner"

	wsCluster.EXPECT().GetClient().Return(wsClient)
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(wsCluster, nil)
	mgr.EXPECT().GetCluster(mock.Anything, "root:orgs").Return(nil, assert.AnError)

	sub := subroutine.NewWorkspaceInitializer(orgsClient, cfg, mgr, fga)

	lc := &kcpcorev1alpha1.LogicalCluster{}
	lc.Name = "test-workspace"
	lc.Spec.Owner.Cluster = "root:orgs"
	lc.Spec.Owner.Name = "org1"

	ctx := mccontext.WithCluster(context.Background(), "ws")
	_, opErr := sub.Process(ctx, lc)

	assert.NotNil(t, opErr, "Expected error when owner cluster not found")
}

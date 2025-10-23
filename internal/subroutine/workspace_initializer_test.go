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
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
)

func TestWorkspaceInitializer_ErrorWhenOwnerClusterEmpty(t *testing.T) {
	// Create temp file for core module
	tmpFile, err := os.CreateTemp("", "coreModule*.fga")
	assert.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()
	_, err = tmpFile.WriteString("model\n  schema 1.1")
	assert.NoError(t, err)
	err = tmpFile.Close()
	assert.NoError(t, err)

	mgr := mocks.NewMockManager(t)
	orgsClient := mocks.NewMockClient(t)
	fga := mocks.NewMockOpenFGAServiceClient(t)

	cfg := config.Config{
		CoreModulePath: tmpFile.Name(),
	}
	cfg.FGA.ObjectType = "core_platform-mesh_io_account"
	cfg.FGA.ParentRelation = "parent"
	cfg.FGA.CreatorRelation = "owner"

	sub := subroutine.NewWorkspaceInitializer(orgsClient, cfg, mgr, fga)

	lc := &kcpcorev1alpha1.LogicalCluster{}
	lc.Name = "test-workspace"
	lc.Spec.Owner = &kcpcorev1alpha1.LogicalClusterOwner{
		Cluster: "", // Empty cluster
	}

	ctx := mccontext.WithCluster(context.Background(), "ws")
	_, opErr := sub.Process(ctx, lc)

	assert.NotNil(t, opErr, "Expected error when owner.cluster is empty")
}

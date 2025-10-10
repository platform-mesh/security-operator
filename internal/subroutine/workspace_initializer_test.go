package subroutine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	kcpv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"
	accountsv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/config"
)

func setupScheme(t *testing.T) *runtime.Scheme {
	scheme := runtime.NewScheme()
	require.NoError(t, kcpv1alpha1.AddToScheme(scheme))
	require.NoError(t, accountsv1alpha1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	return scheme
}

func newTestInitializer(t *testing.T, scheme *runtime.Scheme, cl client.Client, orgsClient client.Client, cfg config.Config, wsClient client.Client) *workspaceInitializer {
	restCfg := &rest.Config{Host: "https://example/services/initializingworkspaces/root:security"}
	initializer := NewWorkspaceInitializer(cl, orgsClient, restCfg, cfg)
	initializer.newWorkspaceClientFunc = func(string) (client.Client, error) {
		return wsClient, nil
	}
	return initializer
}

func TestWorkspaceInitializer_Process_Organization(t *testing.T) {
	scheme := setupScheme(t)

	moduleDir := t.TempDir()
	modulePath := filepath.Join(moduleDir, "core-module.fga")
	require.NoError(t, os.WriteFile(modulePath, []byte("model"), 0o600))

	cfg := config.Config{}
	cfg.CoreModulePath = modulePath
	cfg.FGA.ObjectType = "core_platform-mesh_io_account"
	cfg.FGA.ParentRelation = "parent"
	cfg.FGA.CreatorRelation = "owner"

	lc := &kcpv1alpha1.LogicalCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
			Annotations: map[string]string{
				"kcp.io/path":                "root:security:org-workspace",
				logicalcluster.AnnotationKey: "root:security:org-workspace",
			},
		},
		Spec: kcpv1alpha1.LogicalClusterSpec{
			Owner: &kcpv1alpha1.LogicalClusterOwner{Cluster: "root:orgs", Name: "org-account"},
		},
		Status: kcpv1alpha1.LogicalClusterStatus{Initializers: []kcpv1alpha1.LogicalClusterInitializer{initializerName}},
	}

	account := &accountsv1alpha1.Account{
		ObjectMeta: metav1.ObjectMeta{Name: "org-account"},
		Spec: accountsv1alpha1.AccountSpec{
			Type:    accountsv1alpha1.AccountTypeOrg,
			Creator: ptr.To("creator@example.com"),
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(lc.DeepCopy(), account).
		WithStatusSubresource(&kcpv1alpha1.LogicalCluster{}).
		Build()

	store := &v1alpha1.Store{
		ObjectMeta: metav1.ObjectMeta{Name: "org-workspace"},
		Status:     v1alpha1.StoreStatus{StoreID: "store-id"},
	}

	orgsClient := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(store).
		WithStatusSubresource(&v1alpha1.Store{}).
		Build()

	wsAccountInfo := &accountsv1alpha1.AccountInfo{
		ObjectMeta: metav1.ObjectMeta{Name: "account"},
		Spec: accountsv1alpha1.AccountInfoSpec{
			FGA: accountsv1alpha1.FGAInfo{Store: accountsv1alpha1.StoreInfo{Id: "store-id"}},
			Account: accountsv1alpha1.AccountLocation{
				Name:            "org-account",
				OriginClusterId: "root:orgs",
			},
		},
	}

	wsClient := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(wsAccountInfo).
		Build()

	initializer := newTestInitializer(t, scheme, cl, orgsClient, cfg, wsClient)
	result, opErr := initializer.Process(context.Background(), lc.DeepCopy())
	require.Nil(t, opErr)
	assert.Equal(t, ctrl.Result{}, result)

	updatedStore := &v1alpha1.Store{}
	require.NoError(t, orgsClient.Get(context.Background(), client.ObjectKey{Name: "org-workspace"}, updatedStore))

	expectedTuples := []v1alpha1.Tuple{
		{Object: "role:authenticated", Relation: "assignee", User: "user:*"},
		{Object: "core_platform-mesh_io_account:root:orgs/org-account", Relation: "member", User: "role:authenticated#assignee"},
		{Object: "role:core_platform-mesh_io_account/root:orgs/org-account/owner", Relation: "assignee", User: "user:creator@example.com"},
		{Object: "core_platform-mesh_io_account:root:orgs/org-account", Relation: "owner", User: "role:core_platform-mesh_io_account/root:orgs/org-account/owner#assignee"},
	}
	assert.ElementsMatch(t, expectedTuples, updatedStore.Spec.Tuples)

	updatedLC := &kcpv1alpha1.LogicalCluster{}
	require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Name: "cluster"}, updatedLC))
	assert.NotContains(t, updatedLC.Status.Initializers, initializerName)
}

func TestWorkspaceInitializer_Process_Account(t *testing.T) {
	scheme := setupScheme(t)

	moduleDir := t.TempDir()
	modulePath := filepath.Join(moduleDir, "core-module.fga")
	require.NoError(t, os.WriteFile(modulePath, []byte("model"), 0o600))

	cfg := config.Config{}
	cfg.CoreModulePath = modulePath
	cfg.FGA.ObjectType = "core_platform-mesh_io_account"
	cfg.FGA.ParentRelation = "parent"
	cfg.FGA.CreatorRelation = "owner"

	lc := &kcpv1alpha1.LogicalCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
			Annotations: map[string]string{
				"kcp.io/path":                "root:security:org-workspace:child-account",
				logicalcluster.AnnotationKey: "root:security:org-workspace:child-account",
			},
		},
		Spec: kcpv1alpha1.LogicalClusterSpec{
			Owner: &kcpv1alpha1.LogicalClusterOwner{Cluster: "root:orgs", Name: "child-account"},
		},
		Status: kcpv1alpha1.LogicalClusterStatus{Initializers: []kcpv1alpha1.LogicalClusterInitializer{initializerName}},
	}

	account := &accountsv1alpha1.Account{
		ObjectMeta: metav1.ObjectMeta{Name: "child-account"},
		Spec: accountsv1alpha1.AccountSpec{
			Type:    accountsv1alpha1.AccountTypeAccount,
			Creator: ptr.To("system:serviceaccount:tenant:creator"),
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(lc.DeepCopy(), account).
		WithStatusSubresource(&kcpv1alpha1.LogicalCluster{}).
		Build()

	store := &v1alpha1.Store{
		ObjectMeta: metav1.ObjectMeta{Name: "org-workspace"},
		Status:     v1alpha1.StoreStatus{StoreID: "store-id"},
	}

	orgsClient := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(store).
		WithStatusSubresource(&v1alpha1.Store{}).
		Build()

	wsAccountInfo := &accountsv1alpha1.AccountInfo{
		ObjectMeta: metav1.ObjectMeta{Name: "account"},
		Spec: accountsv1alpha1.AccountInfoSpec{
			FGA: accountsv1alpha1.FGAInfo{Store: accountsv1alpha1.StoreInfo{Id: "store-id"}},
			Account: accountsv1alpha1.AccountLocation{
				Name:            "child-account",
				OriginClusterId: "root:orgs",
			},
			ParentAccount: &accountsv1alpha1.AccountLocation{
				Name:            "org-account",
				OriginClusterId: "root:orgs",
			},
			Organization: accountsv1alpha1.AccountLocation{Path: "root:security:org-workspace"},
		},
	}

	wsClient := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(wsAccountInfo).
		Build()

	initializer := newTestInitializer(t, scheme, cl, orgsClient, cfg, wsClient)
	result, opErr := initializer.Process(context.Background(), lc.DeepCopy())
	require.Nil(t, opErr)
	assert.Equal(t, ctrl.Result{}, result)

	updatedStore := &v1alpha1.Store{}
	require.NoError(t, orgsClient.Get(context.Background(), client.ObjectKey{Name: "org-workspace"}, updatedStore))

	expectedTuples := []v1alpha1.Tuple{
		{Object: "role:authenticated", Relation: "assignee", User: "user:*"},
		{Object: "core_platform-mesh_io_account:root:orgs/child-account", Relation: "member", User: "role:authenticated#assignee"},
		{Object: "core_platform-mesh_io_account:root:orgs/child-account", Relation: "parent", User: "core_platform-mesh_io_account:root:orgs/org-account"},
		{Object: "role:core_platform-mesh_io_account/root:orgs/child-account/owner", Relation: "assignee", User: "user:system.serviceaccount.tenant.creator"},
		{Object: "core_platform-mesh_io_account:root:orgs/child-account", Relation: "owner", User: "role:core_platform-mesh_io_account/root:orgs/child-account/owner#assignee"},
	}
	assert.ElementsMatch(t, expectedTuples, updatedStore.Spec.Tuples)
}

func TestWorkspaceInitializer_Process_RequeuesWhenAccountInfoMissing(t *testing.T) {
	scheme := setupScheme(t)

	moduleDir := t.TempDir()
	modulePath := filepath.Join(moduleDir, "core-module.fga")
	require.NoError(t, os.WriteFile(modulePath, []byte("model"), 0o600))

	cfg := config.Config{}
	cfg.CoreModulePath = modulePath
	cfg.FGA.ObjectType = "core_platform-mesh_io_account"
	cfg.FGA.ParentRelation = "parent"
	cfg.FGA.CreatorRelation = "owner"

	lc := &kcpv1alpha1.LogicalCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
			Annotations: map[string]string{
				"kcp.io/path":                "root:security:org-workspace",
				logicalcluster.AnnotationKey: "root:security:org-workspace",
			},
		},
		Spec: kcpv1alpha1.LogicalClusterSpec{
			Owner: &kcpv1alpha1.LogicalClusterOwner{Cluster: "root:orgs", Name: "org-account"},
		},
		Status: kcpv1alpha1.LogicalClusterStatus{Initializers: []kcpv1alpha1.LogicalClusterInitializer{initializerName}},
	}

	account := &accountsv1alpha1.Account{
		ObjectMeta: metav1.ObjectMeta{Name: "org-account"},
		Spec:       accountsv1alpha1.AccountSpec{Type: accountsv1alpha1.AccountTypeOrg},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(lc.DeepCopy(), account).
		WithStatusSubresource(&kcpv1alpha1.LogicalCluster{}).
		Build()

	store := &v1alpha1.Store{ObjectMeta: metav1.ObjectMeta{Name: "org-workspace"}}
	orgsClient := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(store).
		WithStatusSubresource(&v1alpha1.Store{}).
		Build()

	wsClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	initializer := newTestInitializer(t, scheme, cl, orgsClient, cfg, wsClient)
	result, opErr := initializer.Process(context.Background(), lc.DeepCopy())
	require.Nil(t, opErr)
	assert.True(t, result.Requeue)
}

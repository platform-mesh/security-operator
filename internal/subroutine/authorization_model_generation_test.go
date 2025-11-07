package subroutine_test

import (
	"context"
	"testing"

	kcpv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/subroutine"
	"github.com/platform-mesh/security-operator/internal/subroutine/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	accountv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

func binding(name, path string) *kcpv1alpha1.APIBinding {
	return &kcpv1alpha1.APIBinding{
		Spec: kcpv1alpha1.APIBindingSpec{Reference: kcpv1alpha1.BindingReference{Export: &kcpv1alpha1.ExportBindingReference{Name: name, Path: path}}},
	}
}

func bindingWithCluster(name, path, cluster string) *kcpv1alpha1.APIBinding {
	b := binding(name, path)
	if b.Annotations == nil {
		b.Annotations = make(map[string]string)
	}
	b.Annotations["kcp.io/cluster"] = cluster
	return b
}

func bindingWithStatus(name, path, exportCluster string) *kcpv1alpha1.APIBinding {
	b := binding(name, path)
	b.Status.APIExportClusterName = exportCluster
	return b
}

func mockAccountInfo(cl *mocks.MockClient, orgName, originCluster string) {
	cl.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
		if acc, ok := o.(*accountv1alpha1.AccountInfo); ok {
			acc.Spec.Organization.Name = orgName
			acc.Spec.Organization.OriginClusterId = originCluster
		}
		return nil
	}).Once()
}

func mockAccountInfoWithOrg(cl *mocks.MockClient, orgId string) {
	cl.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
		if acc, ok := o.(*accountv1alpha1.AccountInfo); ok {
			acc.Spec.Organization.Name = "org"
			acc.Spec.Organization.GeneratedClusterId = orgId
		}
		return nil
	})
}

func TestAuthorizationModelGeneration_Process(t *testing.T) {
	tests := []struct {
		name        string
		binding     *kcpv1alpha1.APIBinding
		mockSetup   func(*mocks.MockClient)
		expectError bool
	}{
		{
			name:        "error on ClusterFromContext in Process",
			binding:     binding("foo", "bar"),
			mockSetup:   func(*mocks.MockClient) {},
			expectError: true,
		},
		{
			name:    "early return when accountInfo not found",
			binding: binding("foo", "bar"),
			mockSetup: func(cl *mocks.MockClient) {
				cl.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(
					kerrors.NewNotFound(schema.GroupResource{Group: "account.platform-mesh.org", Resource: "accountinfos"}, "account"))
			},
		},
		{
			name:        "error on getting apiExport",
			binding:     binding("foo", "bar"),
			expectError: true,
			mockSetup: func(cl *mocks.MockClient) {
				cl.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
				cl.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError)
			},
		},
		{
			name:        "error from CreateOrUpdate when creating model",
			binding:     binding("foo", "bar"),
			expectError: true,
			mockSetup: func(cl *mocks.MockClient) {
				cl.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
					if _, ok := o.(*accountv1alpha1.AccountInfo); ok {
						return nil
					}
					return nil
				}).Once()
				cl.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
					if ae, ok := o.(*kcpv1alpha1.APIExport); ok {
						ae.Spec.LatestResourceSchemas = []string{"schema1"}
						return nil
					}
					return nil
				}).Once()
				cl.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
					if rs, ok := o.(*kcpv1alpha1.APIResourceSchema); ok {
						rs.Spec.Group = "group"
						rs.Spec.Names.Plural = "things"
						rs.Spec.Names.Singular = "thing"
						return nil
					}
					return nil
				}).Once()
				cl.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(
					kerrors.NewNotFound(schema.GroupResource{Group: "core.platform-mesh.io", Resource: "authorizationmodels"}, "things-org")).Once()
				cl.EXPECT().Create(mock.Anything, mock.Anything).Return(assert.AnError).Once()
			},
		},
		{
			name:    "skip core exports in Process",
			binding: binding("core.platform-mesh.io", "root"),
			mockSetup: func(cl *mocks.MockClient) {
				mockAccountInfo(cl, "org", "origin")
			},
		},
		{
			name:    "generate model in Process",
			binding: binding("foo", "bar"),
			mockSetup: func(cl *mocks.MockClient) {
				mockAccountInfo(cl, "org", "origin")
				setupAPIExportMocks(cl, "group", "foos", "foo", apiextensionsv1.ClusterScoped)
			},
		},
		{
			name:    "generate model in Process with namespaced scope",
			binding: binding("foo", "bar"),
			mockSetup: func(cl *mocks.MockClient) {
				mockAccountInfo(cl, "org", "origin")
				setupAPIExportMocks(cl, "group", "foos", "foo", apiextensionsv1.NamespaceScoped)
			},
		},
		{
			name:        "error on apiExportClient.Get in Process",
			binding:     binding("foo", "bar"),
			expectError: true,
			mockSetup: func(cl *mocks.MockClient) {
				mockAccountInfo(cl, "org", "origin")
				cl.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError)
			},
		},
		{
			name:        "error on apiExportClient.Get resource schema in Process",
			binding:     binding("foo", "bar"),
			expectError: true,
			mockSetup: func(cl *mocks.MockClient) {
				mockAccountInfo(cl, "org", "origin")
				cl.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
					if ae, ok := o.(*kcpv1alpha1.APIExport); ok {
						ae.Spec.LatestResourceSchemas = []string{"schema1"}
						return nil
					}
					return nil
				}).Once()
				cl.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError)
			},
		},
		{
			name:    "generate model in Process with longestRelationName > 50",
			binding: binding("foo", "bar"),
			mockSetup: func(cl *mocks.MockClient) {
				mockAccountInfo(cl, "org", "origin")
				setupAPIExportMocks(cl, "averyveryveryveryveryveryveryveryverylonggroup.platform-mesh.org", "plural", "singular", apiextensionsv1.ClusterScoped)
			},
		},
		{
			name:        "error on Get accountInfo in Process (not NotFound)",
			binding:     binding("foo", "bar"),
			expectError: true,
			mockSetup: func(cl *mocks.MockClient) {
				cl.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError)
			},
		},
		{
			name:        "error on GetCluster for APIExport cluster in Process",
			binding:     bindingWithStatus("foo", "bar", "export-cluster"),
			expectError: true,
			mockSetup: func(cl *mocks.MockClient) {
				mockAccountInfo(cl, "org", "origin")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := mocks.NewMockManager(t)
			allClient := mocks.NewMockClient(t)
			cluster := mocks.NewMockCluster(t)
			kcpClient := mocks.NewMockClient(t)

			switch test.name {
			case "error on ClusterFromContext in Process":
				manager.EXPECT().ClusterFromContext(mock.Anything).Return(nil, assert.AnError)
			case "error on GetCluster for APIExport cluster in Process":
				manager.EXPECT().ClusterFromContext(mock.Anything).Return(cluster, nil)
				cluster.EXPECT().GetClient().Return(kcpClient).Maybe()
				if test.mockSetup != nil {
					test.mockSetup(kcpClient)
				}
				manager.EXPECT().GetCluster(mock.Anything, test.binding.Status.APIExportClusterName).Return(nil, assert.AnError)
			default:
				manager.EXPECT().ClusterFromContext(mock.Anything).Return(cluster, nil)
				manager.EXPECT().GetCluster(mock.Anything, mock.Anything).Return(cluster, nil).Maybe()
				cluster.EXPECT().GetClient().Return(kcpClient).Maybe()
				if test.mockSetup != nil {
					test.mockSetup(kcpClient)
				}
			}
			sub := subroutine.NewAuthorizationModelGenerationSubroutine(manager, allClient)
			_, err := sub.Process(context.Background(), test.binding)
			if test.expectError {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

func setupAPIExportMocks(cl *mocks.MockClient, group, plural, singular string, scope apiextensionsv1.ResourceScope) {
	cl.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
		if ae, ok := o.(*kcpv1alpha1.APIExport); ok {
			ae.Spec.LatestResourceSchemas = []string{"schema1"}
			return nil
		}
		return nil
	}).Once()
	cl.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
		if rs, ok := o.(*kcpv1alpha1.APIResourceSchema); ok {
			rs.Spec.Group = group
			rs.Spec.Names.Plural = plural
			rs.Spec.Names.Singular = singular
			rs.Spec.Scope = scope
			return nil
		}
		return nil
	}).Once()
	cl.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil)
	cl.EXPECT().Update(mock.Anything, mock.Anything).Return(nil).Maybe()
	cl.EXPECT().Create(mock.Anything, mock.Anything).Return(nil).Maybe()
}

type finalizeTestConfig struct {
	err                   error
	bindingsInList        []*kcpv1alpha1.APIBinding
	matchingBindingsCount int
	deleteError           error
	differentOrg          bool
}

func TestAuthorizationModelGeneration_Finalize(t *testing.T) {
	tests := []struct {
		name        string
		binding     *kcpv1alpha1.APIBinding
		config      finalizeTestConfig
		mockSetup   func(*mocks.MockClient, *kcpv1alpha1.APIBinding)
		expectError bool
	}{
		{
			name:    "bindings with non-matching export are skipped",
			binding: bindingWithStatus("foo", "bar", "export-cluster"),
			config: finalizeTestConfig{
				bindingsInList: []*kcpv1alpha1.APIBinding{
					bindingWithCluster("foo", "bar", "cluster1"),
					bindingWithCluster("other", "other", "cluster2"),
				},
				matchingBindingsCount: 1,
			},
			mockSetup: func(cl *mocks.MockClient, _ *kcpv1alpha1.APIBinding) {
				mockAccountInfoWithOrg(cl, "org-id")
			},
		},
		{
			name:        "error on ClusterFromContext in Finalize",
			binding:     bindingWithStatus("foo", "bar", "export-cluster"),
			config:      finalizeTestConfig{err: assert.AnError},
			expectError: true,
		},
		{
			name:        "early return when accountInfo missing in Finalize",
			binding:     bindingWithStatus("foo", "bar", "export-cluster"),
			config:      finalizeTestConfig{err: kerrors.NewNotFound(schema.GroupResource{Group: "account.platform-mesh.org", Resource: "accountinfos"}, "account")},
			expectError: true,
			mockSetup: func(cl *mocks.MockClient, _ *kcpv1alpha1.APIBinding) {
				cl.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(
					kerrors.NewNotFound(schema.GroupResource{Group: "account.platform-mesh.org", Resource: "accountinfos"}, "account"))
			},
		},
		{
			name:        "delete returns error in Finalize",
			binding:     bindingWithStatus("foo", "bar", "export-cluster"),
			config:      finalizeTestConfig{matchingBindingsCount: 1, deleteError: assert.AnError},
			mockSetup:   func(cl *mocks.MockClient, _ *kcpv1alpha1.APIBinding) { mockAccountInfoWithOrg(cl, "org-id") },
			expectError: true,
		},
		{
			name:    "skip Finalize if other bindings exist",
			binding: bindingWithStatus("foo", "bar", "export-cluster"),
			config: finalizeTestConfig{
				bindingsInList: []*kcpv1alpha1.APIBinding{
					bindingWithCluster("foo", "bar", "cluster1"),
					bindingWithCluster("foo", "bar", "cluster2"),
				},
				matchingBindingsCount: 2,
			},
			mockSetup: func(cl *mocks.MockClient, _ *kcpv1alpha1.APIBinding) { mockAccountInfoWithOrg(cl, "org-id") },
		},
		{
			name:      "delete model in Finalize if last binding",
			binding:   bindingWithStatus("foo", "bar", "export-cluster"),
			config:    finalizeTestConfig{matchingBindingsCount: 1},
			mockSetup: func(cl *mocks.MockClient, _ *kcpv1alpha1.APIBinding) { mockAccountInfoWithOrg(cl, "org-id") },
		},
		{
			name:      "delete model in Finalize but model is not found",
			binding:   bindingWithStatus("foo", "bar", "export-cluster"),
			config:    finalizeTestConfig{matchingBindingsCount: 1, deleteError: kerrors.NewNotFound(schema.GroupResource{Group: "core.platform-mesh.io", Resource: "authorizationmodels"}, "foos-org")},
			mockSetup: func(cl *mocks.MockClient, _ *kcpv1alpha1.APIBinding) { mockAccountInfoWithOrg(cl, "org-id") },
		},
		{
			name:        "error on List in Finalize",
			binding:     binding("foo", "bar"),
			config:      finalizeTestConfig{err: assert.AnError},
			expectError: true,
		},
		{
			name:    "error on getRelatedAuthorizationModels in Finalize",
			binding: bindingWithStatus("foo", "bar", "export-cluster"),
			config:  finalizeTestConfig{err: assert.AnError},
			mockSetup: func(cl *mocks.MockClient, _ *kcpv1alpha1.APIBinding) {
				cl.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError)
			},
			expectError: true,
		},
		{
			name:    "only bindings for same org are counted; delete called if only one, not called if none",
			binding: bindingWithStatus("foo", "bar", "export-cluster"),
			config: finalizeTestConfig{
				bindingsInList: []*kcpv1alpha1.APIBinding{
					bindingWithCluster("foo", "bar", "cluster1"),
					bindingWithCluster("foo", "bar", "cluster2"),
				},
				matchingBindingsCount: 2,
			},
			mockSetup: func(cl *mocks.MockClient, _ *kcpv1alpha1.APIBinding) { mockAccountInfoWithOrg(cl, "org-id") },
		},
		{
			name:    "error on GetCluster for binding workspace in Finalize loop",
			binding: bindingWithStatus("foo", "bar", "export-cluster"),
			config: finalizeTestConfig{
				bindingsInList:        []*kcpv1alpha1.APIBinding{bindingWithCluster("foo", "bar", "cluster1")},
				matchingBindingsCount: 1,
				err:                   assert.AnError,
			},
			mockSetup:   func(cl *mocks.MockClient, _ *kcpv1alpha1.APIBinding) { mockAccountInfoWithOrg(cl, "org-id") },
			expectError: true,
		},
		{
			name:    "error on Get accountInfo in Finalize loop (not NotFound)",
			binding: bindingWithStatus("foo", "bar", "export-cluster"),
			config: finalizeTestConfig{
				bindingsInList:        []*kcpv1alpha1.APIBinding{bindingWithCluster("foo", "bar", "cluster1")},
				matchingBindingsCount: 1,
				err:                   assert.AnError,
			},
			mockSetup:   func(cl *mocks.MockClient, _ *kcpv1alpha1.APIBinding) { mockAccountInfoWithOrg(cl, "org-id") },
			expectError: true,
		},
		{
			name:    "bindings with different org are skipped in Finalize",
			binding: bindingWithStatus("foo", "bar", "export-cluster"),
			config: finalizeTestConfig{
				bindingsInList:        []*kcpv1alpha1.APIBinding{bindingWithCluster("foo", "bar", "cluster1")},
				matchingBindingsCount: 1,
				differentOrg:          true,
			},
			mockSetup: func(cl *mocks.MockClient, _ *kcpv1alpha1.APIBinding) { mockAccountInfoWithOrg(cl, "org-id") },
		},
		{
			name:        "error on GetCluster for APIExport cluster in Finalize",
			binding:     bindingWithStatus("foo", "bar", "export-cluster"),
			config:      finalizeTestConfig{matchingBindingsCount: 1, err: assert.AnError},
			mockSetup:   func(cl *mocks.MockClient, _ *kcpv1alpha1.APIBinding) { mockAccountInfoWithOrg(cl, "org-id") },
			expectError: true,
		},
		{
			name:        "error on Get APIExport in Finalize",
			binding:     bindingWithStatus("foo", "bar", "export-cluster"),
			config:      finalizeTestConfig{matchingBindingsCount: 1, err: assert.AnError},
			mockSetup:   func(cl *mocks.MockClient, _ *kcpv1alpha1.APIBinding) { mockAccountInfoWithOrg(cl, "org-id") },
			expectError: true,
		},
		{
			name:        "error on Get resource schema in Finalize",
			binding:     bindingWithStatus("foo", "bar", "export-cluster"),
			config:      finalizeTestConfig{matchingBindingsCount: 1, err: assert.AnError},
			mockSetup:   func(cl *mocks.MockClient, _ *kcpv1alpha1.APIBinding) { mockAccountInfoWithOrg(cl, "org-id") },
			expectError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := mocks.NewMockManager(t)
			allClient := mocks.NewMockClient(t)
			bindingCluster := mocks.NewMockCluster(t)
			bindingClient := mocks.NewMockClient(t)
			apiExportCluster := mocks.NewMockCluster(t)
			apiExportClient := mocks.NewMockClient(t)

			setupFinalizeExpectations(t, manager, allClient, bindingCluster, bindingClient, apiExportCluster, apiExportClient, test.config, test.binding, test.name, test.mockSetup)

			sub := subroutine.NewAuthorizationModelGenerationSubroutine(manager, allClient)
			_, err := sub.Finalize(context.Background(), test.binding)
			if test.expectError {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

func setupFinalizeExpectations(t *testing.T, manager *mocks.MockManager, allClient *mocks.MockClient, bindingCluster *mocks.MockCluster, bindingClient *mocks.MockClient, apiExportCluster *mocks.MockCluster, apiExportClient *mocks.MockClient, config finalizeTestConfig, testBinding *kcpv1alpha1.APIBinding, testName string, mockSetup func(*mocks.MockClient, *kcpv1alpha1.APIBinding)) {
	if config.err != nil {
		if kerrors.IsNotFound(config.err) {
			manager.EXPECT().ClusterFromContext(mock.Anything).Return(bindingCluster, nil)
			bindingCluster.EXPECT().GetClient().Return(bindingClient).Maybe()
			allClient.EXPECT().List(mock.Anything, mock.Anything).RunAndReturn(setupBindingList(testBinding, nil)).Once()
			if mockSetup != nil {
				mockSetup(bindingClient, testBinding)
			} else {
				bindingClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(config.err)
			}
			return
		}
		if testBinding.Status.APIExportClusterName == "" || testName == "error on List in Finalize" {
			manager.EXPECT().ClusterFromContext(mock.Anything).Return(bindingCluster, nil)
			bindingCluster.EXPECT().GetClient().Return(bindingClient).Maybe()
			allClient.EXPECT().List(mock.Anything, mock.Anything).Return(config.err).Once()
			return
		}
		if testName == "error on ClusterFromContext in Finalize" {
			manager.EXPECT().ClusterFromContext(mock.Anything).Return(nil, config.err)
			return
		}
		if mockSetup != nil {
			manager.EXPECT().ClusterFromContext(mock.Anything).Return(bindingCluster, nil)
			bindingCluster.EXPECT().GetClient().Return(bindingClient).Maybe()
			allClient.EXPECT().List(mock.Anything, mock.Anything).RunAndReturn(setupBindingList(testBinding, config.bindingsInList)).Once()
			mockSetup(bindingClient, testBinding)
			if testName == "error on GetCluster for binding workspace in Finalize loop" || testName == "error on Get accountInfo in Finalize loop (not NotFound)" {
				if config.matchingBindingsCount > 0 {
					setupBindingLoopExpectations(t, manager, config, testBinding, testName)
				}
				return
			}
			if testName == "error on GetCluster for APIExport cluster in Finalize" || testName == "error on Get APIExport in Finalize" || testName == "error on Get resource schema in Finalize" {
				if config.matchingBindingsCount > 0 {
					setupBindingLoopExpectations(t, manager, config, testBinding, testName)
				}
				if config.matchingBindingsCount <= 1 {
					setupAPIExportExpectations(t, manager, apiExportCluster, apiExportClient, config, testBinding, testName)
				}
			}
			return
		}
	}

	manager.EXPECT().ClusterFromContext(mock.Anything).Return(bindingCluster, nil)
	bindingCluster.EXPECT().GetClient().Return(bindingClient).Maybe()
	allClient.EXPECT().List(mock.Anything, mock.Anything).RunAndReturn(setupBindingList(testBinding, config.bindingsInList)).Once()

	if mockSetup != nil {
		mockSetup(bindingClient, testBinding)
	}
	if config.matchingBindingsCount > 0 {
		setupBindingLoopExpectations(t, manager, config, testBinding, testName)
	}
	if config.matchingBindingsCount <= 1 {
		setupAPIExportExpectations(t, manager, apiExportCluster, apiExportClient, config, testBinding, testName)
	}
}

func setupBindingList(testBinding *kcpv1alpha1.APIBinding, bindingsInList []*kcpv1alpha1.APIBinding) func(context.Context, client.ObjectList, ...client.ListOption) error {
	return func(ctx context.Context, ol client.ObjectList, lo ...client.ListOption) error {
		list := ol.(*kcpv1alpha1.APIBindingList)
		if bindingsInList != nil {
			items := make([]kcpv1alpha1.APIBinding, len(bindingsInList))
			for i, b := range bindingsInList {
				items[i] = *b
			}
			list.Items = items
		} else {
			binding := testBinding.DeepCopy()
			if binding.Annotations == nil {
				binding.Annotations = make(map[string]string)
			}
			binding.Annotations["kcp.io/cluster"] = "cluster1"
			list.Items = []kcpv1alpha1.APIBinding{*binding}
		}
		return nil
	}
}

func setupBindingLoopExpectations(t *testing.T, manager *mocks.MockManager, config finalizeTestConfig, testBinding *kcpv1alpha1.APIBinding, testName string) {
	bindingWsCluster := mocks.NewMockCluster(t)
	bindingWsClient := mocks.NewMockClient(t)

	if config.err != nil && testName == "error on GetCluster for binding workspace in Finalize loop" {
		manager.EXPECT().GetCluster(mock.Anything, mock.MatchedBy(func(clusterName string) bool {
			return clusterName != testBinding.Status.APIExportClusterName
		})).Return(nil, config.err).Once()
		return
	}

	manager.EXPECT().GetCluster(mock.Anything, mock.MatchedBy(func(clusterName string) bool {
		return clusterName != testBinding.Status.APIExportClusterName
	})).Return(bindingWsCluster, nil).Times(config.matchingBindingsCount)
	bindingWsCluster.EXPECT().GetClient().Return(bindingWsClient).Times(config.matchingBindingsCount)

	if config.err != nil && testName == "error on Get accountInfo in Finalize loop (not NotFound)" {
		bindingWsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(config.err).Once()
		return
	}

	bindingWsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
		if acc, ok := o.(*accountv1alpha1.AccountInfo); ok {
			acc.Spec.Organization.Name = "org"
			if config.differentOrg {
				acc.Spec.Organization.GeneratedClusterId = "different-org-id"
			} else {
				acc.Spec.Organization.GeneratedClusterId = "org-id"
			}
		}
		return nil
	}).Times(config.matchingBindingsCount)
}

func setupAPIExportExpectations(t *testing.T, manager *mocks.MockManager, apiExportCluster *mocks.MockCluster, apiExportClient *mocks.MockClient, config finalizeTestConfig, testBinding *kcpv1alpha1.APIBinding, testName string) {
	if config.err != nil && testName == "error on GetCluster for APIExport cluster in Finalize" {
		manager.EXPECT().GetCluster(mock.Anything, testBinding.Status.APIExportClusterName).Return(nil, config.err).Once()
		return
	}

	manager.EXPECT().GetCluster(mock.Anything, testBinding.Status.APIExportClusterName).Return(apiExportCluster, nil).Once()
	apiExportCluster.EXPECT().GetClient().Return(apiExportClient)

	if config.err != nil && testName == "error on Get APIExport in Finalize" {
		apiExportClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(config.err).Once()
		return
	}

	apiExportClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
		if ae, ok := o.(*kcpv1alpha1.APIExport); ok {
			ae.Spec.LatestResourceSchemas = []string{"schema1"}
		}
		return nil
	}).Once()

	if config.err != nil && testName == "error on Get resource schema in Finalize" {
		apiExportClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(config.err).Once()
		return
	}

	apiExportClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
		if rs, ok := o.(*kcpv1alpha1.APIResourceSchema); ok {
			rs.Spec.Names.Plural = "foos"
		}
		return nil
	}).Once()

	if config.deleteError != nil {
		apiExportClient.EXPECT().Delete(mock.Anything, mock.Anything).Return(config.deleteError).Once()
	} else {
		apiExportClient.EXPECT().Delete(mock.Anything, mock.Anything).Return(nil).Once()
	}
}

func TestFinalizeAuthorizationModelGeneration(t *testing.T) {
	allClient := mocks.NewMockClient(t)
	finalizers := subroutine.NewAuthorizationModelGenerationSubroutine(nil, allClient).Finalizers(nil)
	assert.Equal(t, []string{"core.platform-mesh.io/apibinding-finalizer"}, finalizers)
}

func TestAuthorizationModelGenerationSubroutine_GetName(t *testing.T) {
	allClient := mocks.NewMockClient(t)
	sub := subroutine.NewAuthorizationModelGenerationSubroutine(nil, allClient)
	assert.Equal(t, "AuthorizationModelGeneration", sub.GetName())
}

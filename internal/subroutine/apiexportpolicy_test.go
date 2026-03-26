package subroutine_test

import (
	"context"
	"errors"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	accountsv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/golang-commons/logger/testlogger"
	corev1alpha1 "github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/security-operator/internal/subroutine"
	"github.com/platform-mesh/security-operator/internal/subroutine/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/kcp-dev/logicalcluster/v3"
	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

func getAPIExportPolicyTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(corev1alpha1.AddToScheme(scheme))
	utilruntime.Must(accountsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(kcpcorev1alpha1.AddToScheme(scheme))
	return scheme
}

func TestAPIExportPolicySubroutine_GetName(t *testing.T) {
	sub := subroutine.NewAPIExportPolicySubroutine(nil, nil, nil, nil, nil)
	assert.Equal(t, "APIExportPolicySubroutine", sub.GetName())
}

func TestAPIExportPolicySubroutine_Finalizers(t *testing.T) {
	sub := subroutine.NewAPIExportPolicySubroutine(nil, nil, nil, nil, nil)
	assert.Equal(t, []string{"system.platform-mesh.io/apiexportpolicy-finalizer"}, sub.Finalizers(nil))
}

func TestAPIExportPolicySubroutine_Process(t *testing.T) {
	tests := []struct {
		name        string
		policy      *corev1alpha1.APIExportPolicy
		setupMocks  func(*testing.T, *mocks.MockOpenFGAServiceClient, *mocks.MockManager, *mocks.MockStoreIDGetter, *mocks.MockCluster, *mocks.MockKcpHelper)
		cfg         *config.Config
		expectError bool
	}{
		{
			name: "should fail when getting provider cluster ID fails - LogicalCluster not found",
			policy: &corev1alpha1.APIExportPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-policy",
				},
				Spec: corev1alpha1.APIExportPolicySpec{
					APIExportRef: corev1alpha1.APIExportRef{
						Name:        "my-export",
						ClusterPath: "root:providers:my-provider",
					},
					AllowPathExpressions: []string{"root:orgs:acme"},
				},
			},
			setupMocks: func(t *testing.T, fga *mocks.MockOpenFGAServiceClient, mgr *mocks.MockManager, storeIDGetter *mocks.MockStoreIDGetter, cluster *mocks.MockCluster, kcpHelper *mocks.MockKcpHelper) {
				kcpHelper.EXPECT().NewForLogicalCluster(mock.Anything).Return(nil, errors.New("logical cluster not found")).Maybe()
			},
			cfg:         &config.Config{},
			expectError: true,
		},
		{
			name: "should fail when expression starts with wrong prefix",
			policy: &corev1alpha1.APIExportPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-policy",
				},
				Spec: corev1alpha1.APIExportPolicySpec{
					APIExportRef: corev1alpha1.APIExportRef{
						Name:        "my-export",
						ClusterPath: "root:providers:my-provider",
					},
					AllowPathExpressions: []string{"wrong:prefix:acme"},
				},
			},
			setupMocks: func(t *testing.T, fga *mocks.MockOpenFGAServiceClient, mgr *mocks.MockManager, storeIDGetter *mocks.MockStoreIDGetter, cluster *mocks.MockCluster, kcpHelper *mocks.MockKcpHelper) {
				scheme := getAPIExportPolicyTestScheme()
				providerClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(&kcpcorev1alpha1.LogicalCluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:        "cluster",
							Annotations: map[string]string{"kcp.io/cluster": "provider-cluster-id"},
						},
					}).
					Build()
				kcpHelper.EXPECT().NewForLogicalCluster(mock.Anything).Return(providerClient, nil).Maybe()
			},
			cfg:         &config.Config{},
			expectError: true,
		},
		{
			name: "should handle wildcard expression with root:orgs path - GetAllClient fails",
			policy: &corev1alpha1.APIExportPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-policy",
				},
				Spec: corev1alpha1.APIExportPolicySpec{
					APIExportRef: corev1alpha1.APIExportRef{
						Name:        "my-export",
						ClusterPath: "root:providers:my-provider",
					},
					AllowPathExpressions: []string{"root:orgs:*"},
				},
			},
			setupMocks: func(t *testing.T, fga *mocks.MockOpenFGAServiceClient, mgr *mocks.MockManager, storeIDGetter *mocks.MockStoreIDGetter, cluster *mocks.MockCluster, kcpHelper *mocks.MockKcpHelper) {
				scheme := getAPIExportPolicyTestScheme()
				providerClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(&kcpcorev1alpha1.LogicalCluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:        "cluster",
							Annotations: map[string]string{"kcp.io/cluster": "provider-cluster-id"},
						},
					}).
					Build()
				kcpHelper.EXPECT().NewForLogicalCluster(mock.Anything).Return(providerClient, nil).Maybe()
				kcpHelper.EXPECT().GetAllClient(mock.Anything, mock.Anything).Return(nil, errors.New("unable to create all client")).Maybe()
			},
			cfg:         &config.Config{},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fga := mocks.NewMockOpenFGAServiceClient(t)
			mgr := mocks.NewMockManager(t)
			storeIDGetter := mocks.NewMockStoreIDGetter(t)
			cluster := mocks.NewMockCluster(t)
			kcpHelper := mocks.NewMockKcpHelper(t)

			if tt.setupMocks != nil {
				tt.setupMocks(t, fga, mgr, storeIDGetter, cluster, kcpHelper)
			}

			l := testlogger.New()
			ctx := l.WithContext(context.Background())

			sub := subroutine.NewAPIExportPolicySubroutine(fga, mgr, tt.cfg, storeIDGetter, kcpHelper)

			_, err := sub.Process(ctx, tt.policy)

			if tt.expectError {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

func TestAPIExportPolicySubroutine_Finalize(t *testing.T) {
	tests := []struct {
		name        string
		policy      *corev1alpha1.APIExportPolicy
		setupMocks  func(*testing.T, *mocks.MockOpenFGAServiceClient, *mocks.MockManager, *mocks.MockStoreIDGetter, *mocks.MockKcpHelper)
		cfg         *config.Config
		expectError bool
	}{
		{
			name: "should fail when getting provider cluster ID fails",
			policy: &corev1alpha1.APIExportPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-policy",
				},
				Spec: corev1alpha1.APIExportPolicySpec{
					APIExportRef: corev1alpha1.APIExportRef{
						Name:        "my-export",
						ClusterPath: "root:providers:my-provider",
					},
					AllowPathExpressions: []string{"root:orgs:acme"},
				},
			},
			setupMocks: func(t *testing.T, fga *mocks.MockOpenFGAServiceClient, mgr *mocks.MockManager, storeIDGetter *mocks.MockStoreIDGetter, kcpHelper *mocks.MockKcpHelper) {
				kcpHelper.EXPECT().NewForLogicalCluster(mock.Anything).Return(nil, errors.New("failed to get clusterID")).Maybe()
			},
			cfg:         &config.Config{},
			expectError: true,
		},
		{
			name: "should handle finalize with wildcard expression - GetAllClient fails",
			policy: &corev1alpha1.APIExportPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-policy",
				},
				Spec: corev1alpha1.APIExportPolicySpec{
					APIExportRef: corev1alpha1.APIExportRef{
						Name:        "my-export",
						ClusterPath: "root:providers:my-provider",
					},
					AllowPathExpressions: []string{"root:orgs:acme:*"},
				},
			},
			setupMocks: func(t *testing.T, fga *mocks.MockOpenFGAServiceClient, mgr *mocks.MockManager, storeIDGetter *mocks.MockStoreIDGetter, kcpHelper *mocks.MockKcpHelper) {
				scheme := getAPIExportPolicyTestScheme()
				providerClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(&kcpcorev1alpha1.LogicalCluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:        "cluster",
							Annotations: map[string]string{"kcp.io/cluster": "provider-cluster-id"},
						},
					}).
					Build()
				kcpHelper.EXPECT().NewForLogicalCluster(mock.Anything).Return(providerClient, nil).Maybe()
			},
			cfg:         &config.Config{},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fga := mocks.NewMockOpenFGAServiceClient(t)
			mgr := mocks.NewMockManager(t)
			storeIDGetter := mocks.NewMockStoreIDGetter(t)
			kcpHelper := mocks.NewMockKcpHelper(t)

			if tt.setupMocks != nil {
				tt.setupMocks(t, fga, mgr, storeIDGetter, kcpHelper)
			}

			l := testlogger.New()
			ctx := l.WithContext(context.Background())

			sub := subroutine.NewAPIExportPolicySubroutine(fga, mgr, tt.cfg, storeIDGetter, kcpHelper)

			_, err := sub.Finalize(ctx, tt.policy)

			if tt.expectError {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

func TestAPIExportPolicySubroutine_Process_Success(t *testing.T) {
	tests := []struct {
		name                string
		policy              *corev1alpha1.APIExportPolicy
		setupMocks          func(*testing.T, *mocks.MockOpenFGAServiceClient, *mocks.MockManager, *mocks.MockStoreIDGetter, *mocks.MockCluster, *mocks.MockKcpHelper)
		cfg                 *config.Config
		expectError         bool
		expectedTupleWrites []corev1alpha1.Tuple
	}{
		{
			name: "should write correct FGA tuple for single org expression",
			policy: &corev1alpha1.APIExportPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-policy",
				},
				Spec: corev1alpha1.APIExportPolicySpec{
					APIExportRef: corev1alpha1.APIExportRef{
						Name:        "my-export",
						ClusterPath: "root:providers:my-provider",
					},
					AllowPathExpressions: []string{"root:orgs:acme"},
				},
			},
			setupMocks: func(t *testing.T, fga *mocks.MockOpenFGAServiceClient, mgr *mocks.MockManager, storeIDGetter *mocks.MockStoreIDGetter, cluster *mocks.MockCluster, kcpHelper *mocks.MockKcpHelper) {
				scheme := getAPIExportPolicyTestScheme()

				// Provider cluster client
				providerClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(&kcpcorev1alpha1.LogicalCluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:        "cluster",
							Annotations: map[string]string{"kcp.io/cluster": "provider-cluster-id"},
						},
					}).
					Build()

				// Target workspace client with AccountInfo
				targetClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(&accountsv1alpha1.AccountInfo{
						ObjectMeta: metav1.ObjectMeta{
							Name: "account",
						},
						Spec: accountsv1alpha1.AccountInfoSpec{
							Account: accountsv1alpha1.AccountLocation{
								Name:            "acme-account",
								OriginClusterId: "acme-cluster-id",
								Type:            accountsv1alpha1.AccountTypeOrg,
							},
							Organization: accountsv1alpha1.AccountLocation{
								Name: "acme-org",
							},
						},
					}).
					Build()

				// Cluster client for status patch
				clusterClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(&corev1alpha1.APIExportPolicy{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-policy",
						},
					}).
					WithStatusSubresource(&corev1alpha1.APIExportPolicy{}).
					Build()

				kcpHelper.EXPECT().NewForLogicalCluster(mock.MatchedBy(func(name logicalcluster.Name) bool {
					return name.String() == "root:providers:my-provider"
				})).Return(providerClient, nil)
				kcpHelper.EXPECT().NewForLogicalCluster(mock.MatchedBy(func(name logicalcluster.Name) bool {
					return name.String() == "root:orgs:acme"
				})).Return(targetClient, nil)

				storeIDGetter.EXPECT().Get(mock.Anything, "acme-org").Return("test-store-id", nil)

				// Expect FGA Write with specific tuple content
				fga.EXPECT().Write(mock.Anything, mock.MatchedBy(func(req *openfgav1.WriteRequest) bool {
					if req.StoreId != "test-store-id" {
						return false
					}
					if len(req.Writes.TupleKeys) != 1 {
						return false
					}
					tuple := req.Writes.TupleKeys[0]
					return tuple.Object == "core_platform-mesh_io_account:acme-cluster-id/acme-account" &&
						tuple.Relation == "bind" &&
						tuple.User == "apis_kcp_io_apiexport:provider-cluster-id/my-export"
				}), mock.Anything).Return(&openfgav1.WriteResponse{}, nil)

				mgr.EXPECT().ClusterFromContext(mock.Anything).Return(cluster, nil)
				cluster.EXPECT().GetClient().Return(clusterClient)
			},
			cfg:         &config.Config{},
			expectError: false,
		},
		{
			name: "should write correct FGA tuple with bind_inherited for wildcard expression",
			policy: &corev1alpha1.APIExportPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-policy",
				},
				Spec: corev1alpha1.APIExportPolicySpec{
					APIExportRef: corev1alpha1.APIExportRef{
						Name:        "my-export",
						ClusterPath: "root:providers:my-provider",
					},
					AllowPathExpressions: []string{"root:orgs:acme:*"},
				},
			},
			setupMocks: func(t *testing.T, fga *mocks.MockOpenFGAServiceClient, mgr *mocks.MockManager, storeIDGetter *mocks.MockStoreIDGetter, cluster *mocks.MockCluster, kcpHelper *mocks.MockKcpHelper) {
				scheme := getAPIExportPolicyTestScheme()

				// Provider cluster client
				providerClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(&kcpcorev1alpha1.LogicalCluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:        "cluster",
							Annotations: map[string]string{"kcp.io/cluster": "provider-cluster-id"},
						},
					}).
					Build()

				// Target workspace client with AccountInfo
				targetClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(&accountsv1alpha1.AccountInfo{
						ObjectMeta: metav1.ObjectMeta{
							Name: "account",
						},
						Spec: accountsv1alpha1.AccountInfoSpec{
							Account: accountsv1alpha1.AccountLocation{
								Name:            "acme-account",
								OriginClusterId: "acme-cluster-id",
								Type:            accountsv1alpha1.AccountTypeOrg,
							},
							Organization: accountsv1alpha1.AccountLocation{
								Name: "acme-org",
							},
						},
					}).
					Build()

				// Cluster client for status patch
				clusterClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(&corev1alpha1.APIExportPolicy{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-policy",
						},
					}).
					WithStatusSubresource(&corev1alpha1.APIExportPolicy{}).
					Build()

				kcpHelper.EXPECT().NewForLogicalCluster(mock.MatchedBy(func(name logicalcluster.Name) bool {
					return name.String() == "root:providers:my-provider"
				})).Return(providerClient, nil)
				kcpHelper.EXPECT().NewForLogicalCluster(mock.MatchedBy(func(name logicalcluster.Name) bool {
					return name.String() == "root:orgs:acme"
				})).Return(targetClient, nil)

				storeIDGetter.EXPECT().Get(mock.Anything, "acme-org").Return("test-store-id", nil)

				// Expect FGA Write with bind_inherited relation for wildcard
				fga.EXPECT().Write(mock.Anything, mock.MatchedBy(func(req *openfgav1.WriteRequest) bool {
					if req.StoreId != "test-store-id" {
						return false
					}
					if len(req.Writes.TupleKeys) != 1 {
						return false
					}
					tuple := req.Writes.TupleKeys[0]
					return tuple.Object == "core_platform-mesh_io_account:acme-cluster-id/acme-account" &&
						tuple.Relation == "bind_inherited" &&
						tuple.User == "apis_kcp_io_apiexport:provider-cluster-id/my-export"
				}), mock.Anything).Return(&openfgav1.WriteResponse{}, nil)

				mgr.EXPECT().ClusterFromContext(mock.Anything).Return(cluster, nil)
				cluster.EXPECT().GetClient().Return(clusterClient)
			},
			cfg:         &config.Config{},
			expectError: false,
		},
		{
			name: "should write FGA tuples for all orgs when root:orgs:* expression is used",
			policy: &corev1alpha1.APIExportPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-policy",
				},
				Spec: corev1alpha1.APIExportPolicySpec{
					APIExportRef: corev1alpha1.APIExportRef{
						Name:        "my-export",
						ClusterPath: "root:providers:my-provider",
					},
					AllowPathExpressions: []string{"root:orgs:*"},
				},
			},
			setupMocks: func(t *testing.T, fga *mocks.MockOpenFGAServiceClient, mgr *mocks.MockManager, storeIDGetter *mocks.MockStoreIDGetter, cluster *mocks.MockCluster, kcpHelper *mocks.MockKcpHelper) {
				scheme := getAPIExportPolicyTestScheme()

				// Provider cluster client
				providerClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(&kcpcorev1alpha1.LogicalCluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:        "cluster",
							Annotations: map[string]string{"kcp.io/cluster": "provider-cluster-id"},
						},
					}).
					Build()

				// All client with AccountInfo list
				allClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithLists(&accountsv1alpha1.AccountInfoList{
						Items: []accountsv1alpha1.AccountInfo{
							{
								ObjectMeta: metav1.ObjectMeta{
									Name: "account-1",
								},
								Spec: accountsv1alpha1.AccountInfoSpec{
									Account: accountsv1alpha1.AccountLocation{
										Name:            "org1-account",
										OriginClusterId: "org1-cluster-id",
										Type:            accountsv1alpha1.AccountTypeOrg,
									},
									Organization: accountsv1alpha1.AccountLocation{
										Name: "org1",
									},
								},
							},
							{
								ObjectMeta: metav1.ObjectMeta{
									Name: "account-2",
								},
								Spec: accountsv1alpha1.AccountInfoSpec{
									Account: accountsv1alpha1.AccountLocation{
										Name:            "org2-account",
										OriginClusterId: "org2-cluster-id",
										Type:            accountsv1alpha1.AccountTypeOrg,
									},
									Organization: accountsv1alpha1.AccountLocation{
										Name: "org2",
									},
								},
							},
						},
					}).
					Build()

				// Cluster client for status patch
				clusterClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(&corev1alpha1.APIExportPolicy{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-policy",
						},
					}).
					WithStatusSubresource(&corev1alpha1.APIExportPolicy{}).
					Build()

				kcpHelper.EXPECT().NewForLogicalCluster(mock.MatchedBy(func(name logicalcluster.Name) bool {
					return name.String() == "root:providers:my-provider"
				})).Return(providerClient, nil)
				kcpHelper.EXPECT().GetAllClient(mock.Anything, mock.Anything).Return(allClient, nil)

				storeIDGetter.EXPECT().Get(mock.Anything, "org1").Return("store-id-org1", nil)
				storeIDGetter.EXPECT().Get(mock.Anything, "org2").Return("store-id-org2", nil)

				// Expect FGA Write for org1 with bind_inherited relation
				fga.EXPECT().Write(mock.Anything, mock.MatchedBy(func(req *openfgav1.WriteRequest) bool {
					if req.StoreId != "store-id-org1" {
						return false
					}
					if len(req.Writes.TupleKeys) != 1 {
						return false
					}
					tuple := req.Writes.TupleKeys[0]
					return tuple.Object == "core_platform-mesh_io_account:org1-cluster-id/org1-account" &&
						tuple.Relation == "bind_inherited" &&
						tuple.User == "apis_kcp_io_apiexport:provider-cluster-id/my-export"
				}), mock.Anything).Return(&openfgav1.WriteResponse{}, nil)

				// Expect FGA Write for org2 with bind_inherited relation
				fga.EXPECT().Write(mock.Anything, mock.MatchedBy(func(req *openfgav1.WriteRequest) bool {
					if req.StoreId != "store-id-org2" {
						return false
					}
					if len(req.Writes.TupleKeys) != 1 {
						return false
					}
					tuple := req.Writes.TupleKeys[0]
					return tuple.Object == "core_platform-mesh_io_account:org2-cluster-id/org2-account" &&
						tuple.Relation == "bind_inherited" &&
						tuple.User == "apis_kcp_io_apiexport:provider-cluster-id/my-export"
				}), mock.Anything).Return(&openfgav1.WriteResponse{}, nil)

				mgr.EXPECT().ClusterFromContext(mock.Anything).Return(cluster, nil)
				cluster.EXPECT().GetClient().Return(clusterClient)
			},
			cfg:         &config.Config{},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fga := mocks.NewMockOpenFGAServiceClient(t)
			mgr := mocks.NewMockManager(t)
			storeIDGetter := mocks.NewMockStoreIDGetter(t)
			cluster := mocks.NewMockCluster(t)
			kcpHelper := mocks.NewMockKcpHelper(t)

			if tt.setupMocks != nil {
				tt.setupMocks(t, fga, mgr, storeIDGetter, cluster, kcpHelper)
			}

			l := testlogger.New()
			ctx := l.WithContext(context.Background())

			sub := subroutine.NewAPIExportPolicySubroutine(fga, mgr, tt.cfg, storeIDGetter, kcpHelper)

			_, err := sub.Process(ctx, tt.policy)

			if tt.expectError {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

func TestAPIExportPolicySubroutine_Finalize_Success(t *testing.T) {
	tests := []struct {
		name        string
		policy      *corev1alpha1.APIExportPolicy
		setupMocks  func(*testing.T, *mocks.MockOpenFGAServiceClient, *mocks.MockManager, *mocks.MockStoreIDGetter, *mocks.MockKcpHelper)
		cfg         *config.Config
		expectError bool
	}{
		{
			name: "should delete FGA tuple for single org expression",
			policy: &corev1alpha1.APIExportPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-policy",
				},
				Spec: corev1alpha1.APIExportPolicySpec{
					APIExportRef: corev1alpha1.APIExportRef{
						Name:        "my-export",
						ClusterPath: "root:providers:my-provider",
					},
					AllowPathExpressions: []string{"root:orgs:acme"},
				},
			},
			setupMocks: func(t *testing.T, fga *mocks.MockOpenFGAServiceClient, mgr *mocks.MockManager, storeIDGetter *mocks.MockStoreIDGetter, kcpHelper *mocks.MockKcpHelper) {
				scheme := getAPIExportPolicyTestScheme()

				// Provider cluster client
				providerClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(&kcpcorev1alpha1.LogicalCluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:        "cluster",
							Annotations: map[string]string{"kcp.io/cluster": "provider-cluster-id"},
						},
					}).
					Build()

				// Target workspace client with AccountInfo
				targetClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(&accountsv1alpha1.AccountInfo{
						ObjectMeta: metav1.ObjectMeta{
							Name: "account",
						},
						Spec: accountsv1alpha1.AccountInfoSpec{
							Account: accountsv1alpha1.AccountLocation{
								Name:            "acme-account",
								OriginClusterId: "acme-cluster-id",
								Type:            accountsv1alpha1.AccountTypeOrg,
							},
							Organization: accountsv1alpha1.AccountLocation{
								Name: "acme-org",
							},
						},
					}).
					Build()

				kcpHelper.EXPECT().NewForLogicalCluster(mock.MatchedBy(func(name logicalcluster.Name) bool {
					return name.String() == "root:providers:my-provider"
				})).Return(providerClient, nil)
				kcpHelper.EXPECT().NewForLogicalCluster(mock.MatchedBy(func(name logicalcluster.Name) bool {
					return name.String() == "root:orgs:acme"
				})).Return(targetClient, nil)

				storeIDGetter.EXPECT().Get(mock.Anything, "acme-org").Return("test-store-id", nil)

				// Expect FGA Write with Deletes for tuple deletion
				fga.EXPECT().Write(mock.Anything, mock.MatchedBy(func(req *openfgav1.WriteRequest) bool {
					if req.StoreId != "test-store-id" {
						return false
					}
					if req.Deletes == nil || len(req.Deletes.TupleKeys) != 1 {
						return false
					}
					tuple := req.Deletes.TupleKeys[0]
					return tuple.Object == "core_platform-mesh_io_account:acme-cluster-id/acme-account" &&
						tuple.Relation == "bind" &&
						tuple.User == "apis_kcp_io_apiexport:provider-cluster-id/my-export"
				}), mock.Anything).Return(&openfgav1.WriteResponse{}, nil)
			},
			cfg:         &config.Config{},
			expectError: false,
		},
		{
			name: "should delete FGA tuple with bind_inherited for wildcard expression",
			policy: &corev1alpha1.APIExportPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-policy",
				},
				Spec: corev1alpha1.APIExportPolicySpec{
					APIExportRef: corev1alpha1.APIExportRef{
						Name:        "my-export",
						ClusterPath: "root:providers:my-provider",
					},
					AllowPathExpressions: []string{"root:orgs:acme:*"},
				},
			},
			setupMocks: func(t *testing.T, fga *mocks.MockOpenFGAServiceClient, mgr *mocks.MockManager, storeIDGetter *mocks.MockStoreIDGetter, kcpHelper *mocks.MockKcpHelper) {
				scheme := getAPIExportPolicyTestScheme()

				// Provider cluster client
				providerClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(&kcpcorev1alpha1.LogicalCluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:        "cluster",
							Annotations: map[string]string{"kcp.io/cluster": "provider-cluster-id"},
						},
					}).
					Build()

				// Target workspace client with AccountInfo
				targetClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(&accountsv1alpha1.AccountInfo{
						ObjectMeta: metav1.ObjectMeta{
							Name: "account",
						},
						Spec: accountsv1alpha1.AccountInfoSpec{
							Account: accountsv1alpha1.AccountLocation{
								Name:            "acme-account",
								OriginClusterId: "acme-cluster-id",
								Type:            accountsv1alpha1.AccountTypeOrg,
							},
							Organization: accountsv1alpha1.AccountLocation{
								Name: "acme-org",
							},
						},
					}).
					Build()

				kcpHelper.EXPECT().NewForLogicalCluster(mock.MatchedBy(func(name logicalcluster.Name) bool {
					return name.String() == "root:providers:my-provider"
				})).Return(providerClient, nil)
				kcpHelper.EXPECT().NewForLogicalCluster(mock.MatchedBy(func(name logicalcluster.Name) bool {
					return name.String() == "root:orgs:acme"
				})).Return(targetClient, nil)

				storeIDGetter.EXPECT().Get(mock.Anything, "acme-org").Return("test-store-id", nil)

				// Expect FGA Write with Deletes using bind_inherited relation
				fga.EXPECT().Write(mock.Anything, mock.MatchedBy(func(req *openfgav1.WriteRequest) bool {
					if req.StoreId != "test-store-id" {
						return false
					}
					if req.Deletes == nil || len(req.Deletes.TupleKeys) != 1 {
						return false
					}
					tuple := req.Deletes.TupleKeys[0]
					return tuple.Object == "core_platform-mesh_io_account:acme-cluster-id/acme-account" &&
						tuple.Relation == "bind_inherited" &&
						tuple.User == "apis_kcp_io_apiexport:provider-cluster-id/my-export"
				}), mock.Anything).Return(&openfgav1.WriteResponse{}, nil)
			},
			cfg:         &config.Config{},
			expectError: false,
		},
		{
			name: "should delete FGA tuples for all orgs when root:orgs:* expression is used",
			policy: &corev1alpha1.APIExportPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-policy",
				},
				Spec: corev1alpha1.APIExportPolicySpec{
					APIExportRef: corev1alpha1.APIExportRef{
						Name:        "my-export",
						ClusterPath: "root:providers:my-provider",
					},
					AllowPathExpressions: []string{"root:orgs:*"},
				},
			},
			setupMocks: func(t *testing.T, fga *mocks.MockOpenFGAServiceClient, mgr *mocks.MockManager, storeIDGetter *mocks.MockStoreIDGetter, kcpHelper *mocks.MockKcpHelper) {
				scheme := getAPIExportPolicyTestScheme()

				// Provider cluster client
				providerClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(&kcpcorev1alpha1.LogicalCluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:        "cluster",
							Annotations: map[string]string{"kcp.io/cluster": "provider-cluster-id"},
						},
					}).
					Build()

				// All client with AccountInfo list
				allClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithLists(&accountsv1alpha1.AccountInfoList{
						Items: []accountsv1alpha1.AccountInfo{
							{
								ObjectMeta: metav1.ObjectMeta{
									Name: "account-1",
								},
								Spec: accountsv1alpha1.AccountInfoSpec{
									Account: accountsv1alpha1.AccountLocation{
										Name:            "org1-account",
										OriginClusterId: "org1-cluster-id",
										Type:            accountsv1alpha1.AccountTypeOrg,
									},
									Organization: accountsv1alpha1.AccountLocation{
										Name: "org1",
									},
								},
							},
							{
								ObjectMeta: metav1.ObjectMeta{
									Name: "account-2",
								},
								Spec: accountsv1alpha1.AccountInfoSpec{
									Account: accountsv1alpha1.AccountLocation{
										Name:            "org2-account",
										OriginClusterId: "org2-cluster-id",
										Type:            accountsv1alpha1.AccountTypeOrg,
									},
									Organization: accountsv1alpha1.AccountLocation{
										Name: "org2",
									},
								},
							},
						},
					}).
					Build()

				kcpHelper.EXPECT().NewForLogicalCluster(mock.MatchedBy(func(name logicalcluster.Name) bool {
					return name.String() == "root:providers:my-provider"
				})).Return(providerClient, nil)
				kcpHelper.EXPECT().GetAllClient(mock.Anything, mock.Anything).Return(allClient, nil)

				storeIDGetter.EXPECT().Get(mock.Anything, "org1").Return("store-id-org1", nil)
				storeIDGetter.EXPECT().Get(mock.Anything, "org2").Return("store-id-org2", nil)

				// Expect FGA Write with Deletes for org1 with bind_inherited relation
				fga.EXPECT().Write(mock.Anything, mock.MatchedBy(func(req *openfgav1.WriteRequest) bool {
					if req.StoreId != "store-id-org1" {
						return false
					}
					if req.Deletes == nil || len(req.Deletes.TupleKeys) != 1 {
						return false
					}
					tuple := req.Deletes.TupleKeys[0]
					return tuple.Object == "core_platform-mesh_io_account:org1-cluster-id/org1-account" &&
						tuple.Relation == "bind_inherited" &&
						tuple.User == "apis_kcp_io_apiexport:provider-cluster-id/my-export"
				}), mock.Anything).Return(&openfgav1.WriteResponse{}, nil)

				// Expect FGA Write with Deletes for org2 with bind_inherited relation
				fga.EXPECT().Write(mock.Anything, mock.MatchedBy(func(req *openfgav1.WriteRequest) bool {
					if req.StoreId != "store-id-org2" {
						return false
					}
					if req.Deletes == nil || len(req.Deletes.TupleKeys) != 1 {
						return false
					}
					tuple := req.Deletes.TupleKeys[0]
					return tuple.Object == "core_platform-mesh_io_account:org2-cluster-id/org2-account" &&
						tuple.Relation == "bind_inherited" &&
						tuple.User == "apis_kcp_io_apiexport:provider-cluster-id/my-export"
				}), mock.Anything).Return(&openfgav1.WriteResponse{}, nil)
			},
			cfg:         &config.Config{},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fga := mocks.NewMockOpenFGAServiceClient(t)
			mgr := mocks.NewMockManager(t)
			storeIDGetter := mocks.NewMockStoreIDGetter(t)
			kcpHelper := mocks.NewMockKcpHelper(t)

			if tt.setupMocks != nil {
				tt.setupMocks(t, fga, mgr, storeIDGetter, kcpHelper)
			}

			l := testlogger.New()
			ctx := l.WithContext(context.Background())

			sub := subroutine.NewAPIExportPolicySubroutine(fga, mgr, tt.cfg, storeIDGetter, kcpHelper)

			_, err := sub.Finalize(ctx, tt.policy)

			if tt.expectError {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

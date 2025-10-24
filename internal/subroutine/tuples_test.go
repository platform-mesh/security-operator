package subroutine_test

import (
	"context"
	"errors"
	"testing"

	kcpcorev1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"
	"github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/subroutine"
	"github.com/platform-mesh/security-operator/internal/subroutine/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
)

func TestTupleGetName(t *testing.T) {
	subroutine := subroutine.NewTupleSubroutine(nil, nil)
	assert.Equal(t, "TupleSubroutine", subroutine.GetName())
}

func TestTupleFinalizers(t *testing.T) {
	subroutine := subroutine.NewTupleSubroutine(nil, nil)
	assert.Equal(t, []string{"core.platform-mesh.io/fga-tuples"}, subroutine.Finalizers(nil))
}

func TestTupleProcessWithStore(t *testing.T) {
	tests := []struct {
		name        string
		store       *v1alpha1.Store
		fgaMocks    func(*mocks.MockOpenFGAServiceClient)
		k8sMocks    func(*mocks.MockClient)
		mgrMocks    func(*mocks.MockManager)
		expectError bool
	}{
		{
			name: "should process and add tuples to the store",
			store: &v1alpha1.Store{
				Spec: v1alpha1.StoreSpec{
					Tuples: []v1alpha1.Tuple{
						{
							Object:   "foo",
							Relation: "bar",
							User:     "user1",
						},
						{
							Object:   "foo",
							Relation: "bar",
							User:     "user2",
						},
						{
							Object:   "foo",
							Relation: "bar",
							User:     "user3",
						},
					},
				},
				Status: v1alpha1.StoreStatus{
					StoreID:              "store-id",
					AuthorizationModelID: "auth-model-id",
					ManagedTuples: []v1alpha1.Tuple{
						{
							Object:   "foo",
							Relation: "bar",
							User:     "user1",
						},
					},
				},
			},
			fgaMocks: func(fga *mocks.MockOpenFGAServiceClient) {
				fga.EXPECT().Write(mock.Anything, mock.Anything).Return(nil, nil).Times(3)
			},
		},
		{
			name: "should process and add/remove tuples to the store",
			store: &v1alpha1.Store{
				Spec: v1alpha1.StoreSpec{
					Tuples: []v1alpha1.Tuple{
						{
							Object:   "foo",
							Relation: "bar",
							User:     "user1",
						},
						{
							Object:   "foo",
							Relation: "bar",
							User:     "user2",
						},
						{
							Object:   "foo",
							Relation: "bar",
							User:     "user3",
						},
					},
				},
				Status: v1alpha1.StoreStatus{
					StoreID:              "store-id",
					AuthorizationModelID: "auth-model-id",
					ManagedTuples: []v1alpha1.Tuple{
						{
							Object:   "foo",
							Relation: "bar",
							User:     "user4",
						},
					},
				},
			},
			fgaMocks: func(fga *mocks.MockOpenFGAServiceClient) {
				// write calls
				fga.EXPECT().Write(mock.Anything, mock.Anything).Return(nil, nil).Times(3)

				// delete call
				fga.EXPECT().Write(mock.Anything, mock.Anything).Return(nil, nil)
			},
		},
		{
			name: "should stop processing if an error occurs",
			store: &v1alpha1.Store{
				Spec: v1alpha1.StoreSpec{
					Tuples: []v1alpha1.Tuple{
						{
							Object:   "foo",
							Relation: "bar",
							User:     "user1",
						},
						{
							Object:   "foo",
							Relation: "bar",
							User:     "user2",
						},
						{
							Object:   "foo",
							Relation: "bar",
							User:     "user3",
						},
					},
				},
				Status: v1alpha1.StoreStatus{
					StoreID:              "store-id",
					AuthorizationModelID: "auth-model-id",
					ManagedTuples: []v1alpha1.Tuple{
						{
							Object:   "foo",
							Relation: "bar",
							User:     "user1",
						},
					},
				},
			},
			fgaMocks: func(fga *mocks.MockOpenFGAServiceClient) {
				fga.EXPECT().Write(mock.Anything, mock.Anything).Return(nil, errors.New("an error"))
			},
			expectError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fga := mocks.NewMockOpenFGAServiceClient(t)
			if test.fgaMocks != nil {
				test.fgaMocks(fga)
			}

			manager := mocks.NewMockManager(t)
			if test.mgrMocks != nil {
				test.mgrMocks(manager)
			}

			subroutine := subroutine.NewTupleSubroutine(fga, manager)

			_, err := subroutine.Process(context.Background(), test.store)
			if test.expectError {
				assert.Error(t, err.Err())
			} else {
				assert.Nil(t, err)
				assert.Equal(t, test.store.Status.ManagedTuples, test.store.Spec.Tuples)
			}

		})
	}
}

func TestTupleProcessWithAuthorizationModel(t *testing.T) {
	tests := []struct {
		name        string
		store       *v1alpha1.AuthorizationModel
		fgaMocks    func(*mocks.MockOpenFGAServiceClient)
		k8sMocks    func(*mocks.MockClient)
		mgrMocks    func(*mocks.MockManager)
		expectError bool
	}{
		{
			name: "should process and add tuples to the authorization model",
			store: &v1alpha1.AuthorizationModel{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						v1alpha1.StoreRefLabelKey: "store",
					},
				},
				Spec: v1alpha1.AuthorizationModelSpec{
					Tuples: []v1alpha1.Tuple{
						{
							Object:   "foo",
							Relation: "bar",
							User:     "user1",
						},
						{
							Object:   "foo",
							Relation: "bar",
							User:     "user2",
						},
						{
							Object:   "foo",
							Relation: "bar",
							User:     "user3",
						},
					},
				},
			},
			fgaMocks: func(fga *mocks.MockOpenFGAServiceClient) {
				fga.EXPECT().Write(mock.Anything, mock.Anything).Return(nil, nil).Times(3)
			},
			k8sMocks: func(k8s *mocks.MockClient) {
				mockLogicalClusterGet(k8s)
				k8s.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
					store := o.(*v1alpha1.Store)
					*store = v1alpha1.Store{
						Status: v1alpha1.StoreStatus{
							StoreID:              "store-id",
							AuthorizationModelID: "auth-model-id",
						},
					}
					return nil
				})
			},
		},
		{
			name: "should process and add/remove tuples to the authorization model",
			store: &v1alpha1.AuthorizationModel{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						v1alpha1.StoreRefLabelKey: "store",
					},
				},
				Spec: v1alpha1.AuthorizationModelSpec{
					Tuples: []v1alpha1.Tuple{
						{
							Object:   "foo",
							Relation: "bar",
							User:     "user1",
						},
						{
							Object:   "foo",
							Relation: "bar",
							User:     "user2",
						},
						{
							Object:   "foo",
							Relation: "bar",
							User:     "user3",
						},
					},
				},
				Status: v1alpha1.AuthorizationModelStatus{
					ManagedTuples: []v1alpha1.Tuple{
						{
							Object:   "foo",
							Relation: "bar",
							User:     "user4",
						},
					},
				},
			},
			fgaMocks: func(fga *mocks.MockOpenFGAServiceClient) {
				// write calls
				fga.EXPECT().Write(mock.Anything, mock.Anything).Return(nil, nil).Times(3)

				// delete call
				fga.EXPECT().Write(mock.Anything, mock.Anything).Return(nil, nil)
			},
			k8sMocks: func(k8s *mocks.MockClient) {
				mockLogicalClusterGet(k8s)
				k8s.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
					store := o.(*v1alpha1.Store)
					*store = v1alpha1.Store{
						Status: v1alpha1.StoreStatus{
							StoreID:              "store-id",
							AuthorizationModelID: "auth-model-id",
						},
					}
					return nil
				})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fga := mocks.NewMockOpenFGAServiceClient(t)
			if test.fgaMocks != nil {
				test.fgaMocks(fga)
			}

			manager := mocks.NewMockManager(t)
			cluster := mocks.NewMockCluster(t)
			k8sClient := mocks.NewMockClient(t)
			if test.k8sMocks != nil {
				test.k8sMocks(k8sClient)
				manager.EXPECT().ClusterFromContext(mock.Anything).Return(cluster, nil)
				manager.EXPECT().GetCluster(mock.Anything, mock.Anything).Return(cluster, nil).Maybe()
				cluster.EXPECT().GetClient().Return(k8sClient).Maybe()
			}

			subroutine := subroutine.NewTupleSubroutine(fga, manager)

			ctx := mccontext.WithCluster(context.Background(), string(logicalcluster.Name("a")))

			_, err := subroutine.Process(ctx, test.store)
			if test.expectError {
				assert.Error(t, err.Err())
			} else {
				assert.Nil(t, err)
				assert.Equal(t, test.store.Status.ManagedTuples, test.store.Spec.Tuples)
			}

		})
	}
}

func TestTupleFinalizationWithAuthorizationModel(t *testing.T) {
	tests := []struct {
		name        string
		store       *v1alpha1.AuthorizationModel
		fgaMocks    func(*mocks.MockOpenFGAServiceClient)
		k8sMocks    func(*mocks.MockClient)
		mgrMocks    func(*mocks.MockManager)
		expectError bool
	}{
		{
			name: "should finalize the authorization model",
			store: &v1alpha1.AuthorizationModel{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						v1alpha1.StoreRefLabelKey: "store",
					},
				},
				Status: v1alpha1.AuthorizationModelStatus{
					ManagedTuples: []v1alpha1.Tuple{
						{
							Object:   "foo",
							Relation: "bar",
							User:     "user4",
						},
					},
				},
			},
			fgaMocks: func(fga *mocks.MockOpenFGAServiceClient) {
				// delete call
				fga.EXPECT().Write(mock.Anything, mock.Anything).Return(nil, nil)
			},
			k8sMocks: func(k8s *mocks.MockClient) {
				mockLogicalClusterGet(k8s)
				k8s.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
					store := o.(*v1alpha1.Store)
					*store = v1alpha1.Store{
						Status: v1alpha1.StoreStatus{
							StoreID:              "store-id",
							AuthorizationModelID: "auth-model-id",
						},
					}
					return nil
				})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fga := mocks.NewMockOpenFGAServiceClient(t)
			if test.fgaMocks != nil {
				test.fgaMocks(fga)
			}

			manager := mocks.NewMockManager(t)
			cluster := mocks.NewMockCluster(t)
			k8sClient := mocks.NewMockClient(t)
			if test.k8sMocks != nil {
				test.k8sMocks(k8sClient)
				manager.EXPECT().ClusterFromContext(mock.Anything).Return(cluster, nil)
				manager.EXPECT().GetCluster(mock.Anything, mock.Anything).Return(cluster, nil).Maybe()
				cluster.EXPECT().GetClient().Return(k8sClient).Maybe()
			}

			subroutine := subroutine.NewTupleSubroutine(fga, manager)

			ctx := mccontext.WithCluster(context.Background(), string(logicalcluster.Name("a")))

			_, err := subroutine.Finalize(ctx, test.store)
			if test.expectError {
				assert.Error(t, err.Err())
			} else {
				assert.Nil(t, err)
				assert.Empty(t, test.store.Status.ManagedTuples)
			}

		})
	}
}

func TestTupleFinalizationWithStore(t *testing.T) {
	tests := []struct {
		name        string
		store       *v1alpha1.Store
		fgaMocks    func(*mocks.MockOpenFGAServiceClient)
		k8sMocks    func(*mocks.MockClient)
		mgrMocks    func(*mocks.MockManager)
		expectError bool
	}{
		{
			name: "should finalize the authorization model",
			store: &v1alpha1.Store{
				Status: v1alpha1.StoreStatus{
					ManagedTuples: []v1alpha1.Tuple{
						{
							Object:   "foo",
							Relation: "bar",
							User:     "user4",
						},
					},
				},
			},
			fgaMocks: func(fga *mocks.MockOpenFGAServiceClient) {
				// delete call
				fga.EXPECT().Write(mock.Anything, mock.Anything).Return(nil, nil)
			},
		},
		{
			name: "should stop finalizing the authorization model if an error occurs",
			store: &v1alpha1.Store{
				Status: v1alpha1.StoreStatus{
					ManagedTuples: []v1alpha1.Tuple{
						{
							Object:   "foo",
							Relation: "bar",
							User:     "user4",
						},
					},
				},
			},
			fgaMocks: func(fga *mocks.MockOpenFGAServiceClient) {
				// delete call
				fga.EXPECT().Write(mock.Anything, mock.Anything).Return(nil, errors.New("an error"))
			},
			expectError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fga := mocks.NewMockOpenFGAServiceClient(t)
			if test.fgaMocks != nil {
				test.fgaMocks(fga)
			}

			manager := mocks.NewMockManager(t)
			if test.mgrMocks != nil {
				test.mgrMocks(manager)
			}

			subroutine := subroutine.NewTupleSubroutine(fga, manager)

			_, err := subroutine.Finalize(context.Background(), test.store)
			if test.expectError {
				assert.Error(t, err.Err())
			} else {
				assert.Nil(t, err)
				assert.Empty(t, test.store.Status.ManagedTuples)
			}

		})
	}
}

func TestTupleFinalizationWithAuthorizationModel_Errors(t *testing.T) {
	t.Run("cluster from context error", func(t *testing.T) {
		fga := mocks.NewMockOpenFGAServiceClient(t)
		manager := mocks.NewMockManager(t)
		// Simulate failure to get cluster from context
		manager.EXPECT().ClusterFromContext(mock.Anything).Return(nil, errors.New("cluster ctx error"))

		sub := subroutine.NewTupleSubroutine(fga, manager)
		am := &v1alpha1.AuthorizationModel{}

		ctx := mccontext.WithCluster(context.Background(), string(logicalcluster.Name("a")))
		_, opErr := sub.Finalize(ctx, am)
		assert.NotNil(t, opErr)
	})

	t.Run("logicalcluster get error", func(t *testing.T) {
		fga := mocks.NewMockOpenFGAServiceClient(t)
		manager := mocks.NewMockManager(t)
		cluster := mocks.NewMockCluster(t)
		k8sClient := mocks.NewMockClient(t)

		manager.EXPECT().ClusterFromContext(mock.Anything).Return(cluster, nil)
		cluster.EXPECT().GetClient().Return(k8sClient)
		// First Get tries to fetch the LogicalCluster named "cluster"
		k8sClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(errors.New("get lc error")).Once()

		sub := subroutine.NewTupleSubroutine(fga, manager)
		am := &v1alpha1.AuthorizationModel{}
		ctx := mccontext.WithCluster(context.Background(), string(logicalcluster.Name("a")))
		_, opErr := sub.Finalize(ctx, am)
		assert.NotNil(t, opErr)
	})

	t.Run("store get error", func(t *testing.T) {
		fga := mocks.NewMockOpenFGAServiceClient(t)
		manager := mocks.NewMockManager(t)
		cluster := mocks.NewMockCluster(t)
		k8sClient := mocks.NewMockClient(t)

		// AuthorizationModel with StoreRef set so we try to lookup the store
		am := &v1alpha1.AuthorizationModel{}
		am.Spec.StoreRef.Name = "store"
		am.Spec.StoreRef.Path = "path"

		manager.EXPECT().ClusterFromContext(mock.Anything).Return(cluster, nil)
		cluster.EXPECT().GetClient().Return(k8sClient)
		// First Get returns LogicalCluster successfully
		k8sClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, _ client.ObjectKey, o client.Object, _ ...client.GetOption) error {
			// Populate lc annotation used in code path
			if lc, ok := o.(*kcpcorev1alpha1.LogicalCluster); ok {
				lc.Annotations = map[string]string{"kcp.io/cluster": "path"}
			}
			return nil
		}).Once()

		// Second: attempt to fetch Store from storeCtx must fail
		k8sClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "store"}, mock.Anything).Return(errors.New("get store error")).Once()

		sub := subroutine.NewTupleSubroutine(fga, manager)
		ctx := mccontext.WithCluster(context.Background(), string(logicalcluster.Name("a")))
		_, opErr := sub.Finalize(ctx, am)
		assert.NotNil(t, opErr)
	})
}

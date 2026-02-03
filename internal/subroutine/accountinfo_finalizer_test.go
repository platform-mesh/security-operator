package subroutine_test

import (
	"context"
	"testing"
	"time"

	accountv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/subroutine"
	"github.com/platform-mesh/security-operator/internal/subroutine/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kcpapisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
)

func TestAccountInfoFinalizerSubroutine_GetName(t *testing.T) {
	sub := subroutine.NewAccountInfoFinalizerSubroutine(nil)
	assert.Equal(t, "AccountInfoFinalizer", sub.GetName())
}

func TestAccountInfoFinalizerSubroutine_Finalizers(t *testing.T) {
	sub := subroutine.NewAccountInfoFinalizerSubroutine(nil)
	finalizers := sub.Finalizers(nil)
	assert.Equal(t, []string{"security.platform-mesh.io/accountinfo-finalizer"}, finalizers)
}

func TestAccountInfoFinalizerSubroutine_Process(t *testing.T) {
	sub := subroutine.NewAccountInfoFinalizerSubroutine(nil)
	result, err := sub.Process(context.Background(), &accountv1alpha1.AccountInfo{})
	assert.Nil(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestAccountInfoFinalizerSubroutine_Finalize(t *testing.T) {
	tests := []struct {
		name           string
		mockSetup      func(*mocks.MockManager, *mocks.MockCluster, *mocks.MockClient)
		expectError    bool
		expectedResult ctrl.Result
	}{
		{
			name: "error on ClusterFromContext",
			mockSetup: func(manager *mocks.MockManager, cluster *mocks.MockCluster, kcpClient *mocks.MockClient) {
				manager.EXPECT().ClusterFromContext(mock.Anything).Return(nil, assert.AnError)
			},
			expectError: true,
		},
		{
			name: "error on List APIBindings",
			mockSetup: func(manager *mocks.MockManager, cluster *mocks.MockCluster, kcpClient *mocks.MockClient) {
				manager.EXPECT().ClusterFromContext(mock.Anything).Return(cluster, nil)
				cluster.EXPECT().GetClient().Return(kcpClient)
				kcpClient.EXPECT().List(mock.Anything, mock.Anything).Return(assert.AnError)
			},
			expectError: true,
		},
		{
			name: "no APIBindings exist - allow deletion",
			mockSetup: func(manager *mocks.MockManager, cluster *mocks.MockCluster, kcpClient *mocks.MockClient) {
				manager.EXPECT().ClusterFromContext(mock.Anything).Return(cluster, nil)
				cluster.EXPECT().GetClient().Return(kcpClient)
				kcpClient.EXPECT().List(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, ol client.ObjectList, lo ...client.ListOption) error {
					list := ol.(*kcpapisv1alpha2.APIBindingList)
					list.Items = []kcpapisv1alpha2.APIBinding{}
					return nil
				})
			},
			expectError:    false,
			expectedResult: ctrl.Result{},
		},
		{
			name: "APIBindings exist without finalizer - allow deletion",
			mockSetup: func(manager *mocks.MockManager, cluster *mocks.MockCluster, kcpClient *mocks.MockClient) {
				manager.EXPECT().ClusterFromContext(mock.Anything).Return(cluster, nil)
				cluster.EXPECT().GetClient().Return(kcpClient)
				kcpClient.EXPECT().List(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, ol client.ObjectList, lo ...client.ListOption) error {
					list := ol.(*kcpapisv1alpha2.APIBindingList)
					list.Items = []kcpapisv1alpha2.APIBinding{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:       "binding1",
								Finalizers: []string{"other-finalizer"},
							},
						},
					}
					return nil
				})
			},
			expectError:    false,
			expectedResult: ctrl.Result{},
		},
		{
			name: "APIBinding exists with apibinding-finalizer - requeue",
			mockSetup: func(manager *mocks.MockManager, cluster *mocks.MockCluster, kcpClient *mocks.MockClient) {
				manager.EXPECT().ClusterFromContext(mock.Anything).Return(cluster, nil)
				cluster.EXPECT().GetClient().Return(kcpClient)
				kcpClient.EXPECT().List(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, ol client.ObjectList, lo ...client.ListOption) error {
					list := ol.(*kcpapisv1alpha2.APIBindingList)
					list.Items = []kcpapisv1alpha2.APIBinding{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:       "binding1",
								Finalizers: []string{"core.platform-mesh.io/apibinding-finalizer"},
							},
						},
					}
					return nil
				})
			},
			expectError:    false,
			expectedResult: ctrl.Result{RequeueAfter: 5 * time.Second},
		},
		{
			name: "multiple APIBindings - one with finalizer - requeue",
			mockSetup: func(manager *mocks.MockManager, cluster *mocks.MockCluster, kcpClient *mocks.MockClient) {
				manager.EXPECT().ClusterFromContext(mock.Anything).Return(cluster, nil)
				cluster.EXPECT().GetClient().Return(kcpClient)
				kcpClient.EXPECT().List(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, ol client.ObjectList, lo ...client.ListOption) error {
					list := ol.(*kcpapisv1alpha2.APIBindingList)
					list.Items = []kcpapisv1alpha2.APIBinding{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:       "binding1",
								Finalizers: []string{"other-finalizer"},
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:       "binding2",
								Finalizers: []string{"core.platform-mesh.io/apibinding-finalizer"},
							},
						},
					}
					return nil
				})
			},
			expectError:    false,
			expectedResult: ctrl.Result{RequeueAfter: 5 * time.Second},
		},
		{
			name: "multiple APIBindings - none with target finalizer - allow deletion",
			mockSetup: func(manager *mocks.MockManager, cluster *mocks.MockCluster, kcpClient *mocks.MockClient) {
				manager.EXPECT().ClusterFromContext(mock.Anything).Return(cluster, nil)
				cluster.EXPECT().GetClient().Return(kcpClient)
				kcpClient.EXPECT().List(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, ol client.ObjectList, lo ...client.ListOption) error {
					list := ol.(*kcpapisv1alpha2.APIBindingList)
					list.Items = []kcpapisv1alpha2.APIBinding{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:       "binding1",
								Finalizers: []string{"other-finalizer-1"},
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:       "binding2",
								Finalizers: []string{"other-finalizer-2"},
							},
						},
					}
					return nil
				})
			},
			expectError:    false,
			expectedResult: ctrl.Result{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := mocks.NewMockManager(t)
			cluster := mocks.NewMockCluster(t)
			kcpClient := mocks.NewMockClient(t)

			if test.mockSetup != nil {
				test.mockSetup(manager, cluster, kcpClient)
			}

			sub := subroutine.NewAccountInfoFinalizerSubroutine(manager)
			result, err := sub.Finalize(context.Background(), &accountv1alpha1.AccountInfo{})

			if test.expectError {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, test.expectedResult, result)
			}
		})
	}
}

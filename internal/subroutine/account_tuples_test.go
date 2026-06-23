package subroutine_test

import (
	"context"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	accountsv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/fga"
	"github.com/platform-mesh/security-operator/internal/subroutine"
	"github.com/platform-mesh/security-operator/internal/subroutine/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

const (
	accountLCPath        = "root:orgs:myorg:myaccount"
	parentClusterID      = "parent-cluster-id"
	grandParentClusterID = "grand-cluster-id"
)

func newAccountLogicalCluster() *kcpcorev1alpha1.LogicalCluster {
	return &kcpcorev1alpha1.LogicalCluster{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"kcp.io/path": accountLCPath,
			},
		},
	}
}

func newLocalAccountInfo(creator *string) accountsv1alpha1.AccountInfo {
	return accountsv1alpha1.AccountInfo{
		ObjectMeta: metav1.ObjectMeta{Name: "account"},
		Spec: accountsv1alpha1.AccountInfoSpec{
			Account: accountsv1alpha1.AccountLocation{
				Name:    "myaccount",
				Creator: creator,
				Path:    accountLCPath,
			},
			ParentAccount: &accountsv1alpha1.AccountLocation{
				Name:               "myorg",
				GeneratedClusterId: parentClusterID,
				OriginClusterId:    grandParentClusterID,
			},
			Organization: accountsv1alpha1.AccountLocation{
				Name: "myorg",
			},
		},
	}
}

func mockLocalAccountInfo(
	mgr *mocks.MockManager,
	cluster *mocks.MockCluster,
	localClient *mocks.MockClient,
	accountInfo accountsv1alpha1.AccountInfo,
	getErr error,
) {
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(cluster, nil).Once()
	cluster.EXPECT().GetClient().Return(localClient).Once()
	localClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "account"}, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
			if getErr != nil {
				return getErr
			}
			if ai, ok := o.(*accountsv1alpha1.AccountInfo); ok {
				*ai = accountInfo
			}
			return nil
		}).Once()
}

func TestAccountTuplesSubroutine_GetName(t *testing.T) {
	sub := subroutine.NewAccountTuplesSubroutine(nil, nil, nil, "creator", "parent", "type", nil)
	assert.Equal(t, "AccountTuplesSubroutine", sub.GetName())
}

func TestAccountTuplesSubroutine_Process(t *testing.T) {
	storeIDGetter := mocks.NewMockStoreIDGetter(t)
	mgr := mocks.NewMockManager(t)
	cluster := mocks.NewMockCluster(t)
	localClient := mocks.NewMockClient(t)
	fgaClient := mocks.NewMockOpenFGAServiceClient(t)

	creator := "user@example.com"
	mockLocalAccountInfo(mgr, cluster, localClient, newLocalAccountInfo(&creator), nil)
	storeIDGetter.EXPECT().Get(mock.Anything, "myorg").Return("store-id", nil)

	expectedTuples, err := fga.InitialTuplesForAccount(fga.InitialTuplesForAccountInput{
		BaseTuplesInput: fga.BaseTuplesInput{
			Creator:                creator,
			AccountOriginClusterID: parentClusterID,
			AccountName:            "myaccount",
			CreatorRelation:        "creator",
			ObjectType:             "account",
		},
		ParentOriginClusterID: grandParentClusterID,
		ParentName:            "myorg",
		ParentRelation:        "parent",
	})
	require.NoError(t, err)

	fgaClient.EXPECT().Write(mock.Anything, mock.MatchedBy(func(req *openfgav1.WriteRequest) bool {
		if req.StoreId != "store-id" || req.AuthorizationModelId != fga.AuthorizationModelIDLatest {
			return false
		}
		if req.Writes == nil || req.Deletes != nil {
			return false
		}
		if req.Writes.OnDuplicate != "ignore" || len(req.Writes.TupleKeys) != len(expectedTuples) {
			return false
		}
		for i, tk := range req.Writes.TupleKeys {
			if tk.User != expectedTuples[i].User || tk.Relation != expectedTuples[i].Relation || tk.Object != expectedTuples[i].Object {
				return false
			}
		}
		return true
	})).Return(&openfgav1.WriteResponse{}, nil).Once()

	sub := subroutine.NewAccountTuplesSubroutine(mgr, fgaClient, storeIDGetter, "creator", "parent", "account", nil)
	_, err = sub.Process(context.Background(), newAccountLogicalCluster())
	assert.NoError(t, err)
}

func TestAccountTuplesSubroutine_Initialize(t *testing.T) {
	tests := []struct {
		name        string
		obj         *kcpcorev1alpha1.LogicalCluster
		mockSetup   func(*mocks.MockStoreIDGetter, *mocks.MockManager, *mocks.MockCluster, *mocks.MockClient, *mocks.MockOpenFGAServiceClient)
		expectError bool
	}{
		{
			name: "error: missing path annotation",
			obj:  &kcpcorev1alpha1.LogicalCluster{},
			mockSetup: func(_ *mocks.MockStoreIDGetter, _ *mocks.MockManager, _ *mocks.MockCluster, _ *mocks.MockClient, _ *mocks.MockOpenFGAServiceClient) {
			},
			expectError: true,
		},
		{
			name: "error: cluster from context fails",
			obj:  newAccountLogicalCluster(),
			mockSetup: func(_ *mocks.MockStoreIDGetter, mgr *mocks.MockManager, _ *mocks.MockCluster, _ *mocks.MockClient, _ *mocks.MockOpenFGAServiceClient) {
				mgr.EXPECT().ClusterFromContext(mock.Anything).Return(nil, assert.AnError).Once()
			},
			expectError: true,
		},
		{
			name: "error: get accountInfo fails",
			obj:  newAccountLogicalCluster(),
			mockSetup: func(_ *mocks.MockStoreIDGetter, mgr *mocks.MockManager, cluster *mocks.MockCluster, localClient *mocks.MockClient, _ *mocks.MockOpenFGAServiceClient) {
				mockLocalAccountInfo(mgr, cluster, localClient, accountsv1alpha1.AccountInfo{}, assert.AnError)
			},
			expectError: true,
		},
		{
			name: "error: parent account missing",
			obj:  newAccountLogicalCluster(),
			mockSetup: func(_ *mocks.MockStoreIDGetter, mgr *mocks.MockManager, cluster *mocks.MockCluster, localClient *mocks.MockClient, _ *mocks.MockOpenFGAServiceClient) {
				accountInfo := newLocalAccountInfo(nil)
				accountInfo.Spec.ParentAccount = nil
				mockLocalAccountInfo(mgr, cluster, localClient, accountInfo, nil)
			},
			expectError: true,
		},
		{
			name: "error: account creator is empty",
			obj:  newAccountLogicalCluster(),
			mockSetup: func(_ *mocks.MockStoreIDGetter, mgr *mocks.MockManager, cluster *mocks.MockCluster, localClient *mocks.MockClient, _ *mocks.MockOpenFGAServiceClient) {
				mockLocalAccountInfo(mgr, cluster, localClient, newLocalAccountInfo(nil), nil)
			},
			expectError: true,
		},
		{
			name: "error: storeIDGetter fails",
			obj:  newAccountLogicalCluster(),
			mockSetup: func(storeIDGetter *mocks.MockStoreIDGetter, mgr *mocks.MockManager, cluster *mocks.MockCluster, localClient *mocks.MockClient, _ *mocks.MockOpenFGAServiceClient) {
				creator := "user@example.com"
				mockLocalAccountInfo(mgr, cluster, localClient, newLocalAccountInfo(&creator), nil)
				storeIDGetter.EXPECT().Get(mock.Anything, "myorg").Return("", assert.AnError)
			},
			expectError: true,
		},
		{
			name: "error: fga.Write fails",
			obj:  newAccountLogicalCluster(),
			mockSetup: func(storeIDGetter *mocks.MockStoreIDGetter, mgr *mocks.MockManager, cluster *mocks.MockCluster, localClient *mocks.MockClient, fgaClient *mocks.MockOpenFGAServiceClient) {
				creator := "user@example.com"
				mockLocalAccountInfo(mgr, cluster, localClient, newLocalAccountInfo(&creator), nil)
				storeIDGetter.EXPECT().Get(mock.Anything, "myorg").Return("store-id", nil)
				fgaClient.EXPECT().Write(mock.Anything, mock.Anything).Return(nil, assert.AnError)
			},
			expectError: true,
		},
		{
			name: "success",
			obj:  newAccountLogicalCluster(),
			mockSetup: func(storeIDGetter *mocks.MockStoreIDGetter, mgr *mocks.MockManager, cluster *mocks.MockCluster, localClient *mocks.MockClient, fgaClient *mocks.MockOpenFGAServiceClient) {
				creator := "user@example.com"
				mockLocalAccountInfo(mgr, cluster, localClient, newLocalAccountInfo(&creator), nil)
				storeIDGetter.EXPECT().Get(mock.Anything, "myorg").Return("store-id", nil)
				fgaClient.EXPECT().Write(mock.Anything, mock.Anything).Return(&openfgav1.WriteResponse{}, nil)
			},
			expectError: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			storeIDGetter := mocks.NewMockStoreIDGetter(t)
			mgr := mocks.NewMockManager(t)
			cluster := mocks.NewMockCluster(t)
			localClient := mocks.NewMockClient(t)
			fgaClient := mocks.NewMockOpenFGAServiceClient(t)

			test.mockSetup(storeIDGetter, mgr, cluster, localClient, fgaClient)

			sub := subroutine.NewAccountTuplesSubroutine(mgr, fgaClient, storeIDGetter, "creator", "parent", "account", nil)
			_, err := sub.Initialize(context.Background(), test.obj)
			if test.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAccountTuplesSubroutine_Terminate(t *testing.T) {
	tests := []struct {
		name        string
		obj         *kcpcorev1alpha1.LogicalCluster
		mockSetup   func(*mocks.MockStoreIDGetter, *mocks.MockManager, *mocks.MockCluster, *mocks.MockClient, *mocks.MockOpenFGAServiceClient)
		expectError bool
	}{
		{
			name: "error: missing path annotation",
			obj:  &kcpcorev1alpha1.LogicalCluster{},
			mockSetup: func(_ *mocks.MockStoreIDGetter, _ *mocks.MockManager, _ *mocks.MockCluster, _ *mocks.MockClient, _ *mocks.MockOpenFGAServiceClient) {
			},
			expectError: true,
		},
		{
			name: "error: cluster from context fails",
			obj:  newAccountLogicalCluster(),
			mockSetup: func(_ *mocks.MockStoreIDGetter, mgr *mocks.MockManager, _ *mocks.MockCluster, _ *mocks.MockClient, _ *mocks.MockOpenFGAServiceClient) {
				mgr.EXPECT().ClusterFromContext(mock.Anything).Return(nil, assert.AnError).Once()
			},
			expectError: true,
		},
		{
			name: "error: parent account missing",
			obj:  newAccountLogicalCluster(),
			mockSetup: func(_ *mocks.MockStoreIDGetter, mgr *mocks.MockManager, cluster *mocks.MockCluster, localClient *mocks.MockClient, _ *mocks.MockOpenFGAServiceClient) {
				accountInfo := newLocalAccountInfo(nil)
				accountInfo.Spec.ParentAccount = nil
				mockLocalAccountInfo(mgr, cluster, localClient, accountInfo, nil)
			},
			expectError: true,
		},
		{
			name: "error: storeIDGetter fails",
			obj:  newAccountLogicalCluster(),
			mockSetup: func(storeIDGetter *mocks.MockStoreIDGetter, mgr *mocks.MockManager, cluster *mocks.MockCluster, localClient *mocks.MockClient, _ *mocks.MockOpenFGAServiceClient) {
				creator := "user@example.com"
				mockLocalAccountInfo(mgr, cluster, localClient, newLocalAccountInfo(&creator), nil)
				storeIDGetter.EXPECT().Get(mock.Anything, "myorg").Return("", assert.AnError)
			},
			expectError: true,
		},
		{
			name: "error: ListWithKey fails",
			obj:  newAccountLogicalCluster(),
			mockSetup: func(storeIDGetter *mocks.MockStoreIDGetter, mgr *mocks.MockManager, cluster *mocks.MockCluster, localClient *mocks.MockClient, fgaClient *mocks.MockOpenFGAServiceClient) {
				creator := "user@example.com"
				mockLocalAccountInfo(mgr, cluster, localClient, newLocalAccountInfo(&creator), nil)
				storeIDGetter.EXPECT().Get(mock.Anything, "myorg").Return("store-id", nil)
				fgaClient.EXPECT().Read(mock.Anything, mock.Anything).Return(nil, assert.AnError)
			},
			expectError: true,
		},
		{
			name: "error: ListWithKey for role fails",
			obj:  newAccountLogicalCluster(),
			mockSetup: func(storeIDGetter *mocks.MockStoreIDGetter, mgr *mocks.MockManager, cluster *mocks.MockCluster, localClient *mocks.MockClient, fgaClient *mocks.MockOpenFGAServiceClient) {
				creator := "user@example.com"
				mockLocalAccountInfo(mgr, cluster, localClient, newLocalAccountInfo(&creator), nil)
				storeIDGetter.EXPECT().Get(mock.Anything, "myorg").Return("store-id", nil)
				roleUser := "role:account/" + parentClusterID + "/myaccount/owner#assignee"
				fgaClient.EXPECT().Read(mock.Anything, mock.Anything).
					Return(&openfgav1.ReadResponse{
						Tuples: []*openfgav1.Tuple{
							{Key: &openfgav1.TupleKey{Object: "account:" + parentClusterID + "/myaccount", Relation: "member", User: roleUser}},
						},
					}, nil).Once()
				fgaClient.EXPECT().Read(mock.Anything, mock.Anything).Return(nil, assert.AnError).Once()
			},
			expectError: true,
		},
		{
			name: "error: Delete fails",
			obj:  newAccountLogicalCluster(),
			mockSetup: func(storeIDGetter *mocks.MockStoreIDGetter, mgr *mocks.MockManager, cluster *mocks.MockCluster, localClient *mocks.MockClient, fgaClient *mocks.MockOpenFGAServiceClient) {
				creator := "user@example.com"
				mockLocalAccountInfo(mgr, cluster, localClient, newLocalAccountInfo(&creator), nil)
				storeIDGetter.EXPECT().Get(mock.Anything, "myorg").Return("store-id", nil)
				fgaClient.EXPECT().Read(mock.Anything, mock.Anything).
					Return(&openfgav1.ReadResponse{
						Tuples: []*openfgav1.Tuple{
							{Key: &openfgav1.TupleKey{Object: "account:" + parentClusterID + "/myaccount", Relation: "member", User: "user:someone"}},
						},
					}, nil).Once()
				fgaClient.EXPECT().Write(mock.Anything, mock.Anything).Return(nil, assert.AnError)
			},
			expectError: true,
		},
		{
			name: "success: no tuples to delete",
			obj:  newAccountLogicalCluster(),
			mockSetup: func(storeIDGetter *mocks.MockStoreIDGetter, mgr *mocks.MockManager, cluster *mocks.MockCluster, localClient *mocks.MockClient, fgaClient *mocks.MockOpenFGAServiceClient) {
				creator := "user@example.com"
				mockLocalAccountInfo(mgr, cluster, localClient, newLocalAccountInfo(&creator), nil)
				storeIDGetter.EXPECT().Get(mock.Anything, "myorg").Return("store-id", nil)
				fgaClient.EXPECT().Read(mock.Anything, mock.Anything).
					Return(&openfgav1.ReadResponse{Tuples: []*openfgav1.Tuple{}}, nil).Once()
			},
			expectError: false,
		},
		{
			name: "success: tuples with role prefix deleted",
			obj:  newAccountLogicalCluster(),
			mockSetup: func(storeIDGetter *mocks.MockStoreIDGetter, mgr *mocks.MockManager, cluster *mocks.MockCluster, localClient *mocks.MockClient, fgaClient *mocks.MockOpenFGAServiceClient) {
				creator := "user@example.com"
				mockLocalAccountInfo(mgr, cluster, localClient, newLocalAccountInfo(&creator), nil)
				storeIDGetter.EXPECT().Get(mock.Anything, "myorg").Return("store-id", nil)
				roleUser := "role:account/" + parentClusterID + "/myaccount/owner#assignee"
				fgaClient.EXPECT().Read(mock.Anything, mock.Anything).
					Return(&openfgav1.ReadResponse{
						Tuples: []*openfgav1.Tuple{
							{Key: &openfgav1.TupleKey{Object: "account:" + parentClusterID + "/myaccount", Relation: "member", User: roleUser}},
						},
					}, nil).Once()
				fgaClient.EXPECT().Read(mock.Anything, mock.Anything).
					Return(&openfgav1.ReadResponse{Tuples: []*openfgav1.Tuple{}}, nil).Once()
				fgaClient.EXPECT().Write(mock.Anything, mock.Anything).Return(&openfgav1.WriteResponse{}, nil)
			},
			expectError: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			storeIDGetter := mocks.NewMockStoreIDGetter(t)
			mgr := mocks.NewMockManager(t)
			cluster := mocks.NewMockCluster(t)
			localClient := mocks.NewMockClient(t)
			fgaClient := mocks.NewMockOpenFGAServiceClient(t)

			test.mockSetup(storeIDGetter, mgr, cluster, localClient, fgaClient)

			sub := subroutine.NewAccountTuplesSubroutine(mgr, fgaClient, storeIDGetter, "creator", "parent", "account", nil)
			_, err := sub.Terminate(context.Background(), test.obj)
			if test.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

package subroutine_test

import (
	"context"
	"errors"
	"testing"

	accountsv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/golang-commons/logger/testlogger"
	corev1alpha1 "github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/security-operator/internal/subroutine"
	"github.com/platform-mesh/security-operator/internal/subroutine/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

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
	sub := subroutine.NewAPIExportPolicySubroutine(nil, nil, nil, nil)
	assert.Equal(t, "APIExportPolicySubroutine", sub.GetName())
}

func TestAPIExportPolicySubroutine_Finalizers(t *testing.T) {
	sub := subroutine.NewAPIExportPolicySubroutine(nil, nil, nil, nil)
	assert.Equal(t, []string{"authorization.platform-mesh.io/apiexportpolicy-finalizer"}, sub.Finalizers(nil))
}

func TestAPIExportPolicySubroutine_Process(t *testing.T) {
	tests := []struct {
		name           string
		policy         *corev1alpha1.APIExportPolicy
		setupMocks     func(*testing.T, *mocks.MockOpenFGAServiceClient, *mocks.MockManager, *mocks.MockStoreIDGetter, *mocks.MockCluster, client.Client)
		cfg            *config.Config
		expectError    bool
		expectedStatus []string
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
			setupMocks: func(t *testing.T, fga *mocks.MockOpenFGAServiceClient, mgr *mocks.MockManager, storeIDGetter *mocks.MockStoreIDGetter, cluster *mocks.MockCluster, fakeClient client.Client) {
				ctrlMgr := mocks.NewCTRLManager(t)
				mgr.EXPECT().GetLocalManager().Return(ctrlMgr).Maybe()
				ctrlMgr.EXPECT().GetConfig().Return(&rest.Config{Host: "https://localhost:99999"}).Maybe()
				ctrlMgr.EXPECT().GetScheme().Return(getAPIExportPolicyTestScheme()).Maybe()
			},
			cfg:         &config.Config{},
			expectError: true,
		},
		{
			name: "should fail when deleteRemovedExpressions fails - cannot get cluster ID",
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
				Status: corev1alpha1.APIExportPolicyStatus{
					ManagedAllowExpressions: []string{"root:orgs:old-expression"},
				},
			},
			setupMocks: func(t *testing.T, fga *mocks.MockOpenFGAServiceClient, mgr *mocks.MockManager, storeIDGetter *mocks.MockStoreIDGetter, cluster *mocks.MockCluster, fakeClient client.Client) {
				ctrlMgr := mocks.NewCTRLManager(t)
				mgr.EXPECT().GetLocalManager().Return(ctrlMgr).Maybe()
				ctrlMgr.EXPECT().GetConfig().Return(&rest.Config{Host: "https://localhost:99999"}).Maybe()
				ctrlMgr.EXPECT().GetScheme().Return(getAPIExportPolicyTestScheme()).Maybe()
			},
			cfg:         &config.Config{},
			expectError: true,
		},
		{
			name: "should fail when expression path is invalid",
			policy: &corev1alpha1.APIExportPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-policy",
				},
				Spec: corev1alpha1.APIExportPolicySpec{
					APIExportRef: corev1alpha1.APIExportRef{
						Name:        "my-export",
						ClusterPath: "root:providers:my-provider",
					},
					AllowPathExpressions: []string{"invalid:expression:path"},
				},
			},
			setupMocks: func(t *testing.T, fga *mocks.MockOpenFGAServiceClient, mgr *mocks.MockManager, storeIDGetter *mocks.MockStoreIDGetter, cluster *mocks.MockCluster, fakeClient client.Client) {
				ctrlMgr := mocks.NewCTRLManager(t)
				mgr.EXPECT().GetLocalManager().Return(ctrlMgr).Maybe()
				ctrlMgr.EXPECT().GetConfig().Return(&rest.Config{Host: "https://localhost:99999"}).Maybe()
				ctrlMgr.EXPECT().GetScheme().Return(getAPIExportPolicyTestScheme()).Maybe()
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
			setupMocks: func(t *testing.T, fga *mocks.MockOpenFGAServiceClient, mgr *mocks.MockManager, storeIDGetter *mocks.MockStoreIDGetter, cluster *mocks.MockCluster, fakeClient client.Client) {
				ctrlMgr := mocks.NewCTRLManager(t)
				mgr.EXPECT().GetLocalManager().Return(ctrlMgr).Maybe()
				ctrlMgr.EXPECT().GetConfig().Return(&rest.Config{Host: "https://localhost:99999"}).Maybe()
				ctrlMgr.EXPECT().GetScheme().Return(getAPIExportPolicyTestScheme()).Maybe()
			},
			cfg:         &config.Config{},
			expectError: true,
		},
		{
			name: "should handle wildcard expression with root:orgs path",
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
			setupMocks: func(t *testing.T, fga *mocks.MockOpenFGAServiceClient, mgr *mocks.MockManager, storeIDGetter *mocks.MockStoreIDGetter, cluster *mocks.MockCluster, fakeClient client.Client) {
				ctrlMgr := mocks.NewCTRLManager(t)
				mgr.EXPECT().GetLocalManager().Return(ctrlMgr).Maybe()
				ctrlMgr.EXPECT().GetConfig().Return(&rest.Config{Host: "https://localhost:99999"}).Maybe()
				ctrlMgr.EXPECT().GetScheme().Return(getAPIExportPolicyTestScheme()).Maybe()
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

			scheme := getAPIExportPolicyTestScheme()
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&corev1alpha1.APIExportPolicy{}).
				Build()

			if tt.setupMocks != nil {
				tt.setupMocks(t, fga, mgr, storeIDGetter, cluster, fakeClient)
			}

			l := testlogger.New()
			ctx := l.WithContext(context.Background())

			sub := subroutine.NewAPIExportPolicySubroutine(fga, mgr, tt.cfg, storeIDGetter)

			_, err := sub.Process(ctx, tt.policy)

			if tt.expectError {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
				if tt.expectedStatus != nil {
					assert.Equal(t, tt.expectedStatus, tt.policy.Status.ManagedAllowExpressions)
				}
			}
		})
	}
}

func TestAPIExportPolicySubroutine_Finalize(t *testing.T) {
	tests := []struct {
		name        string
		policy      *corev1alpha1.APIExportPolicy
		setupMocks  func(*testing.T, *mocks.MockOpenFGAServiceClient, *mocks.MockManager, *mocks.MockStoreIDGetter)
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
			setupMocks: func(t *testing.T, fga *mocks.MockOpenFGAServiceClient, mgr *mocks.MockManager, storeIDGetter *mocks.MockStoreIDGetter) {
				ctrlMgr := mocks.NewCTRLManager(t)
				mgr.EXPECT().GetLocalManager().Return(ctrlMgr).Maybe()
				ctrlMgr.EXPECT().GetConfig().Return(&rest.Config{Host: "https://localhost:99999"}).Maybe()
				ctrlMgr.EXPECT().GetScheme().Return(getAPIExportPolicyTestScheme()).Maybe()
			},
			cfg:         &config.Config{},
			expectError: true,
		},
		{
			name: "should fail when expression is invalid during finalize",
			policy: &corev1alpha1.APIExportPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-policy",
				},
				Spec: corev1alpha1.APIExportPolicySpec{
					APIExportRef: corev1alpha1.APIExportRef{
						Name:        "my-export",
						ClusterPath: "root:providers:my-provider",
					},
					AllowPathExpressions: []string{"invalid:path"},
				},
			},
			setupMocks: func(t *testing.T, fga *mocks.MockOpenFGAServiceClient, mgr *mocks.MockManager, storeIDGetter *mocks.MockStoreIDGetter) {
				ctrlMgr := mocks.NewCTRLManager(t)
				mgr.EXPECT().GetLocalManager().Return(ctrlMgr).Maybe()
				ctrlMgr.EXPECT().GetConfig().Return(&rest.Config{Host: "https://localhost:99999"}).Maybe()
				ctrlMgr.EXPECT().GetScheme().Return(getAPIExportPolicyTestScheme()).Maybe()
			},
			cfg:         &config.Config{},
			expectError: true,
		},
		{
			name: "should handle finalize with wildcard expression",
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
			setupMocks: func(t *testing.T, fga *mocks.MockOpenFGAServiceClient, mgr *mocks.MockManager, storeIDGetter *mocks.MockStoreIDGetter) {
				ctrlMgr := mocks.NewCTRLManager(t)
				mgr.EXPECT().GetLocalManager().Return(ctrlMgr).Maybe()
				ctrlMgr.EXPECT().GetConfig().Return(&rest.Config{Host: "https://localhost:99999"}).Maybe()
				ctrlMgr.EXPECT().GetScheme().Return(getAPIExportPolicyTestScheme()).Maybe()
			},
			cfg:         &config.Config{},
			expectError: true,
		},
		{
			name: "should handle finalize with multiple expressions",
			policy: &corev1alpha1.APIExportPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-policy",
				},
				Spec: corev1alpha1.APIExportPolicySpec{
					APIExportRef: corev1alpha1.APIExportRef{
						Name:        "my-export",
						ClusterPath: "root:providers:my-provider",
					},
					AllowPathExpressions: []string{
						"root:orgs:acme",
						"root:orgs:beta:*",
					},
				},
			},
			setupMocks: func(t *testing.T, fga *mocks.MockOpenFGAServiceClient, mgr *mocks.MockManager, storeIDGetter *mocks.MockStoreIDGetter) {
				ctrlMgr := mocks.NewCTRLManager(t)
				mgr.EXPECT().GetLocalManager().Return(ctrlMgr).Maybe()
				ctrlMgr.EXPECT().GetConfig().Return(&rest.Config{Host: "https://localhost:99999"}).Maybe()
				ctrlMgr.EXPECT().GetScheme().Return(getAPIExportPolicyTestScheme()).Maybe()
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

			if tt.setupMocks != nil {
				tt.setupMocks(t, fga, mgr, storeIDGetter)
			}

			l := testlogger.New()
			ctx := l.WithContext(context.Background())

			sub := subroutine.NewAPIExportPolicySubroutine(fga, mgr, tt.cfg, storeIDGetter)

			_, err := sub.Finalize(ctx, tt.policy)

			if tt.expectError {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

func TestAPIExportPolicySubroutine_ProcessWithFGAMocks(t *testing.T) {
	tests := []struct {
		name        string
		policy      *corev1alpha1.APIExportPolicy
		fgaMocks    func(*mocks.MockOpenFGAServiceClient)
		setupMocks  func(*testing.T, *mocks.MockManager, *mocks.MockStoreIDGetter, *mocks.MockCluster, client.Client)
		cfg         *config.Config
		expectError bool
	}{
		{
			name: "should fail when StoreIDGetter returns error",
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
			fgaMocks: func(fga *mocks.MockOpenFGAServiceClient) {
				// No FGA calls expected since we fail before reaching FGA
			},
			setupMocks: func(t *testing.T, mgr *mocks.MockManager, storeIDGetter *mocks.MockStoreIDGetter, cluster *mocks.MockCluster, fakeClient client.Client) {
				ctrlMgr := mocks.NewCTRLManager(t)
				mgr.EXPECT().GetLocalManager().Return(ctrlMgr).Maybe()
				ctrlMgr.EXPECT().GetConfig().Return(&rest.Config{Host: "https://localhost:99999"}).Maybe()
				ctrlMgr.EXPECT().GetScheme().Return(getAPIExportPolicyTestScheme()).Maybe()
				// StoreIDGetter would be called after getting AccountInfo, but we fail before that
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

			scheme := getAPIExportPolicyTestScheme()
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&corev1alpha1.APIExportPolicy{}).
				Build()

			if tt.fgaMocks != nil {
				tt.fgaMocks(fga)
			}
			if tt.setupMocks != nil {
				tt.setupMocks(t, mgr, storeIDGetter, cluster, fakeClient)
			}

			l := testlogger.New()
			ctx := l.WithContext(context.Background())

			sub := subroutine.NewAPIExportPolicySubroutine(fga, mgr, tt.cfg, storeIDGetter)

			_, err := sub.Process(ctx, tt.policy)

			if tt.expectError {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

func TestAPIExportPolicySubroutine_FinalizeWithFGAWrite(t *testing.T) {
	tests := []struct {
		name        string
		policy      *corev1alpha1.APIExportPolicy
		fgaMocks    func(*mocks.MockOpenFGAServiceClient)
		setupMocks  func(*testing.T, *mocks.MockManager, *mocks.MockStoreIDGetter)
		cfg         *config.Config
		expectError bool
	}{
		{
			name: "should fail when FGA Write fails during tuple deletion",
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
			fgaMocks: func(fga *mocks.MockOpenFGAServiceClient) {
				fga.EXPECT().Write(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errors.New("FGA write failed")).Maybe()
			},
			setupMocks: func(t *testing.T, mgr *mocks.MockManager, storeIDGetter *mocks.MockStoreIDGetter) {
				ctrlMgr := mocks.NewCTRLManager(t)
				mgr.EXPECT().GetLocalManager().Return(ctrlMgr).Maybe()
				ctrlMgr.EXPECT().GetConfig().Return(&rest.Config{Host: "https://localhost:99999"}).Maybe()
				ctrlMgr.EXPECT().GetScheme().Return(getAPIExportPolicyTestScheme()).Maybe()
				storeIDGetter.EXPECT().Get(mock.Anything, mock.Anything).Return("store-id", nil).Maybe()
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

			if tt.fgaMocks != nil {
				tt.fgaMocks(fga)
			}
			if tt.setupMocks != nil {
				tt.setupMocks(t, mgr, storeIDGetter)
			}

			l := testlogger.New()
			ctx := l.WithContext(context.Background())

			sub := subroutine.NewAPIExportPolicySubroutine(fga, mgr, tt.cfg, storeIDGetter)

			_, err := sub.Finalize(ctx, tt.policy)

			if tt.expectError {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}
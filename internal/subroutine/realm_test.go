package subroutine

import (
	"context"
	"testing"

	kcpv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/security-operator/internal/subroutine/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestApplyReleaseWithValues_TableDriven(t *testing.T) {
	cases := []struct {
		name       string
		setupMocks func(m *mocks.MockClient)
		expectErr  bool
	}{
		{
			name: "success - spec.values is JSON and patch succeeds",
			setupMocks: func(m *mocks.MockClient) {
				m.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
					func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						hr := obj.(*unstructured.Unstructured)

						// Extract .spec
						spec, found, err := unstructured.NestedFieldNoCopy(hr.Object, "spec")
						require.NoError(t, err, "should be able to get spec")
						require.True(t, found, "spec should be present")

						specMap, ok := spec.(map[string]interface{})
						require.True(t, ok, "spec should be a map[string]interface{}")

						// Extract .spec.values
						specValues, found, err := unstructured.NestedFieldNoCopy(specMap, "values")
						require.NoError(t, err, "should be able to get spec.values")
						require.True(t, found, "spec.values should be present")

						_, ok = specValues.(apiextensionsv1.JSON)
						require.True(t, ok, "spec.values should be of type apiextensionsv1.JSON")

						return nil
					},
				).Once()
			},
			expectErr: false,
		},
		{
			name: "patch fails - wrapped error returned",
			setupMocks: func(m *mocks.MockClient) {
				m.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(errors.New("simulated patch fail")).
					Once()
			},
			expectErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clientMock := new(mocks.MockClient)
			if tc.setupMocks != nil {
				tc.setupMocks(clientMock)
			}
			ctx := context.Background()

			err := applyReleaseWithValues(ctx, helmRelease, clientMock, apiextensionsv1.JSON{}, "test-org")
			if tc.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestApplyManifestWithMergedValues_TableDriven(t *testing.T) {
	cases := []struct {
		name       string
		setupMocks func(m *mocks.MockClient)
		expectErr  bool
	}{
		{
			name: "success - patch accepts unstructured",
			setupMocks: func(m *mocks.MockClient) {
				m.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					RunAndReturn(func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						// Ensure it's an unstructured object
						_, ok := obj.(*unstructured.Unstructured)
						require.True(t, ok)
						return nil
					}).Once()
			},
			expectErr: false,
		},
		{
			name: "patch fails - wrapped error returned",
			setupMocks: func(m *mocks.MockClient) {
				m.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(errors.New("simulated patch fail for manifest")).
					Once()
			},
			expectErr: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			clientMock := new(mocks.MockClient)
			if tc.setupMocks != nil {
				tc.setupMocks(clientMock)
			}
			ctx := context.Background()

			err := applyManifestWithMergedValues(ctx, repository, clientMock, nil)
			if tc.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestRealmSubroutine_Process_TableDriven(t *testing.T) {
	cases := []struct {
		name       string
		lc         *kcpv1alpha1.LogicalCluster
		setupMocks func(m *mocks.MockClient)
		expectErr  bool
	}{
		{
			name: "success - create repo then helmrelease with JSON values",
			lc: func() *kcpv1alpha1.LogicalCluster {
				l := &kcpv1alpha1.LogicalCluster{}
				l.ObjectMeta.Annotations = map[string]string{"kcp.io/path": "root:orgs:test"}
				return l
			}(),
			setupMocks: func(m *mocks.MockClient) {
				// First Patch: OCI repository creation
				m.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					RunAndReturn(func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						_, ok := obj.(*unstructured.Unstructured)
						require.True(t, ok, "expected unstructured object for OCI repository")
						return nil
					}).Once()
				// Second Patch: HelmRelease, validate spec.values type
				m.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					RunAndReturn(func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						hr := obj.(*unstructured.Unstructured)
						spec, found, err := unstructured.NestedFieldNoCopy(hr.Object, "spec")
						require.NoError(t, err)
						require.True(t, found)

						specMap, ok := spec.(map[string]interface{})
						require.True(t, ok)

						specValues, found, err := unstructured.NestedFieldNoCopy(specMap, "values")
						require.NoError(t, err)
						require.True(t, found)

						_, ok = specValues.(apiextensionsv1.JSON)
						require.True(t, ok)
						return nil
					}).Once()
			},
			expectErr: false,
		},
		{
			name: "oci apply fails - process returns operator error",
			lc: func() *kcpv1alpha1.LogicalCluster {
				l := &kcpv1alpha1.LogicalCluster{}
				l.ObjectMeta.Annotations = map[string]string{"kcp.io/path": "root:myrealm"}
				return l
			}(),
			setupMocks: func(m *mocks.MockClient) {
				// Simulate failure on first patch (OCI)
				m.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					RunAndReturn(func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						_, ok := obj.(*unstructured.Unstructured)
						require.True(t, ok, "expected unstructured object for OCI repository")
						return errors.New("simulated patch failure for OCI repo")
					}).Once()
			},
			expectErr: true,
		},
		{
			name: "no workspace annotation - returns operator error",
			lc: func() *kcpv1alpha1.LogicalCluster {
				return &kcpv1alpha1.LogicalCluster{} // no annotations
			}(),
			setupMocks: nil,
			expectErr:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clientMock := new(mocks.MockClient)
			if tc.setupMocks != nil {
				tc.setupMocks(clientMock)
			}
			rs := NewRealmSubroutine(clientMock)
			ctx := context.Background()

			res, opErr := rs.Process(ctx, tc.lc)
			if tc.expectErr {
				require.NotNil(t, opErr)
				require.Equal(t, ctrl.Result{}, res)
			} else {
				require.Nil(t, opErr)
				require.Equal(t, ctrl.Result{}, res)
			}
		})
	}
}

func TestReplaceTemplateAndUnstructured_TableDriven(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())

	cases := []struct {
		name         string
		templateData map[string]string
		template     []byte
		expectErr    bool
		expectOutput string
	}{
		{
			name:      "parse error invalid template",
			template:  []byte("{{"),
			expectErr: true,
		},
		{
			name:         "empty template yields empty result",
			templateData: map[string]string{},
			template:     []byte(""),
			expectErr:    false,
			expectOutput: "",
		},
		{
			name:         "successful template rendering",
			templateData: map[string]string{"Name": "testing"},
			template:     []byte("hello {{ .Name }}"),
			expectErr:    false,
			expectOutput: "hello testing",
		},
		{
			name:      "unmarshal YAML error from manifest",
			template:  []byte("not: : valid: yaml"),
			expectErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := ReplaceTemplate(tc.templateData, tc.template)
			// If the ReplaceTemplate parse fails, we expect err directly from ReplaceTemplate.
			if tc.name == "parse error invalid template" {
				require.Error(t, err)
				return
			}

			// For other cases, if ReplaceTemplate succeeded, attempt to unmarshal into unstructured
			if err != nil {
				// For invalid YAML case we expect an error from unstructuredFromString as well
				_, err2 := unstructuredFromString(string(tc.template), nil, log)
				require.Error(t, err2)
				return
			}

			require.NoError(t, err)
			if tc.expectOutput != "" {
				require.Equal(t, tc.expectOutput, string(out))
			}

			_, err2 := unstructuredFromString("kind: Test\nmetadata:\n  name: t", nil, log)
			require.NoError(t, err2)
		})
	}
}

func TestFinalize_TableDriven(t *testing.T) {
	cases := []struct {
		name       string
		setupMocks func(m *mocks.MockClient)
		expectErr  bool
	}{
		{
			name: "OCI delete error",
			setupMocks: func(m *mocks.MockClient) {
				// First delete (OCI) fails
				m.EXPECT().Delete(mock.Anything, mock.Anything).
					Return(errors.New("failed to delete oci")).
					Once()
			},
			expectErr: true,
		},
		{
			name: "HelmRelease delete error (first succeeds, second fails)",
			setupMocks: func(m *mocks.MockClient) {
				// OCI delete succeeds
				m.EXPECT().Delete(mock.Anything, mock.Anything).Return(nil).Once()
				// HelmRelease delete fails
				m.EXPECT().Delete(mock.Anything, mock.Anything).Return(errors.New("failed to delete helmRelease")).Once()
			},
			expectErr: true,
		},
		{
			name: "Both deletes succeed",
			setupMocks: func(m *mocks.MockClient) {
				m.EXPECT().Delete(mock.Anything, mock.Anything).Return(nil).Twice()
			},
			expectErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clientMock := new(mocks.MockClient)
			if tc.setupMocks != nil {
				tc.setupMocks(clientMock)
			}
			rs := NewRealmSubroutine(clientMock)
			lc := &kcpv1alpha1.LogicalCluster{}
			lc.ObjectMeta.Annotations = map[string]string{"kcp.io/path": "root:orgs:realm-test"}
			ctx := context.Background()

			res, opErr := rs.Finalize(ctx, lc)
			if tc.expectErr {
				require.NotNil(t, opErr)
				require.Equal(t, ctrl.Result{}, res)
			} else {
				require.Nil(t, opErr)
				require.Equal(t, ctrl.Result{}, res)
			}
		})
	}
}

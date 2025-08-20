package subroutine

import (
	"context"
	"testing"

	kcpv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/security-operator/internal/subroutine/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type DeployTestSuite struct {
	suite.Suite
	clientMock *mocks.MockClient
	testObj    *realmSubroutine
	log        *logger.Logger
}

func TestDeployTestSuite(t *testing.T) {
	suite.Run(t, new(DeployTestSuite))
}

func (s *DeployTestSuite) SetupTest() {
	s.clientMock = new(mocks.MockClient)
	s.log, _ = logger.New(logger.DefaultConfig())

	s.testObj = NewRealmSubroutine(s.clientMock)
}

func (s *DeployTestSuite) Test_applyReleaseWithValues() {
	ctx := context.TODO()

	// mocks
	s.clientMock.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: "default", Name: "rebac-authz-webhook-cert"}, mock.Anything).Return(nil).Once()
	s.clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			// Simulate a successful patch operation
			hr := obj.(*unstructured.Unstructured)

			// Extract .spec
			spec, found, err := unstructured.NestedFieldNoCopy(hr.Object, "spec")
			s.Require().NoError(err, "should be able to get spec")
			s.Require().True(found, "spec should be present")

			// Check if spec is a map
			specMap, ok := spec.(map[string]interface{})
			s.Require().True(ok, "spec should be a map[string]interface{}")

			// Extract .spec.values
			specValues, found, err := unstructured.NestedFieldNoCopy(specMap, "values")
			s.Require().NoError(err, "should be able to get spec.values")
			s.Require().True(found, "spec.values should be present")

			_, ok = specValues.(apiextensionsv1.JSON)
			s.Require().True(ok, "spec.values should be of type apiextensionsv1.JSON")

			return nil
		},
	).Once()

	err := applyReleaseWithValues(ctx, helmRelease, s.clientMock, apiextensionsv1.JSON{}, "test")
	s.Assert().NoError(err, "ApplyReleaseWithValues should not return an error")
}

func (s *DeployTestSuite) Test_applyOciRepository() {
	ctx := context.TODO()

	// mocks
	s.clientMock.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: "default", Name: "rebac-authz-webhook-cert"}, mock.Anything).Return(nil).Once()
	s.clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			return nil
		},
	).Once()

	err := applyManifestWithMergedValues(ctx, repository, s.clientMock, nil)
	s.Assert().NoError(err, "ApplyReleaseWithValues should not return an error")
}

func (s *DeployTestSuite) Test_RealmSubroutine_Process_Success() {
	ctx := context.TODO()

	// logical cluster with path annotation so getWorkspaceName returns a realm
	lc := &kcpv1alpha1.LogicalCluster{}
	lc.ObjectMeta.Annotations = map[string]string{
		"kcp.io/path": "root:orgs:test",
	}

	// First Patch (OCI repository) - successful patch
	s.clientMock.EXPECT().
		Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			_, ok := obj.(*unstructured.Unstructured)
			s.Require().True(ok, "expected unstructured object for OCI repository")
			return nil
		}).Once()

	// Second Patch (HelmRelease) - validate spec.values is JSON as expected
	s.clientMock.EXPECT().
		Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			hr := obj.(*unstructured.Unstructured)

			// Extract .spec
			spec, found, err := unstructured.NestedFieldNoCopy(hr.Object, "spec")
			s.Require().NoError(err, "should be able to get spec")
			s.Require().True(found, "spec should be present")

			specMap, ok := spec.(map[string]interface{})
			s.Require().True(ok, "spec should be a map[string]interface{}")

			// Extract .spec.values
			specValues, found, err := unstructured.NestedFieldNoCopy(specMap, "values")
			s.Require().NoError(err, "should be able to get spec.values")
			s.Require().True(found, "spec.values should be present")

			_, ok = specValues.(apiextensionsv1.JSON)
			s.Require().True(ok, "spec.values should be of type apiextensionsv1.JSON")

			return nil
		}).Once()

	res, err := s.testObj.Process(ctx, lc)
	s.Assert().Nil(err, "Process should not return an operator error")
	s.Assert().Equal(ctrl.Result{}, res, "expected empty ctrl.Result on success")
}

func (s *DeployTestSuite) Test_RealmSubroutine_Process_OciApplyError() {
	ctx := context.TODO()

	// logical cluster with path annotation so getWorkspaceName returns a realm
	lc := &kcpv1alpha1.LogicalCluster{}
	lc.ObjectMeta.Annotations = map[string]string{
		"kcp.io/path": "root:myrealm",
	}

	// Simulate a failure when patching the OCI repository.
	// This should make applyManifestFromFileWithMergedValues return an error
	// and cause Process to return an OperatorError without proceeding further.
	s.clientMock.EXPECT().
		Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			// ensure the object is an Unstructured
			_, ok := obj.(*unstructured.Unstructured)
			s.Require().True(ok, "expected unstructured object for OCI repository")
			return errors.New("simulated patch failure for OCI repo")
		}).
		Once()

	res, opErr := s.testObj.Process(ctx, lc)

	s.Assert().NotNil(opErr, "expected an operator error when OCI apply fails")
	s.Assert().Equal(ctrl.Result{}, res, "expected empty ctrl.Result on failure")
}

package fga

import (
	"context"
	"errors"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/platform-mesh/golang-commons/logger/testlogger"
	"github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/subroutine/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestTupleManager_Apply(t *testing.T) {
	t.Run("returns nil for empty tuples", func(t *testing.T) {
		client := mocks.NewMockOpenFGAServiceClient(t)
		log := testlogger.New()
		mgr := NewTupleManager(client, "store-id", "model-id", log.Logger)

		err := mgr.Apply(context.Background(), nil)
		assert.NoError(t, err)

		err = mgr.Apply(context.Background(), []v1alpha1.Tuple{})
		assert.NoError(t, err)
	})

	t.Run("writes tuples successfully", func(t *testing.T) {
		client := mocks.NewMockOpenFGAServiceClient(t)
		client.EXPECT().Write(mock.Anything, mock.MatchedBy(func(req *openfgav1.WriteRequest) bool {
			return req.StoreId == "store-id" &&
				req.AuthorizationModelId == "model-id" &&
				req.Writes != nil &&
				len(req.Writes.TupleKeys) == 2 &&
				req.Writes.OnDuplicate == "ignore"
		})).Return(&openfgav1.WriteResponse{}, nil)

		log := testlogger.New()
		mgr := NewTupleManager(client, "store-id", "model-id", log.Logger)

		tuples := []v1alpha1.Tuple{
			{Object: "doc:1", Relation: "viewer", User: "user:alice"},
			{Object: "doc:2", Relation: "owner", User: "user:bob"},
		}

		err := mgr.Apply(context.Background(), tuples)
		assert.NoError(t, err)
	})

	t.Run("returns error when write fails", func(t *testing.T) {
		client := mocks.NewMockOpenFGAServiceClient(t)
		client.EXPECT().Write(mock.Anything, mock.Anything).Return(nil, errors.New("write failed"))

		log := testlogger.New()
		mgr := NewTupleManager(client, "store-id", "model-id", log.Logger)

		tuples := []v1alpha1.Tuple{
			{Object: "doc:1", Relation: "viewer", User: "user:alice"},
		}

		err := mgr.Apply(context.Background(), tuples)
		assert.Error(t, err)
	})
}

func TestTupleManager_Delete(t *testing.T) {
	t.Run("returns nil for empty tuples", func(t *testing.T) {
		client := mocks.NewMockOpenFGAServiceClient(t)
		log := testlogger.New()
		mgr := NewTupleManager(client, "store-id", "model-id", log.Logger)

		err := mgr.Delete(context.Background(), nil)
		assert.NoError(t, err)

		err = mgr.Delete(context.Background(), []v1alpha1.Tuple{})
		assert.NoError(t, err)
	})

	t.Run("deletes tuples successfully", func(t *testing.T) {
		client := mocks.NewMockOpenFGAServiceClient(t)
		client.EXPECT().Write(mock.Anything, mock.MatchedBy(func(req *openfgav1.WriteRequest) bool {
			return req.StoreId == "store-id" &&
				req.AuthorizationModelId == "model-id" &&
				req.Deletes != nil &&
				len(req.Deletes.TupleKeys) == 2 &&
				req.Deletes.OnMissing == "ignore"
		})).Return(&openfgav1.WriteResponse{}, nil)

		log := testlogger.New()
		mgr := NewTupleManager(client, "store-id", "model-id", log.Logger)

		tuples := []v1alpha1.Tuple{
			{Object: "doc:1", Relation: "viewer", User: "user:alice"},
			{Object: "doc:2", Relation: "owner", User: "user:bob"},
		}

		err := mgr.Delete(context.Background(), tuples)
		assert.NoError(t, err)
	})

	t.Run("returns error when delete fails", func(t *testing.T) {
		client := mocks.NewMockOpenFGAServiceClient(t)
		client.EXPECT().Write(mock.Anything, mock.Anything).Return(nil, errors.New("delete failed"))

		log := testlogger.New()
		mgr := NewTupleManager(client, "store-id", "model-id", log.Logger)

		tuples := []v1alpha1.Tuple{
			{Object: "doc:1", Relation: "viewer", User: "user:alice"},
		}

		err := mgr.Delete(context.Background(), tuples)
		assert.Error(t, err)
	})
}

func TestTupleManager_Apply_verifies_tuple_contents(t *testing.T) {
	var capturedReq *openfgav1.WriteRequest
	client := mocks.NewMockOpenFGAServiceClient(t)
	client.EXPECT().Write(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, req *openfgav1.WriteRequest, opts ...grpc.CallOption) (*openfgav1.WriteResponse, error) {
		capturedReq = req
		return &openfgav1.WriteResponse{}, nil
	})

	log := testlogger.New()
	mgr := NewTupleManager(client, "store-id", "model-id", log.Logger)

	tuples := []v1alpha1.Tuple{
		{Object: "doc:1", Relation: "viewer", User: "user:alice"},
		{Object: "doc:2", Relation: "owner", User: "user:bob"},
	}

	err := mgr.Apply(context.Background(), tuples)
	require.NoError(t, err)
	require.NotNil(t, capturedReq)
	require.NotNil(t, capturedReq.Writes)
	require.Len(t, capturedReq.Writes.TupleKeys, 2)

	// Verify both tuples are in the request
	keys := capturedReq.Writes.TupleKeys
	assert.True(t, (keys[0].Object == "doc:1" && keys[0].Relation == "viewer" && keys[0].User == "user:alice") ||
		(keys[1].Object == "doc:1" && keys[1].Relation == "viewer" && keys[1].User == "user:alice"))
	assert.True(t, (keys[0].Object == "doc:2" && keys[0].Relation == "owner" && keys[0].User == "user:bob") ||
		(keys[1].Object == "doc:2" && keys[1].Relation == "owner" && keys[1].User == "user:bob"))
}

func TestTupleManager_Delete_verifies_tuple_contents(t *testing.T) {
	var capturedReq *openfgav1.WriteRequest
	client := mocks.NewMockOpenFGAServiceClient(t)
	client.EXPECT().Write(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, req *openfgav1.WriteRequest, opts ...grpc.CallOption) (*openfgav1.WriteResponse, error) {
		capturedReq = req
		return &openfgav1.WriteResponse{}, nil
	})

	log := testlogger.New()
	mgr := NewTupleManager(client, "store-id", "model-id", log.Logger)

	tuples := []v1alpha1.Tuple{
		{Object: "doc:1", Relation: "viewer", User: "user:alice"},
		{Object: "doc:2", Relation: "owner", User: "user:bob"},
	}

	err := mgr.Delete(context.Background(), tuples)
	require.NoError(t, err)
	require.NotNil(t, capturedReq)
	require.NotNil(t, capturedReq.Deletes)
	require.Len(t, capturedReq.Deletes.TupleKeys, 2)

	keys := capturedReq.Deletes.TupleKeys
	assert.True(t, (keys[0].Object == "doc:1" && keys[0].Relation == "viewer" && keys[0].User == "user:alice") ||
		(keys[1].Object == "doc:1" && keys[1].Relation == "viewer" && keys[1].User == "user:alice"))
	assert.True(t, (keys[0].Object == "doc:2" && keys[0].Relation == "owner" && keys[0].User == "user:bob") ||
		(keys[1].Object == "doc:2" && keys[1].Relation == "owner" && keys[1].User == "user:bob"))
}

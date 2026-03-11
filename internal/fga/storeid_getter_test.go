package fga

import (
	"context"
	"errors"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/platform-mesh/security-operator/internal/subroutine/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestCachingStoreIDGetter_Get(t *testing.T) {
	t.Run("returns store ID from OpenFGA on cache miss", func(t *testing.T) {
		client := mocks.NewMockOpenFGAServiceClient(t)
		client.EXPECT().ListStores(mock.Anything, mock.Anything).Return(&openfgav1.ListStoresResponse{
			Stores: []*openfgav1.Store{
				{Name: "foo", Id: "DEADBEEF"},
			},
		}, nil).Once()

		getter := NewCachingStoreIDGetter(client)

		id, err := getter.Get(context.Background(), "foo")
		require.NoError(t, err)
		assert.Equal(t, "DEADBEEF", id)
	})

	t.Run("returns cached value on subsequent calls without calling OpenFGA", func(t *testing.T) {
		client := mocks.NewMockOpenFGAServiceClient(t)
		client.EXPECT().ListStores(mock.Anything, mock.Anything).Return(&openfgav1.ListStoresResponse{
			Stores: []*openfgav1.Store{
				{Name: "foo", Id: "DEADBEEF"},
			},
		}, nil).Once()

		getter := NewCachingStoreIDGetter(client)

		id1, err := getter.Get(context.Background(), "foo")
		require.NoError(t, err)
		assert.Equal(t, "DEADBEEF", id1)

		id2, err := getter.Get(context.Background(), "foo")
		require.NoError(t, err)
		assert.Equal(t, "DEADBEEF", id2)

		client.AssertExpectations(t)
	})

	t.Run("returns error when store not found in OpenFGA", func(t *testing.T) {
		client := mocks.NewMockOpenFGAServiceClient(t)
		client.EXPECT().ListStores(mock.Anything, mock.Anything).Return(&openfgav1.ListStoresResponse{
			Stores: []*openfgav1.Store{
				{Name: "other-store", Id: "OTHER-ID"},
			},
		}, nil).Once()

		getter := NewCachingStoreIDGetter(client)

		id, err := getter.Get(context.Background(), "missing-store")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "store \"missing-store\" not found")
		assert.Empty(t, id)
	})

	t.Run("returns error when ListStores fails", func(t *testing.T) {
		client := mocks.NewMockOpenFGAServiceClient(t)
		client.EXPECT().ListStores(mock.Anything, mock.Anything).Return(nil, errors.New("connection refused")).Once()

		getter := NewCachingStoreIDGetter(client)

		id, err := getter.Get(context.Background(), "foo")
		assert.Error(t, err)
		assert.Empty(t, id)
	})

	t.Run("sync removes stores no longer in OpenFGA", func(t *testing.T) {
		callCount := 0
		client := mocks.NewMockOpenFGAServiceClient(t)
		client.EXPECT().ListStores(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, req *openfgav1.ListStoresRequest, opts ...grpc.CallOption) (*openfgav1.ListStoresResponse, error) {
			callCount++
			if callCount == 1 {
				// First sync: two stores
				return &openfgav1.ListStoresResponse{
					Stores: []*openfgav1.Store{
						{Name: "foo", Id: "DEADBEEF"},
						{Name: "bar", Id: "1337CAFE"},
					},
				}, nil
			}
			// Second sync: one store deleted
			return &openfgav1.ListStoresResponse{
				Stores: []*openfgav1.Store{
					{Name: "bar", Id: "1337CAFE"},
				},
			}, nil
		})

		getter := NewCachingStoreIDGetter(client)

		// First Get: sync loads both stores
		id1, err := getter.Get(context.Background(), "foo")
		require.NoError(t, err)
		assert.Equal(t, "DEADBEEF", id1)

		// Get a non-cached store to trigger rsync on cache-miss
		_, err = getter.Get(context.Background(), "hoge")
		assert.Error(t, err)

		// DEADBEEF should not be in cache anymore
		_, err = getter.Get(context.Background(), "foo")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "store \"foo\" not found")
	})
}

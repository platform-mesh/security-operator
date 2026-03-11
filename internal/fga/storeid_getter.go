package fga

import (
	"context"
	"fmt"
	"sync"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// StoreIDGetter should return the OpenFGA store ID for a store name.
type StoreIDGetter interface {
	Get(ctx context.Context, storeName string) (string, error)
}

// CachingStoreIDGetter maps store names to IDs by listing stores in OpenFGA but keeps
// a local cache to avoid frequent list calls.
type CachingStoreIDGetter struct {
	mu     sync.RWMutex
	stores map[string]string
	fga    openfgav1.OpenFGAServiceClient
}

func NewCachingStoreIDGetter(fga openfgav1.OpenFGAServiceClient) *CachingStoreIDGetter {
	return &CachingStoreIDGetter{
		stores: make(map[string]string),
		fga:    fga,
	}
}

// Get returns the store ID for the given store name.
func (m *CachingStoreIDGetter) Get(ctx context.Context, storeName string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if id, ok := m.stores[storeName]; ok {
		return id, nil
	}

	if err := m.syncFromOpenFGA(ctx); err != nil {
		return "", fmt.Errorf("syncing stores: %w", err)
	}

	if id, ok := m.stores[storeName]; ok {
		return id, nil
	}

	return "", fmt.Errorf("store %q not found", storeName)
}

func (m *CachingStoreIDGetter) syncFromOpenFGA(ctx context.Context) error {
	stores := make(map[string]string)
	var continuationToken string

	for {
		resp, err := m.fga.ListStores(ctx, &openfgav1.ListStoresRequest{
			PageSize:          wrapperspb.Int32(100),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return err
		}

		for _, store := range resp.GetStores() {
			stores[store.GetName()] = store.GetId()
		}

		continuationToken = resp.GetContinuationToken()
		if continuationToken == "" {
			break
		}
	}

	m.stores = stores
	return nil
}

var _ StoreIDGetter = (*CachingStoreIDGetter)(nil)

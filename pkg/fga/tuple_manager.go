package fga

import (
	"context"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/security-operator/api/v1alpha1"
)

// AuthorizationModelIDLatest is to explicitely acknowledge that no ID means
// latest.
const AuthorizationModelIDLatest = ""

// TupleManager wraps around FGA attributes to write and delete sets of tuples.
type TupleManager struct {
	client               openfgav1.OpenFGAServiceClient
	storeID              string
	authorizationModelID string
	logger               logger.Logger
}

type TupleFilter func(t v1alpha1.Tuple) bool

func NewTupleManager(client openfgav1.OpenFGAServiceClient, storeID, authorizationModelID string, log *logger.Logger) *TupleManager {
	return &TupleManager{
		client:               client,
		storeID:              storeID,
		authorizationModelID: authorizationModelID,
		logger:               *log.ComponentLogger("tuple_manager").MustChildLoggerWithAttributes("store_id", storeID, "authorization_model", authorizationModelID),
	}
}

// Apply writes a given set of tuples within a single transaction and ignores
// duplicate writes.
func (m *TupleManager) Apply(ctx context.Context, tuples []v1alpha1.Tuple) error {
	if len(tuples) == 0 {
		return nil
	}

	tupleKeys := make([]*openfgav1.TupleKey, 0, len(tuples))
	for _, t := range tuples {
		tupleKeys = append(tupleKeys, &openfgav1.TupleKey{
			Object:   t.Object,
			Relation: t.Relation,
			User:     t.User,
		})
	}

	_, err := m.client.Write(ctx, &openfgav1.WriteRequest{
		StoreId:              m.storeID,
		AuthorizationModelId: m.authorizationModelID,
		Writes: &openfgav1.WriteRequestWrites{
			TupleKeys:   tupleKeys,
			OnDuplicate: "ignore",
		},
	})
	if err != nil {
		return err
	}

	m.logger.Debug().Int("count", len(tuples)).Msg("Wrote tuples")
	return nil
}

// Delete deletes a given set of tuples within a single transaction and ignores
// duplicate deletions.
func (m *TupleManager) Delete(ctx context.Context, tuples []v1alpha1.Tuple) error {
	if len(tuples) == 0 {
		return nil
	}

	tupleKeys := make([]*openfgav1.TupleKeyWithoutCondition, 0, len(tuples))
	for _, t := range tuples {
		tupleKeys = append(tupleKeys, &openfgav1.TupleKeyWithoutCondition{
			Object:   t.Object,
			Relation: t.Relation,
			User:     t.User,
		})
	}

	_, err := m.client.Write(ctx, &openfgav1.WriteRequest{
		StoreId:              m.storeID,
		AuthorizationModelId: m.authorizationModelID,
		Deletes: &openfgav1.WriteRequestDeletes{
			TupleKeys: tupleKeys,
			OnMissing: "ignore",
		},
	})
	if err != nil {
		return err
	}

	m.logger.Debug().Int("count", len(tuples)).Msg("Deleted tuples")
	return nil
}

// ListWithFilter gets all tuples in the store and returns a list of all tuples
// that match the given filter.
func (m *TupleManager) ListWithFilter(ctx context.Context, filter TupleFilter) ([]v1alpha1.Tuple, error) {
	var result []v1alpha1.Tuple
	var continuationToken string
	for {
		resp, err := m.client.Read(ctx, &openfgav1.ReadRequest{
			StoreId:           m.storeID,
			TupleKey:          nil, // nil returns all tuples
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return nil, err
		}

		for _, t := range resp.Tuples {
			if t.Key == nil {
				continue
			}
			tuple := v1alpha1.Tuple{
				Object:   t.Key.Object,
				Relation: t.Key.Relation,
				User:     t.Key.User,
			}
			if filter(tuple) {
				result = append(result, tuple)
			}
		}

		continuationToken = resp.ContinuationToken
		if continuationToken == "" {
			break
		}
	}

	return result, nil
}

package fga

import (
	"context"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/security-operator/api/v1alpha1"
)

// TupleManager wraps around FGA attributes to write and delete sets of tuples.
type TupleManager struct {
	client               openfgav1.OpenFGAServiceClient
	storeID              string
	authorizationModelID string
	logger               logger.Logger
}

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

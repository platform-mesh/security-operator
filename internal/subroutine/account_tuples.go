package subroutine

import (
	"context"
	"fmt"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/security-operator/pkg/fga"
	ctrl "sigs.k8s.io/controller-runtime"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	mcclient "github.com/kcp-dev/multicluster-provider/client"
	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

// AccountTuplesSubroutine creates FGA tuples for Accounts not of the
// "org"-type when initializing, and deletes them when terminating.
type AccountTuplesSubroutine struct {
	mgr mcmanager.Manager
	mcc mcclient.ClusterClient
	fga openfgav1.OpenFGAServiceClient

	objectType      string
	parentRelation  string
	creatorRelation string
}

// Process implements lifecycle.Subroutine as no-op since Initialize handles the
// work when not in deletion.
func (s *AccountTuplesSubroutine) Process(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil
}

// Initialize implements lifecycle.Initializer.
func (s *AccountTuplesSubroutine) Initialize(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	lc := instance.(*kcpcorev1alpha1.LogicalCluster)
	acc, ai, opErr := AccountAndInfoForLogicalCluster(ctx, s.mgr, lc)
	if opErr != nil {
		return ctrl.Result{}, opErr
	}

	// Ensure the necessary tuples in OpenFGA.
	tuples, err := fga.TuplesForAccount(acc, ai, s.creatorRelation, s.parentRelation, s.objectType)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("building tuples for account: %w", err), true, true)
	}
	if err := fga.NewTupleManager(s.fga, ai.Spec.FGA.Store.Id, fga.AuthorizationModelIDLatest, logger.LoadLoggerFromContext(ctx)).Apply(ctx, tuples); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("applying tuples for Account: %w", err), true, true)
	}

	return ctrl.Result{}, nil
}

// Terminate implements lifecycle.Terminator.
func (s *AccountTuplesSubroutine) Terminate(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	lc := instance.(*kcpcorev1alpha1.LogicalCluster)
	acc, ai, opErr := AccountAndInfoForLogicalCluster(ctx, s.mgr, lc)
	if opErr != nil {
		return ctrl.Result{}, opErr
	}

	// Delete the corresponding tuples in OpenFGA.
	tuples, err := fga.TuplesForAccount(acc, ai, s.creatorRelation, s.parentRelation, s.objectType)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("building tuples for account: %w", err), true, true)
	}
	if err := fga.NewTupleManager(s.fga, ai.Spec.FGA.Store.Id, fga.AuthorizationModelIDLatest, logger.LoadLoggerFromContext(ctx)).Delete(ctx, tuples); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("deleting tuples for Account: %w", err), true, true)
	}

	return ctrl.Result{}, nil
}

// Finalize implements lifecycle.Subroutine.
func (s *AccountTuplesSubroutine) Finalize(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil
}

// Finalizers implements lifecycle.Subroutine.
func (s *AccountTuplesSubroutine) Finalizers(_ runtimeobject.RuntimeObject) []string {
	return []string{}
}

// GetName implements lifecycle.Subroutine.
func (s *AccountTuplesSubroutine) GetName() string { return "AccountTuplesSubroutine" }

func NewAccountTuplesSubroutine(mcc mcclient.ClusterClient, mgr mcmanager.Manager, fga openfgav1.OpenFGAServiceClient, creatorRelation, parentRelation, objectType string) *AccountTuplesSubroutine {
	return &AccountTuplesSubroutine{
		mgr:             mgr,
		mcc:             mcc,
		fga:             fga,
		creatorRelation: creatorRelation,
		parentRelation:  parentRelation,
		objectType:      objectType,
	}
}

var (
	_ lifecyclesubroutine.Subroutine   = &AccountTuplesSubroutine{}
	_ lifecyclesubroutine.Initializer  = &AccountTuplesSubroutine{}
	_ lifecyclesubroutine.Terminator   = &AccountTuplesSubroutine{}
)

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

// AccountTuplesTerminatorSubroutine deletes FGA tuples when an account workspace
// is being terminated.
type AccountTuplesTerminatorSubroutine struct {
	mgr mcmanager.Manager
	mcc mcclient.ClusterClient
	fga openfgav1.OpenFGAServiceClient

	objectType      string
	parentRelation  string
	creatorRelation string
}

// Process implements lifecycle.Subroutine.
func (s *AccountTuplesTerminatorSubroutine) Process(_ context.Context, _ runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil
}

// Terminate implements lifecycle.Terminator.
func (s *AccountTuplesTerminatorSubroutine) Terminate(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
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
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("applying tuples for Account: %w", err), true, true)
	}

	return ctrl.Result{}, nil
}

// Finalize implements lifecycle.Subroutine.
func (s *AccountTuplesTerminatorSubroutine) Finalize(_ context.Context, _ runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil
}

// Finalizers implements lifecycle.Subroutine.
func (s *AccountTuplesTerminatorSubroutine) Finalizers(_ runtimeobject.RuntimeObject) []string {
	return []string{}
}

// GetName implements lifecycle.Subroutine.
func (s *AccountTuplesTerminatorSubroutine) GetName() string {
	return "AccountTuplesTerminatorSubroutine"
}

// NewAccountTuplesTerminatorSubroutine returns a new AccountTuplesTerminatorSubroutine.
func NewAccountTuplesTerminatorSubroutine(mcc mcclient.ClusterClient, mgr mcmanager.Manager, fga openfgav1.OpenFGAServiceClient, creatorRelation, parentRelation, objectType string) *AccountTuplesTerminatorSubroutine {
	return &AccountTuplesTerminatorSubroutine{
		mgr:             mgr,
		mcc:             mcc,
		fga:             fga,
		creatorRelation: creatorRelation,
		parentRelation:  parentRelation,
		objectType:      objectType,
	}
}

var _ lifecyclesubroutine.Subroutine = &AccountTuplesTerminatorSubroutine{}
var _ lifecyclesubroutine.Terminator = &AccountTuplesTerminatorSubroutine{}

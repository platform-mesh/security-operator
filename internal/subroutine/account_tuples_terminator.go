package subroutine

import (
	"context"
	"fmt"

	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	ctrl "sigs.k8s.io/controller-runtime"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	mcclient "github.com/kcp-dev/multicluster-provider/client"
	kcpcore "github.com/kcp-dev/sdk/apis/core"
	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

// AccountTuplesTerminatorSubroutine deletes FGA tuples when an account workspace
// is being terminated.
type AccountTuplesTerminatorSubroutine struct {
	mgr mcmanager.Manager
	mcc mcclient.ClusterClient
	// fga, creatorRelation, parentRelation, objectType to be added
}

// Process implements lifecycle.Subroutine.
func (s *AccountTuplesTerminatorSubroutine) Process(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)
	lc := instance.(*kcpcorev1alpha1.LogicalCluster)

	p := lc.Annotations[kcpcore.LogicalClusterPathAnnotationKey]
	if p == "" {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("annotation on LogicalCluster %s is not set", lc.Name), true, true)
	}
	lcID, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("cluster name not found in context"), true, true)
	}

	log.Info().Msgf("Processing logical cluster %s with ID %s and path %s", lc.Name, lcID, p)
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
func NewAccountTuplesTerminatorSubroutine(mcc mcclient.ClusterClient, mgr mcmanager.Manager /* fga, creatorRelation, parentRelation, objectType */) *AccountTuplesTerminatorSubroutine {
	return &AccountTuplesTerminatorSubroutine{
		mgr: mgr,
		mcc: mcc,
	}
}

var _ lifecyclesubroutine.Subroutine = &AccountTuplesTerminatorSubroutine{}

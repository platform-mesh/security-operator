package subroutine

import (
	"context"
	"fmt"
	"slices"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	securityv1alpha1 "github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/pkg/fga"
	ctrl "sigs.k8s.io/controller-runtime"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"k8s.io/apimachinery/pkg/types"
)

type tupleSubroutine struct {
	fga openfgav1.OpenFGAServiceClient
	mgr mcmanager.Manager
}

// Finalize implements lifecycle.Subroutine.
func (t *tupleSubroutine) Finalize(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)

	var storeID string
	var authorizationModelID string
	var managedTuples []securityv1alpha1.Tuple

	switch obj := instance.(type) {
	case *securityv1alpha1.Store:
		storeID = obj.Status.StoreID
		authorizationModelID = obj.Status.AuthorizationModelID
		managedTuples = obj.Status.ManagedTuples
	case *securityv1alpha1.AuthorizationModel:
		managedTuples = obj.Status.ManagedTuples

		storeCluster, err := t.mgr.GetCluster(ctx, obj.Spec.StoreRef.Cluster)
		if err != nil {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to get store cluster: %w", err), true, false)
		}

		var store securityv1alpha1.Store
		err = storeCluster.GetClient().Get(ctx, types.NamespacedName{
			Name: obj.Spec.StoreRef.Name,
		}, &store)
		if err != nil {
			return ctrl.Result{}, errors.NewOperatorError(err, true, true)
		}

		storeID = store.Status.StoreID
		authorizationModelID = store.Status.AuthorizationModelID
	}

	tm := fga.NewTupleManager(t.fga, storeID, authorizationModelID, log)
	if err := tm.Delete(ctx, managedTuples); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
	}

	switch obj := instance.(type) {
	case *securityv1alpha1.Store:
		obj.Status.ManagedTuples = nil
	case *securityv1alpha1.AuthorizationModel:
		obj.Status.ManagedTuples = nil
	}

	return ctrl.Result{}, nil
}

// Finalizers implements lifecycle.Subroutine.
func (t *tupleSubroutine) Finalizers(_ runtimeobject.RuntimeObject) []string {
	return []string{"core.platform-mesh.io/fga-tuples"}
}

// GetName implements lifecycle.Subroutine.
func (t *tupleSubroutine) GetName() string { return "TupleSubroutine" }

// Process implements lifecycle.Subroutine.
func (t *tupleSubroutine) Process(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)

	var storeID string
	var authorizationModelID string
	var specTuples []securityv1alpha1.Tuple
	var managedTuples []securityv1alpha1.Tuple

	switch obj := instance.(type) {
	case *securityv1alpha1.Store:
		storeID = obj.Status.StoreID
		authorizationModelID = obj.Status.AuthorizationModelID

		specTuples = obj.Spec.Tuples
		managedTuples = obj.Status.ManagedTuples
	case *securityv1alpha1.AuthorizationModel:
		specTuples = obj.Spec.Tuples
		managedTuples = obj.Status.ManagedTuples

		storeCluster, err := t.mgr.GetCluster(ctx, obj.Spec.StoreRef.Cluster)
		if err != nil {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to get store cluster: %w", err), true, false)
		}

		var store securityv1alpha1.Store
		err = storeCluster.GetClient().Get(ctx, types.NamespacedName{
			Name: obj.Spec.StoreRef.Name,
		}, &store)
		if err != nil { // coverage-ignore
			return ctrl.Result{}, errors.NewOperatorError(err, true, true)
		}

		storeID = store.Status.StoreID
		authorizationModelID = store.Status.AuthorizationModelID
	}

	tm := fga.NewTupleManager(t.fga, storeID, authorizationModelID, log)
	if err := tm.Apply(ctx, specTuples); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
	}

	var tuplesToDelete []securityv1alpha1.Tuple
	for _, tuple := range managedTuples {
		if slices.IndexFunc(specTuples, func(s securityv1alpha1.Tuple) bool {
			return s.Object == tuple.Object && s.Relation == tuple.Relation && s.User == tuple.User
		}) == -1 {
			tuplesToDelete = append(tuplesToDelete, tuple)
		}
	}
	if err := tm.Delete(ctx, tuplesToDelete); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
	}

	switch obj := instance.(type) {
	case *securityv1alpha1.Store:
		obj.Status.ManagedTuples = specTuples
	case *securityv1alpha1.AuthorizationModel:
		obj.Status.ManagedTuples = specTuples
	}

	return ctrl.Result{}, nil
}

func NewTupleSubroutine(fga openfgav1.OpenFGAServiceClient, mgr mcmanager.Manager) *tupleSubroutine {
	return &tupleSubroutine{
		fga: fga,
		mgr: mgr,
	}
}

var _ lifecyclesubroutine.Subroutine = &tupleSubroutine{}

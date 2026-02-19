package subroutine

import (
	"context"
	"fmt"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	accountsv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	iclient "github.com/platform-mesh/security-operator/internal/client"
	"github.com/platform-mesh/security-operator/pkg/fga"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/kcp-dev/logicalcluster/v3"
	mcclient "github.com/kcp-dev/multicluster-provider/client"
	kcpcore "github.com/kcp-dev/sdk/apis/core"
	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

const accountTuplesTerminatorFinalizer = "core.platform-mesh.io/account-tuples-terminator"

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

	if updated := controllerutil.AddFinalizer(&ai, accountTuplesTerminatorFinalizer); updated {
		lcID, ok := mccontext.ClusterFrom(ctx)
		if !ok {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("cluster name not found in context"), true, true)
		}

		lcClient, err := iclient.NewForLogicalCluster(s.mgr.GetLocalManager().GetConfig(), s.mgr.GetLocalManager().GetScheme(), logicalcluster.Name(lcID))
		if err != nil {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("getting client: %w", err), true, true)
		}

		if err := lcClient.Update(ctx, &ai); err != nil {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("updating AccountInfo to set finalizer: %w", err), true, true)
		}
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

	if updated := controllerutil.RemoveFinalizer(&ai, accountTuplesTerminatorFinalizer); updated {
		lcID, ok := mccontext.ClusterFrom(ctx)
		if !ok {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("cluster name not found in context"), true, true)
		}

		lcClient, err := iclient.NewForLogicalCluster(s.mgr.GetLocalManager().GetConfig(), s.mgr.GetLocalManager().GetScheme(), logicalcluster.Name(lcID))
		if err != nil {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("getting client: %w", err), true, true)
		}

		if err := lcClient.Update(ctx, &ai); err != nil {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("updating AccountInfo to remove finalizer: %w", err), true, true)
		}
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
	_ lifecyclesubroutine.Subroutine  = &AccountTuplesSubroutine{}
	_ lifecyclesubroutine.Initializer = &AccountTuplesSubroutine{}
	_ lifecyclesubroutine.Terminator  = &AccountTuplesSubroutine{}
)

// AccountAndInfoForLogicalCluster fetches the AccountInfo from the
// LogicalCluster and the corresponding Account from the parent account's
// workspace.
func AccountAndInfoForLogicalCluster(ctx context.Context, mgr mcmanager.Manager, lc *kcpcorev1alpha1.LogicalCluster) (accountsv1alpha1.Account, accountsv1alpha1.AccountInfo, errors.OperatorError) {
	if lc.Annotations[kcpcore.LogicalClusterPathAnnotationKey] == "" {
		return accountsv1alpha1.Account{}, accountsv1alpha1.AccountInfo{}, errors.NewOperatorError(fmt.Errorf("annotation on LogicalCluster is not set"), true, true)
	}
	lcID, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return accountsv1alpha1.Account{}, accountsv1alpha1.AccountInfo{}, errors.NewOperatorError(fmt.Errorf("cluster name not found in context"), true, true)
	}

	// The AccountInfo in the logical cluster belongs to the Account the
	// Workspace was created for
	lcClient, err := iclient.NewForLogicalCluster(mgr.GetLocalManager().GetConfig(), mgr.GetLocalManager().GetScheme(), logicalcluster.Name(lcID))
	if err != nil {
		return accountsv1alpha1.Account{}, accountsv1alpha1.AccountInfo{}, errors.NewOperatorError(fmt.Errorf("getting client: %w", err), true, true)
	}
	var ai accountsv1alpha1.AccountInfo
	if err := lcClient.Get(ctx, client.ObjectKey{
		Name: "account",
	}, &ai); err != nil && !kerrors.IsNotFound(err) {
		return accountsv1alpha1.Account{}, accountsv1alpha1.AccountInfo{}, errors.NewOperatorError(fmt.Errorf("getting AccountInfo for LogicalCluster: %w", err), true, true)
	} else if kerrors.IsNotFound(err) {
		return accountsv1alpha1.Account{}, accountsv1alpha1.AccountInfo{}, errors.NewOperatorError(fmt.Errorf("AccountInfo not found"), true, true)
	}

	// The actual Account resource belonging to the Workspace needs to be
	// fetched from the parent Account's Workspace
	parentAccountClient, err := iclient.NewForLogicalCluster(mgr.GetLocalManager().GetConfig(), mgr.GetLocalManager().GetScheme(), logicalcluster.Name(ai.Spec.ParentAccount.Path))
	if err != nil {
		return accountsv1alpha1.Account{}, accountsv1alpha1.AccountInfo{}, errors.NewOperatorError(fmt.Errorf("getting parent account cluster client: %w", err), true, true)
	}
	var acc accountsv1alpha1.Account
	if err := parentAccountClient.Get(ctx, client.ObjectKey{
		Name: ai.Spec.Account.Name,
	}, &acc); err != nil {
		return accountsv1alpha1.Account{}, accountsv1alpha1.AccountInfo{}, errors.NewOperatorError(fmt.Errorf("getting Account in parent account cluster: %w", err), true, true)
	}

	return acc, ai, nil
}

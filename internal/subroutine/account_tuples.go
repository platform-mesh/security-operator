package subroutine

import (
	"context"
	"fmt"
	"strings"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	accountsv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/security-operator/api/v1alpha1"
	iclient "github.com/platform-mesh/security-operator/internal/client"
	"github.com/platform-mesh/security-operator/internal/fga"
	platformmeshpath "github.com/platform-mesh/security-operator/internal/platformmesh"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/kcp-dev/logicalcluster/v3"
	mcclient "github.com/kcp-dev/multicluster-provider/client"
	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

// AccountTuplesSubroutine creates FGA tuples for Accounts not of the
// "org"-type when initializing, and deletes them when terminating.
type AccountTuplesSubroutine struct {
	mgr             mcmanager.Manager
	mcc             mcclient.ClusterClient
	fga             openfgav1.OpenFGAServiceClient
	storeIDGetter   fga.StoreIDGetter
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

	accountPath, err := platformmeshpath.NewAccountPathFromLogicalCluster(lc)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("getting AccountPath from LogicalCluster: %w", err), true, true)
	}

	storeID, err := s.storeIDGetter.Get(ctx, accountPath.Org().Base())
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("getting store ID: %w", err), true, true)
	}

	// Determine the parent's and grandParent's LogicalCluster ID
	parentPath, _ := accountPath.Parent()
	parentAccountClusterID, parentAccountLC, err := clusterAndIDFromLogicalClusterForPath(ctx, s.mgr, parentPath)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("getting parent account's LogicalCluster: %w", err), true, true)
	}
	grandParentAccountClusterID := parentAccountLC.Spec.Owner.Cluster

	// Retrieve the Account resource out of the parent workspace to determine
	// the creator
	parentAccountClient, err := iclient.NewForLogicalCluster(s.mgr.GetLocalManager().GetConfig(), s.mgr.GetLocalManager().GetScheme(), logicalcluster.Name(parentPath.String()))
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("getting client for parent account cluster: %w", err), true, true)
	}
	var acc accountsv1alpha1.Account
	if err := parentAccountClient.Get(ctx, client.ObjectKey{
		Name: accountPath.Base(),
	}, &acc); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("getting Account in parent account cluster: %w", err), true, true)
	}
	if acc.Spec.Creator == nil || *acc.Spec.Creator == "" {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("account creator is nil or empty"), true, true)
	}

	tuples, err := fga.InitialTuplesForAccount(fga.InitialTuplesForAccountInput{
		BaseTuplesInput: fga.BaseTuplesInput{
			Creator:                *acc.Spec.Creator,
			AccountOriginClusterID: parentAccountClusterID,
			AccountName:            accountPath.Base(),
			CreatorRelation:        s.creatorRelation,
			ObjectType:             s.objectType,
		},
		ParentOriginClusterID: grandParentAccountClusterID,
		ParentName:            parentPath.Base(),
		ParentRelation:        s.parentRelation,
	})
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("building tuples for account: %w", err), true, true)
	}
	if err := fga.NewTupleManager(s.fga, storeID, fga.AuthorizationModelIDLatest, logger.LoadLoggerFromContext(ctx)).Apply(ctx, tuples); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("applying tuples for Account: %w", err), true, true)
	}

	return ctrl.Result{}, nil
}

// Terminate implements lifecycle.Terminator.
func (s *AccountTuplesSubroutine) Terminate(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	lc := instance.(*kcpcorev1alpha1.LogicalCluster)

	accountPath, err := platformmeshpath.NewAccountPathFromLogicalCluster(lc)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("getting AccountPath from LogicalCluster: %w", err), true, true)
	}
	parentPath, _ := accountPath.Parent()

	// Determine the parent's LogicalClusterID
	parentClusterID, _, err := clusterAndIDFromLogicalClusterForPath(ctx, s.mgr, parentPath)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("getting parent account's LogicalCluster: %w", err), true, true)
	}

	storeID, err := s.storeIDGetter.Get(ctx, accountPath.Org().Base())
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("getting store ID: %w", err), true, true)
	}

	// List tuples that reference the account.
	tm := fga.NewTupleManager(s.fga, storeID, fga.AuthorizationModelIDLatest, logger.LoadLoggerFromContext(ctx))
	accountReferenceTuples, err := tm.ListWithKey(ctx, fga.ReferencingAccountTupleKey(s.objectType, parentClusterID, accountPath.Base()))
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("listing tuples referencing Account: %w", err), true, true)
	}
	accountTuples := make([]v1alpha1.Tuple, 0, len(accountReferenceTuples)*2)
	accountTuples = append(accountTuples, accountReferenceTuples...)

	// From tuples referencing the account, parse potential roles specific to the account.
	rolePrefix := fga.RenderRolePrefix(s.objectType, parentClusterID, accountPath.Base())
	for _, t := range accountReferenceTuples {
		if strings.HasPrefix(t.User, rolePrefix) {
			role := strings.TrimSuffix(t.User, "#assignee")
			roleReferences, err := tm.ListWithKey(ctx, &openfgav1.ReadRequestTupleKey{Object: role})
			if err != nil {
				return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("listing tuples for role %s: %w", role, err), true, true)
			}
			accountTuples = append(accountTuples, roleReferences...)
		}
	}

	// Delete all collected tuples.
	if err := tm.Delete(ctx, accountTuples); err != nil {
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

func NewAccountTuplesSubroutine(mcc mcclient.ClusterClient, mgr mcmanager.Manager, fga openfgav1.OpenFGAServiceClient, storeIDGetter fga.StoreIDGetter, creatorRelation, parentRelation, objectType string) *AccountTuplesSubroutine {
	return &AccountTuplesSubroutine{
		mgr:             mgr,
		mcc:             mcc,
		fga:             fga,
		storeIDGetter:   storeIDGetter,
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

// clusterAndIDFromLogicalClusterForPath retrieves the LogicalCluster of a given
// path and returns its cluster ID and the LogicalCluster object.
func clusterAndIDFromLogicalClusterForPath(ctx context.Context, mgr mcmanager.Manager, p logicalcluster.Path) (string, kcpcorev1alpha1.LogicalCluster, error) {
	var lc kcpcorev1alpha1.LogicalCluster

	clusterClient, err := iclient.NewForLogicalCluster(mgr.GetLocalManager().GetConfig(), mgr.GetLocalManager().GetScheme(), logicalcluster.Name(p.String()))
	if err != nil {
		return "", lc, fmt.Errorf("getting account cluster client: %w", err)
	}
	if err := clusterClient.Get(ctx, client.ObjectKey{
		Name: "cluster",
	}, &lc); err != nil {
		return "", lc, fmt.Errorf("getting account's LogicalCluster: %w", err)
	}

	clusterID, ok := lc.Annotations["kcp.io/cluster"]
	if !ok || clusterID == "" {
		return "", lc, fmt.Errorf("cluster-annotation kcp.io/cluster on LogicalCluster is not set")
	}

	return clusterID, lc, nil
}

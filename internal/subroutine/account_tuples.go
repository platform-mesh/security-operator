package subroutine

import (
	"context"
	"fmt"
	"strings"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	accountsv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/security-operator/api/v1alpha1"
	iclient "github.com/platform-mesh/security-operator/internal/client"
	"github.com/platform-mesh/security-operator/internal/fga"
	platformmeshpath "github.com/platform-mesh/security-operator/internal/platformmesh"
	"github.com/platform-mesh/subroutines"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

// AccountTuplesSubroutine creates FGA tuples for Accounts not of the
// "org"-type when initializing, and deletes them when terminating.
type AccountTuplesSubroutine struct {
	mgr             mcmanager.Manager
	fga             openfgav1.OpenFGAServiceClient
	storeIDGetter   fga.StoreIDGetter
	objectType      string
	parentRelation  string
	creatorRelation string
	kcpClientGetter iclient.KCPClientGetter
}

// Process implements subroutines.Processor.
func (s *AccountTuplesSubroutine) Process(ctx context.Context, obj client.Object) (subroutines.Result, error) {
	return s.reconcile(ctx, obj)
}

// Initialize implements subroutines.Initializer.
func (s *AccountTuplesSubroutine) Initialize(ctx context.Context, obj client.Object) (subroutines.Result, error) {
	return s.reconcile(ctx, obj)
}

func (s *AccountTuplesSubroutine) reconcile(ctx context.Context, obj client.Object) (subroutines.Result, error) {
	lc := obj.(*kcpcorev1alpha1.LogicalCluster)

	if _, err := platformmeshpath.NewAccountPathFromLogicalCluster(lc); err != nil {
		return subroutines.OK(), fmt.Errorf("getting AccountPath from LogicalCluster: %w", err)
	}

	accountInfo, err := s.getLocalAccountInfo(ctx)
	if err != nil {
		return subroutines.OK(), err
	}
	if accountInfo.Spec.ParentAccount == nil {
		return subroutines.OK(), fmt.Errorf("parent account is not set on AccountInfo")
	}
	if accountInfo.Spec.Account.Creator == nil || *accountInfo.Spec.Account.Creator == "" {
		return subroutines.OK(), fmt.Errorf("account creator is nil or empty")
	}

	storeID, err := s.storeIDGetter.Get(ctx, accountInfo.Spec.Organization.Name)
	if err != nil {
		return subroutines.OK(), fmt.Errorf("getting store ID: %w", err)
	}

	tuples, err := fga.InitialTuplesForAccount(fga.InitialTuplesForAccountInput{
		BaseTuplesInput: fga.BaseTuplesInput{
			Creator:                *accountInfo.Spec.Account.Creator,
			AccountOriginClusterID: accountInfo.Spec.ParentAccount.GeneratedClusterId,
			AccountName:            accountInfo.Spec.Account.Name,
			CreatorRelation:        s.creatorRelation,
			ObjectType:             s.objectType,
		},
		ParentOriginClusterID: accountInfo.Spec.ParentAccount.OriginClusterId,
		ParentName:            accountInfo.Spec.ParentAccount.Name,
		ParentRelation:        s.parentRelation,
	})
	if err != nil {
		return subroutines.OK(), fmt.Errorf("building tuples for account: %w", err)
	}
	if err := fga.NewTupleManager(s.fga, storeID, fga.AuthorizationModelIDLatest, logger.LoadLoggerFromContext(ctx)).Apply(ctx, tuples); err != nil {
		return subroutines.OK(), fmt.Errorf("applying tuples for Account: %w", err)
	}

	return subroutines.OK(), nil
}

// Terminate implements subroutines.Terminator.
func (s *AccountTuplesSubroutine) Terminate(ctx context.Context, obj client.Object) (subroutines.Result, error) {
	lc := obj.(*kcpcorev1alpha1.LogicalCluster)

	accountPath, err := platformmeshpath.NewAccountPathFromLogicalCluster(lc)
	if err != nil {
		return subroutines.OK(), fmt.Errorf("getting AccountPath from LogicalCluster: %w", err)
	}
	accountInfo, err := s.getLocalAccountInfo(ctx)
	if err != nil {
		return subroutines.OK(), err
	}
	if accountInfo.Spec.ParentAccount == nil {
		return subroutines.OK(), fmt.Errorf("parent account is not set on AccountInfo")
	}

	parentClusterID := accountInfo.Spec.ParentAccount.GeneratedClusterId
	storeID, err := s.storeIDGetter.Get(ctx, accountInfo.Spec.Organization.Name)
	if err != nil {
		return subroutines.OK(), fmt.Errorf("getting store ID: %w", err)
	}

	// List tuples that reference the account.
	tm := fga.NewTupleManager(s.fga, storeID, fga.AuthorizationModelIDLatest, logger.LoadLoggerFromContext(ctx))
	accountReferenceTuples, err := tm.ListWithKey(ctx, fga.ReferencingAccountTupleKey(s.objectType, parentClusterID, accountPath.Base()))
	if err != nil {
		return subroutines.OK(), fmt.Errorf("listing tuples referencing Account: %w", err)
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
				return subroutines.OK(), fmt.Errorf("listing tuples for role %s: %w", role, err)
			}
			accountTuples = append(accountTuples, roleReferences...)
		}
	}

	// Delete all collected tuples.
	if err := tm.Delete(ctx, accountTuples); err != nil {
		return subroutines.OK(), fmt.Errorf("deleting tuples for Account: %w", err)
	}

	return subroutines.OK(), nil
}

// GetName implements subroutines.Subroutine.
func (s *AccountTuplesSubroutine) GetName() string { return "AccountTuplesSubroutine" }

func NewAccountTuplesSubroutine(mgr mcmanager.Manager, fga openfgav1.OpenFGAServiceClient, storeIDGetter fga.StoreIDGetter, creatorRelation, parentRelation, objectType string, kcpHelper iclient.KCPClientGetter) *AccountTuplesSubroutine {
	return &AccountTuplesSubroutine{
		mgr:             mgr,
		fga:             fga,
		storeIDGetter:   storeIDGetter,
		creatorRelation: creatorRelation,
		parentRelation:  parentRelation,
		objectType:      objectType,
		kcpClientGetter: kcpHelper,
	}
}

var (
	_ subroutines.Initializer = &AccountTuplesSubroutine{}
	_ subroutines.Processor   = &AccountTuplesSubroutine{}
	_ subroutines.Terminator  = &AccountTuplesSubroutine{}
)

func (s *AccountTuplesSubroutine) getLocalAccountInfo(ctx context.Context) (*accountsv1alpha1.AccountInfo, error) {
	cluster, err := s.mgr.ClusterFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster from context: %w", err)
	}

	var accountInfo accountsv1alpha1.AccountInfo
	if err := cluster.GetClient().Get(ctx, client.ObjectKey{Name: "account"}, &accountInfo); err != nil {
		return nil, fmt.Errorf("getting local AccountInfo: %w", err)
	}

	return &accountInfo, nil
}

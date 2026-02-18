package subroutine

import (
	"context"
	"fmt"

	accountsv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/golang-commons/errors"
	iclient "github.com/platform-mesh/security-operator/internal/client"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/kcp-dev/logicalcluster/v3"
	kcpcore "github.com/kcp-dev/sdk/apis/core"
	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
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
		return accountsv1alpha1.Account{}, accountsv1alpha1.AccountInfo{}, errors.NewOperatorError(fmt.Errorf("AccountInfo not found yet, requeueing"), true, false)
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

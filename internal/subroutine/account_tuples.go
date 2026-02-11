package subroutine

import (
	"context"
	"fmt"
	"slices"

	accountsv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/security-operator/api/v1alpha1"
	iclient "github.com/platform-mesh/security-operator/internal/client"
	"github.com/platform-mesh/security-operator/pkg/fga"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	kerrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/kcp-dev/logicalcluster/v3"
	mcclient "github.com/kcp-dev/multicluster-provider/client"
	kcpcore "github.com/kcp-dev/sdk/apis/core"
	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

// AccountTuplesSubroutine creates FGA tuples for Accounts not of the
// "org"-type.
type AccountTuplesSubroutine struct {
	mgr mcmanager.Manager
	mcc mcclient.ClusterClient

	objectType      string
	parentRelation  string
	creatorRelation string
}

// Process implements lifecycle.Subroutine.
func (s *AccountTuplesSubroutine) Process(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)

	lc := instance.(*kcpcorev1alpha1.LogicalCluster)
	p := lc.Annotations[kcpcore.LogicalClusterPathAnnotationKey]
	if p == "" {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("annotation on LogicalCluster is not set"), true, true)
	}
	lcID, _ := mccontext.ClusterFrom(ctx)
	log = log.ChildLogger("ID", lcID).ChildLogger("path", p)
	log.Info().Msgf("Processing logical cluster")

	// The AccountInfo in the logical custer belongs to the Account the
	// Workspace was created for
	lcClient, err := iclient.NewForLogicalCluster(s.mgr.GetLocalManager().GetConfig(), s.mgr.GetLocalManager().GetScheme(), logicalcluster.Name(lcID))
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("getting client: %w", err), true, true)
	}
	var ai accountsv1alpha1.AccountInfo
	if err := lcClient.Get(ctx, client.ObjectKey{
		Name: "account",
	}, &ai); err != nil && !kerrors.IsNotFound(err) {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("getting AccountInfo for LogicalCluster: %w", err), true, true)
	} else if kerrors.IsNotFound(err) {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("AccountInfo not found yet, requeueing"), true, false)
	}

	// The actual Account resource belonging to the Workspace needs to be
	// fetched from the parent Account's Workspace
	parentAccountClient, err := iclient.NewForLogicalCluster(s.mgr.GetLocalManager().GetConfig(), s.mgr.GetLocalManager().GetScheme(), logicalcluster.Name(ai.Spec.ParentAccount.Path))
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("getting parent account cluster client: %w", err), true, true)
	}
	var acc accountsv1alpha1.Account
	if err := parentAccountClient.Get(ctx, client.ObjectKey{
		Name: ai.Spec.Account.Name,
	}, &acc); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("getting Account in parent account cluster: %w", err), true, true)
	}

	// The Store to be updated belongs the parent Organization(which is not
	// necessarily the parent Account!) but exists in the "orgs" Workspace
	orgsClient, err := iclient.NewForLogicalCluster(s.mgr.GetLocalManager().GetConfig(), s.mgr.GetLocalManager().GetScheme(), logicalcluster.Name("root:orgs"))
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("getting orgs client: %w", err), true, true)
	}
	var st v1alpha1.Store
	if err := orgsClient.Get(ctx, client.ObjectKey{
		Name: ai.Spec.Organization.Name,
	}, &st); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("getting parent organisation's Store: %w", err), true, true)
	}

	// Append the stores tuples with every tuple for the Account not yet managed
	// via the Store resource
	tuples := fga.TuplesForAccount(acc, ai, s.creatorRelation, s.parentRelation, s.objectType)
	var changed bool
	for _, t := range tuples {
		if !slices.Contains(st.Spec.Tuples, t) {
			st.Spec.Tuples = append(st.Spec.Tuples, t)
			changed = true
		}
	}

	// Potentially update Store and requeue to wait for update
	// todo(simontesar): we could be watching Stores instead
	if changed {
		if err := orgsClient.Update(ctx, &st); err != nil {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("updating Store with tuples: %w", err), true, true)
		}

		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("store needed to be updated, requeueing"), true, false)
	}

	// Check if Store applied tuple changes
	for _, t := range tuples {
		if !slices.Contains(st.Status.ManagedTuples, t) {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("store does not yet contain all specified tuples, requeueing"), true, false)
		}
	}

	// todo(simontesar): checking and waiting for Readiness is currently futile,
	// our conditions don't include the observed generation
	// if conditions.IsPresentAndEqualForGeneration(st.Status.Conditions, lcconditions.ConditionReady, metav1.ConditionTrue, st.GetObjectMeta().GetGeneration()) {
	// 	return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("store %s is not ready", st.Name), true, false)
	// }

	return ctrl.Result{}, nil
}

// Finalize implements lifecycle.Subroutine.
func (s *AccountTuplesSubroutine) Finalize(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)

	lc := instance.(*kcpcorev1alpha1.LogicalCluster)
	p := lc.Annotations[kcpcore.LogicalClusterPathAnnotationKey]
	if p == "" {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("annotation on LogicalCluster is not set"), true, true)
	}
	log.Info().Msgf("Finalizing logical cluster of path %s", p)

	return ctrl.Result{}, nil
}

// Finalizers implements lifecycle.Subroutine.
func (s *AccountTuplesSubroutine) Finalizers(_ runtimeobject.RuntimeObject) []string {
	return []string{"core.platform-mesh.io/account-fga-tuples"}
}

// GetName implements lifecycle.Subroutine.
func (s *AccountTuplesSubroutine) GetName() string { return "AccountTuplesSubroutine" }

func NewAccountTuplesSubroutine(mcc mcclient.ClusterClient, mgr mcmanager.Manager, creatorRelation, parentRelation, objectType string) *AccountTuplesSubroutine {
	return &AccountTuplesSubroutine{
		mgr:             mgr,
		mcc:             mcc,
		creatorRelation: creatorRelation,
		parentRelation:  parentRelation,
		objectType:      objectType,
	}
}

var _ lifecyclesubroutine.Subroutine = &AccountTuplesSubroutine{}

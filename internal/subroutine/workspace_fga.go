package subroutine

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	kcpv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	accountsv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/account-operator/pkg/subroutines/accountinfo"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/fga/helpers"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
)

type workspaceFGASubroutine struct {
	mgr           mcmanager.Manager
	fga           openfgav1.OpenFGAServiceClient
	fgaObjectType string
	fgaParentRel  string
	fgaCreatorRel string
}

func NewWorkspaceFGASubroutine(mgr mcmanager.Manager, fga openfgav1.OpenFGAServiceClient, objectType, parentRel, creatorRel string) *workspaceFGASubroutine {
	return &workspaceFGASubroutine{
		mgr:           mgr,
		fga:           fga,
		fgaObjectType: objectType,
		fgaParentRel:  parentRel,
		fgaCreatorRel: creatorRel,
	}
}

var _ lifecyclesubroutine.Subroutine = &workspaceFGASubroutine{}

func (w *workspaceFGASubroutine) GetName() string { return "WorkspaceFGA" }

func (w *workspaceFGASubroutine) Finalizers(_ runtimeobject.RuntimeObject) []string { return nil }

func (w *workspaceFGASubroutine) Finalize(ctx context.Context, _ runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil
}

func (w *workspaceFGASubroutine) Process(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	if w.fga == nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("OpenFGA client is nil"), false, false)
	}

	_ = instance.(*kcpv1alpha1.LogicalCluster)

	clusterRef, err := w.mgr.ClusterFromContext(ctx)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to get cluster from context: %w", err), true, false)
	}
	workspaceClient := clusterRef.GetClient()

	ctxWithTimeout, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	accountInfo := &accountsv1alpha1.AccountInfo{}
	if err := workspaceClient.Get(ctxWithTimeout, client.ObjectKey{Name: accountinfo.DefaultAccountInfoName}, accountInfo); err != nil {
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}
	if accountInfo.Spec.Account.Name == "" || accountInfo.Spec.Account.OriginClusterId == "" || accountInfo.Spec.FGA.Store.Id == "" {
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}

	// Parent relation for non-org accounts
	if accountInfo.Spec.ParentAccount != nil {
		parent := accountInfo.Spec.ParentAccount
		if err := w.writeTuple(ctxWithTimeout, accountInfo.Spec.FGA.Store.Id, &openfgav1.TupleKey{
			User:     fmt.Sprintf("%s:%s/%s", w.fgaObjectType, parent.OriginClusterId, parent.Name),
			Relation: w.fgaParentRel,
			Object:   fmt.Sprintf("%s:%s/%s", w.fgaObjectType, accountInfo.Spec.Account.OriginClusterId, accountInfo.Spec.Account.Name),
		}); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Owner/creator relations: write only once using creator from AccountInfo
	if accountInfo.Spec.Creator != nil && *accountInfo.Spec.Creator != "" && !accountInfo.Status.CreatorTupleWritten {
		creator := *accountInfo.Spec.Creator
		normalized := formatUser(creator)
		if !validateCreator(normalized) {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("creator string is in the protected service account prefix range"), false, false)
		}
		if err := w.writeTuple(ctxWithTimeout, accountInfo.Spec.FGA.Store.Id, &openfgav1.TupleKey{
			User:     fmt.Sprintf("user:%s", normalized),
			Relation: "assignee",
			Object:   fmt.Sprintf("role:%s/%s/%s/owner", w.fgaObjectType, accountInfo.Spec.Account.OriginClusterId, accountInfo.Spec.Account.Name),
		}); err != nil {
			return ctrl.Result{}, err
		}
		if err := w.writeTuple(ctxWithTimeout, accountInfo.Spec.FGA.Store.Id, &openfgav1.TupleKey{
			User:     fmt.Sprintf("role:%s/%s/%s/owner#assignee", w.fgaObjectType, accountInfo.Spec.Account.OriginClusterId, accountInfo.Spec.Account.Name),
			Relation: w.fgaCreatorRel,
			Object:   fmt.Sprintf("%s:%s/%s", w.fgaObjectType, accountInfo.Spec.Account.OriginClusterId, accountInfo.Spec.Account.Name),
		}); err != nil {
			return ctrl.Result{}, err
		}

		// Mark creator tuple as written
		accountInfo.Status.CreatorTupleWritten = true
		if err := workspaceClient.Status().Update(ctxWithTimeout, accountInfo); err != nil {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to update accountInfo status: %w", err), true, true)
		}
	}

	return ctrl.Result{}, nil
}

func (w *workspaceFGASubroutine) writeTuple(ctx context.Context, storeID string, tuple *openfgav1.TupleKey) errors.OperatorError {
	_, err := w.fga.Write(ctx, &openfgav1.WriteRequest{
		StoreId: storeID,
		Writes: &openfgav1.WriteRequestWrites{
			TupleKeys: []*openfgav1.TupleKey{tuple},
		},
	})
	if helpers.IsDuplicateWriteError(err) {
		return nil
	}
	if err != nil {
		return errors.NewOperatorError(fmt.Errorf("unable to write FGA tuple: %w", err), true, true)
	}
	return nil
}

var saRegex = regexp.MustCompile(`^system:serviceaccount:[^:]*:[^:]*$`)

func formatUser(user string) string {
	if saRegex.MatchString(user) {
		return strings.ReplaceAll(user, ":", ".")
	}
	return user
}

func validateCreator(creator string) bool {
	if strings.HasPrefix(creator, "system:serviceaccount:") {
		return false
	}
	if strings.HasPrefix(creator, "system.serviceaccount.") {
		return false
	}
	return true
}

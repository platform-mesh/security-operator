package subroutine

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	kcpv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"
	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	accountsv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/account-operator/pkg/subroutines/accountinfo"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/fga/helpers"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/config"
)

const (
	annotationOwnerWritten  = "security-operator.platform-mesh.io/fga-owner-written"
	annotationParentWritten = "security-operator.platform-mesh.io/fga-parent-written"
)

func NewWorkspaceInitializer(orgsClient client.Client, cfg config.Config, mgr mcmanager.Manager, fga openfgav1.OpenFGAServiceClient) *workspaceInitializer {
	coreModulePath := cfg.CoreModulePath

	data, err := os.ReadFile(coreModulePath)
	if err != nil {
		panic(err)
	}

	return &workspaceInitializer{
		orgsClient:    orgsClient,
		mgr:           mgr,
		coreModule:    string(data),
		fga:           fga,
		fgaObjectType: cfg.FGA.ObjectType,
		fgaParentRel:  cfg.FGA.ParentRelation,
		fgaCreatorRel: cfg.FGA.CreatorRelation,
	}
}

var _ lifecyclesubroutine.Subroutine = &workspaceInitializer{}

type workspaceInitializer struct {
	orgsClient    client.Client
	mgr           mcmanager.Manager
	coreModule    string
	fga           openfgav1.OpenFGAServiceClient
	fgaObjectType string
	fgaParentRel  string
	fgaCreatorRel string
}

func (w *workspaceInitializer) Finalize(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	// Finalization handled by dedicated subroutine.
	return ctrl.Result{}, nil
}

func (w *workspaceInitializer) Finalizers(_ runtimeobject.RuntimeObject) []string {
	return nil
}

func (w *workspaceInitializer) GetName() string { return "WorkspaceInitializer" }

func (w *workspaceInitializer) Process(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	lc := instance.(*kcpv1alpha1.LogicalCluster)

	clusterRef, err := w.mgr.ClusterFromContext(ctx)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to get cluster from context: %w", err), true, false)
	}
	workspaceClient := clusterRef.GetClient()

	ownerClusterName := logicalcluster.Name(lc.Spec.Owner.Cluster)
	ownerClusterRef, err := w.mgr.GetCluster(ctx, ownerClusterName.String())
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to get owner cluster: %w", err), true, true)
	}

	var account accountsv1alpha1.Account
	if err := ownerClusterRef.GetClient().Get(ctx, client.ObjectKey{Name: lc.Spec.Owner.Name}, &account); err != nil {
		if kerrors.IsNotFound(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to get owner account: %w", err), true, true)
	}

	ctxWithTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	accountInfo := &accountsv1alpha1.AccountInfo{}
	if err := workspaceClient.Get(ctxWithTimeout, client.ObjectKey{Name: accountinfo.DefaultAccountInfoName}, accountInfo); err != nil {
		if kerrors.IsNotFound(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to get accountInfo: %w", err), true, true)
	}

	if accountInfo.Spec.Account.Name == "" || accountInfo.Spec.Account.OriginClusterId == "" {
		return ctrl.Result{Requeue: true}, nil
	}

	storeClusterName, storeName, opErr := w.resolveStoreTarget(lc, account, accountInfo)
	if opErr != nil {
		return ctrl.Result{}, opErr
	}

	ctxStore := mccontext.WithCluster(ctxWithTimeout, storeClusterName.String())
	allowCreate := account.Spec.Type == accountsv1alpha1.AccountTypeOrg

	store := &v1alpha1.Store{}
	if err := w.orgsClient.Get(ctxStore, client.ObjectKey{Name: storeName}, store); err != nil {
		if kerrors.IsNotFound(err) && allowCreate {
			store = &v1alpha1.Store{ObjectMeta: metav1.ObjectMeta{Name: storeName}}
		} else if kerrors.IsNotFound(err) {
			return ctrl.Result{Requeue: true}, nil
		} else {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to get store: %w", err), true, true)
		}
	}

	_, err = controllerutil.CreateOrUpdate(ctxStore, w.orgsClient, store, func() error {
		if allowCreate {
			store.Spec.CoreModule = w.coreModule
		}
		return nil
	})
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to create/update store: %w", err), true, true)
	}

	if err := w.orgsClient.Get(ctxStore, client.ObjectKey{Name: storeName}, store); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to refresh store status: %w", err), true, true)
	}

	if store.Status.StoreID == "" {
		return ctrl.Result{Requeue: true}, nil
	}

	if accountInfo.Spec.FGA.Store.Id != store.Status.StoreID {
		if opErr := w.ensureAccountInfoStoreID(ctxWithTimeout, workspaceClient, store.Status.StoreID); opErr != nil {
			return ctrl.Result{}, opErr
		}
	}

	annotations := lc.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	ownerWritten := annotations[annotationOwnerWritten] == "true"
	parentWritten := annotations[annotationParentWritten] == "true"

	if account.Spec.Type != accountsv1alpha1.AccountTypeOrg && !parentWritten {
		parentAccount := accountInfo.Spec.ParentAccount
		if parentAccount == nil || parentAccount.Name == "" || parentAccount.OriginClusterId == "" {
			return ctrl.Result{Requeue: true}, nil
		}
		if err := w.writeTuple(ctxWithTimeout, store.Status.StoreID, &openfgav1.TupleKey{
			User:     fmt.Sprintf("%s:%s/%s", w.fgaObjectType, parentAccount.OriginClusterId, parentAccount.Name),
			Relation: w.fgaParentRel,
			Object:   fmt.Sprintf("%s:%s/%s", w.fgaObjectType, accountInfo.Spec.Account.OriginClusterId, account.Name),
		}); err != nil {
			return ctrl.Result{}, err
		}
		parentWritten = true
	}

	if !ownerWritten && account.Spec.Creator != nil {
		if !validateCreator(*account.Spec.Creator) {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("creator string is in the protected service account prefix range"), false, false)
		}
		creator := formatUser(*account.Spec.Creator)

		if err := w.writeTuple(ctxWithTimeout, store.Status.StoreID, &openfgav1.TupleKey{
			User:     fmt.Sprintf("user:%s", creator),
			Relation: "assignee",
			Object:   fmt.Sprintf("role:%s/%s/%s/owner", w.fgaObjectType, accountInfo.Spec.Account.OriginClusterId, account.Name),
		}); err != nil {
			return ctrl.Result{}, err
		}
		if err := w.writeTuple(ctxWithTimeout, store.Status.StoreID, &openfgav1.TupleKey{
			User:     fmt.Sprintf("role:%s/%s/%s/owner#assignee", w.fgaObjectType, accountInfo.Spec.Account.OriginClusterId, account.Name),
			Relation: w.fgaCreatorRel,
			Object:   fmt.Sprintf("%s:%s/%s", w.fgaObjectType, accountInfo.Spec.Account.OriginClusterId, account.Name),
		}); err != nil {
			return ctrl.Result{}, err
		}
		ownerWritten = true
	}

	if err := w.updateAnnotations(ctx, clusterRef.GetClient(), lc, ownerWritten, parentWritten); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (w *workspaceInitializer) ensureAccountInfoStoreID(ctx context.Context, workspaceClient client.Client, storeID string) errors.OperatorError {
	accountInfo := &accountsv1alpha1.AccountInfo{ObjectMeta: metav1.ObjectMeta{Name: accountinfo.DefaultAccountInfoName}}
	_, err := controllerutil.CreateOrUpdate(ctx, workspaceClient, accountInfo, func() error {
		accountInfo.Spec.FGA.Store.Id = storeID
		return nil
	})
	if err != nil {
		return errors.NewOperatorError(fmt.Errorf("unable to create/update accountInfo: %w", err), true, true)
	}
	return nil
}

func (w *workspaceInitializer) resolveStoreTarget(lc *kcpv1alpha1.LogicalCluster, account accountsv1alpha1.Account, accountInfo *accountsv1alpha1.AccountInfo) (logicalcluster.Name, string, errors.OperatorError) {
	if account.Spec.Type == accountsv1alpha1.AccountTypeOrg {
		path, ok := lc.Annotations["kcp.io/path"]
		if !ok {
			return "", "", errors.NewOperatorError(fmt.Errorf("unable to get workspace path"), true, false)
		}
		return logicalcluster.Name(path), generateStoreName(lc), nil
	}

	if accountInfo.Spec.Organization.Path == "" {
		return "", "", errors.NewOperatorError(fmt.Errorf("organization path not yet set"), true, false)
	}
	storeName := generateStoreNameFromPath(accountInfo.Spec.Organization.Path)
	if storeName == "" {
		return "", "", errors.NewOperatorError(fmt.Errorf("unable to derive store name from organization path"), true, false)
	}
	if accountInfo.Spec.ParentAccount == nil || accountInfo.Spec.ParentAccount.Name == "" || accountInfo.Spec.ParentAccount.OriginClusterId == "" {
		return "", "", errors.NewOperatorError(fmt.Errorf("parent account information not ready"), true, false)
	}

	return logicalcluster.Name(accountInfo.Spec.Organization.Path), storeName, nil
}

func generateStoreName(lc *kcpv1alpha1.LogicalCluster) string {
	if path, ok := lc.Annotations["kcp.io/path"]; ok {
		pathElements := strings.Split(path, ":")
		return pathElements[len(pathElements)-1]
	}
	return ""
}

func generateStoreNameFromPath(path string) string {
	pathElements := strings.Split(path, ":")
	if len(pathElements) == 0 {
		return ""
	}
	return pathElements[len(pathElements)-1]
}

var saRegex = regexp.MustCompile(`^system:serviceaccount:[^:]*:[^:]*$`)

func formatUser(user string) string {
	if saRegex.MatchString(user) {
		return strings.ReplaceAll(user, ":", ".")
	}
	return user
}

func validateCreator(creator string) bool {
	return !strings.HasPrefix(creator, "system.serviceaccount")
}

func (w *workspaceInitializer) writeTuple(ctx context.Context, storeID string, tuple *openfgav1.TupleKey) errors.OperatorError {
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

func (w *workspaceInitializer) updateAnnotations(ctx context.Context, cl client.Client, lc *kcpv1alpha1.LogicalCluster, ownerWritten, parentWritten bool) errors.OperatorError {
	currentOwner := lc.GetAnnotations()[annotationOwnerWritten] == "true"
	currentParent := lc.GetAnnotations()[annotationParentWritten] == "true"

	if currentOwner == ownerWritten && currentParent == parentWritten {
		return nil
	}

	original := lc.DeepCopy()
	if lc.Annotations == nil {
		lc.Annotations = map[string]string{}
	}
	if ownerWritten {
		lc.Annotations[annotationOwnerWritten] = "true"
	}
	if parentWritten {
		lc.Annotations[annotationParentWritten] = "true"
	}

	if err := cl.Patch(ctx, lc, client.MergeFrom(original)); err != nil {
		return errors.NewOperatorError(fmt.Errorf("unable to patch logical cluster annotations: %w", err), true, true)
	}
	return nil
}

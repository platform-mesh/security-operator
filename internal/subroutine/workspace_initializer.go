package subroutine

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	kcpv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"
	accountsv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/account-operator/pkg/subroutines/accountinfo"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
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

func NewWorkspaceInitializer(orgsClient client.Client, cfg config.Config, mgr mcmanager.Manager) *workspaceInitializer {
	coreModulePath := cfg.CoreModulePath

	data, err := os.ReadFile(coreModulePath)
	if err != nil {
		panic(err)
	}

	return &workspaceInitializer{
		orgsClient: orgsClient,
		mgr:        mgr,
		coreModule: string(data),
	}
}

var _ lifecyclesubroutine.Subroutine = &workspaceInitializer{}

type workspaceInitializer struct {
	orgsClient client.Client
	mgr        mcmanager.Manager
	coreModule string
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

	// Validate that owner cluster is specified before getting workspace client
	if lc.Spec.Owner.Cluster == "" {
		return ctrl.Result{}, errors.NewOperatorError(
			fmt.Errorf("spec.owner.cluster is empty for LogicalCluster %s", lc.Name),
			true, true)
	}

	clusterRef, err := w.mgr.ClusterFromContext(ctx)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to get cluster from context: %w", err), true, false)
	}
	workspaceClient := clusterRef.GetClient()

	// Use orgsClient directly since lc.Spec.Owner.Cluster contains short cluster ID
	// which cannot be resolved via mgr.GetCluster()
	var account accountsv1alpha1.Account
	if err := w.orgsClient.Get(ctx, client.ObjectKey{Name: lc.Spec.Owner.Name}, &account); err != nil {
		if kerrors.IsNotFound(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to get owner account: %w", err), true, true)
	}

	// Ensure AccountInfo exists (create if missing) so account-operator can populate it
	accountInfo := &accountsv1alpha1.AccountInfo{ObjectMeta: metav1.ObjectMeta{Name: accountinfo.DefaultAccountInfoName}}
	_, err = controllerutil.CreateOrUpdate(ctx, workspaceClient, accountInfo, func() error {
		// Set Creator immediately when creating AccountInfo to avoid race with account-operator
		if accountInfo.Spec.Creator == nil && account.Spec.Creator != nil {
			creatorValue := *account.Spec.Creator
			accountInfo.Spec.Creator = &creatorValue
		}
		return nil
	})
	if err != nil {
		// If APIBinding not ready yet, return error to retry whole reconcile
		if strings.Contains(err.Error(), "no matches for kind") {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("apiBinding not ready: %w", err), true, false)
		}
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to ensure accountInfo exists: %w", err), true, true)
	}

	// Only create Store for org accounts during initialization
	// For account-type accounts, Store already exists in parent org
	if account.Spec.Type != accountsv1alpha1.AccountTypeOrg {
		// Re-fetch AccountInfo to get latest state populated by account-operator
		accountInfo = &accountsv1alpha1.AccountInfo{ObjectMeta: metav1.ObjectMeta{Name: accountinfo.DefaultAccountInfoName}}
		if err := workspaceClient.Get(ctx, client.ObjectKey{Name: accountinfo.DefaultAccountInfoName}, accountInfo); err != nil {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to get accountInfo: %w", err), true, true)
		}

		// Wait for account-operator to populate organization path
		if accountInfo.Spec.Organization.Path == "" {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}

		// Resolve parent org's Store name from organization path
		storeName := generateStoreNameFromPath(accountInfo.Spec.Organization.Path)
		if storeName == "" {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to derive store name from organization path"), true, false)
		}
		storeClusterName := logicalcluster.Name(accountInfo.Spec.Organization.Path)

		ctxStore := mccontext.WithCluster(ctx, storeClusterName.String())

		// Get parent org's Store
		store := &v1alpha1.Store{}
		if err := w.orgsClient.Get(ctxStore, client.ObjectKey{Name: storeName}, store); err != nil {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to get parent org store: %w", err), true, true)
		}

		if store.Status.StoreID == "" {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}

		// Update AccountInfo with parent org's Store ID
		accountInfo = &accountsv1alpha1.AccountInfo{ObjectMeta: metav1.ObjectMeta{Name: accountinfo.DefaultAccountInfoName}}
		_, err = controllerutil.CreateOrUpdate(ctx, workspaceClient, accountInfo, func() error {
			accountInfo.Spec.FGA.Store.Id = store.Status.StoreID
			// Also set Creator if available
			if account.Spec.Creator != nil {
				creatorValue := *account.Spec.Creator
				accountInfo.Spec.Creator = &creatorValue
			}
			return nil
		})
		if err != nil {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to update accountInfo with store ID: %w", err), true, true)
		}

		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// Resolve Store name and location for org accounts
	path, ok := lc.Annotations["kcp.io/path"]
	if !ok {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to get workspace path"), true, false)
	}
	storeName := generateStoreName(lc)
	if storeName == "" {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to generate store name from workspace path"), true, false)
	}
	storeClusterName := logicalcluster.Name(path)

	ctxStore := mccontext.WithCluster(ctx, storeClusterName.String())

	// Create Store for org account
	store := &v1alpha1.Store{}
	if err := w.orgsClient.Get(ctxStore, client.ObjectKey{Name: storeName}, store); err != nil {
		if !kerrors.IsNotFound(err) {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to get store: %w", err), true, true)
		}
		// Store doesn't exist, create it
		store = &v1alpha1.Store{ObjectMeta: metav1.ObjectMeta{Name: storeName}}
	}

	_, err = controllerutil.CreateOrUpdate(ctxStore, w.orgsClient, store, func() error {
		store.Spec.CoreModule = w.coreModule
		return nil
	})
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to create/update store: %w", err), true, true)
	}

	// Re-fetch to get store status
	if err := w.orgsClient.Get(ctxStore, client.ObjectKey{Name: storeName}, store); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to refresh store status: %w", err), true, true)
	}

	if store.Status.StoreID == "" {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("store not ready"), true, false)
	}

	// Update AccountInfo with Store ID and Creator
	accountInfo = &accountsv1alpha1.AccountInfo{ObjectMeta: metav1.ObjectMeta{Name: accountinfo.DefaultAccountInfoName}}
	_, err = controllerutil.CreateOrUpdate(ctx, workspaceClient, accountInfo, func() error {
		accountInfo.Spec.FGA.Store.Id = store.Status.StoreID
		// Copy creator value (not pointer) to avoid issues with pointer sharing
		if account.Spec.Creator != nil {
			creatorValue := *account.Spec.Creator
			accountInfo.Spec.Creator = &creatorValue
		}
		return nil
	})
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to update accountInfo: %w", err), true, true)
	}

	return ctrl.Result{RequeueAfter: time.Minute}, nil
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

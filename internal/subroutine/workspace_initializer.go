package subroutine

import (
	"context"
	"fmt"
	"os"
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

func NewWorkspaceInitializer(orgsClient client.Client, cfg config.Config, mgr mcmanager.Manager, fga openfgav1.OpenFGAServiceClient) *workspaceInitializer {
	coreModulePath := cfg.CoreModulePath

	data, err := os.ReadFile(coreModulePath)
	if err != nil {
		panic(err)
	}

	return &workspaceInitializer{
		orgsClient: orgsClient,
		mgr:        mgr,
		coreModule: string(data),
		fga:        fga,
	}
}

var _ lifecyclesubroutine.Subroutine = &workspaceInitializer{}

type workspaceInitializer struct {
	orgsClient client.Client
	mgr        mcmanager.Manager
	coreModule string
	fga        openfgav1.OpenFGAServiceClient
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

	if lc.Status.Phase != kcpv1alpha1.LogicalClusterPhaseInitializing {
		fmt.Printf("[DEBUG] Workspace phase=%s, ensuring resources remain consistent\n", lc.Status.Phase)
	}

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
			fmt.Printf("[DEBUG] Account %s not found yet, requeuing\n", lc.Spec.Owner.Name)
			return ctrl.Result{Requeue: true}, nil
		}
		fmt.Printf("[ERROR] Failed to get account %s: %v\n", lc.Spec.Owner.Name, err)
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to get owner account: %w", err), true, true)
	}
	fmt.Printf("[DEBUG] Successfully fetched account: %s, type: %s\n", account.Name, account.Spec.Type)

	// Ensure AccountInfo exists (create if missing) so account-operator can populate it
	fmt.Printf("[DEBUG] Creating/updating AccountInfo in workspace\n")
	accountInfo := &accountsv1alpha1.AccountInfo{ObjectMeta: metav1.ObjectMeta{Name: accountinfo.DefaultAccountInfoName}}
	op, err := controllerutil.CreateOrUpdate(ctx, workspaceClient, accountInfo, func() error {
		// Set Creator immediately when creating AccountInfo to avoid race with account-operator
		if accountInfo.Spec.Creator == nil && account.Spec.Creator != nil {
			creatorValue := *account.Spec.Creator
			accountInfo.Spec.Creator = &creatorValue
			fmt.Printf("[DEBUG] Setting Creator to: %s during AccountInfo creation\n", creatorValue)
		}
		return nil
	})
	if op != controllerutil.OperationResultNone {
		creatorVal := "<nil>"
		if accountInfo.Spec.Creator != nil {
			creatorVal = *accountInfo.Spec.Creator
		}
		fmt.Printf("[DEBUG] After CreateOrUpdate (op=%s): Creator=%s\n", op, creatorVal)
	}
	if err != nil {
		// If APIBinding not ready yet, return error to retry whole reconcile
		if strings.Contains(err.Error(), "no matches for kind") {
			fmt.Printf("[DEBUG] CRD not ready yet (no matches for kind), will retry reconcile\n")
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("apiBinding not ready: %w", err), true, false)
		}
		fmt.Printf("[ERROR] Failed to create/update AccountInfo: %v\n", err)
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to ensure accountInfo exists: %w", err), true, true)
	}
	fmt.Printf("[DEBUG] AccountInfo operation: %s\n", op)

	// Only create Store for org accounts during initialization
	// For account-type accounts, Store already exists in parent org
	if account.Spec.Type != accountsv1alpha1.AccountTypeOrg {
		fmt.Printf("[DEBUG] Account type is '%s', need to populate Store ID from parent org\n", account.Spec.Type)

		// Re-fetch AccountInfo to get latest state populated by account-operator
		accountInfo = &accountsv1alpha1.AccountInfo{ObjectMeta: metav1.ObjectMeta{Name: accountinfo.DefaultAccountInfoName}}
		if err := workspaceClient.Get(ctx, client.ObjectKey{Name: accountinfo.DefaultAccountInfoName}, accountInfo); err != nil {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to get accountInfo: %w", err), true, true)
		}

		// Wait for account-operator to populate organization path
		if accountInfo.Spec.Organization.Path == "" {
			fmt.Printf("[DEBUG] Organization path not yet set by account-operator, requeuing\n")
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}

		// Resolve parent org's Store name from organization path
		storeName := generateStoreNameFromPath(accountInfo.Spec.Organization.Path)
		if storeName == "" {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to derive store name from organization path"), true, false)
		}
		storeClusterName := logicalcluster.Name(accountInfo.Spec.Organization.Path)
		fmt.Printf("[DEBUG] Resolved parent org store: cluster=%s, name=%s\n", storeClusterName, storeName)

		ctxStore := mccontext.WithCluster(ctx, storeClusterName.String())

		// Get parent org's Store
		store := &v1alpha1.Store{}
		if err := w.orgsClient.Get(ctxStore, client.ObjectKey{Name: storeName}, store); err != nil {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to get parent org store: %w", err), true, true)
		}

		if store.Status.StoreID == "" {
			fmt.Printf("[DEBUG] Parent org Store not ready yet (StoreID empty), requeuing\n")
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		fmt.Printf("[DEBUG] Parent org Store ready with ID: %s\n", store.Status.StoreID)

		// Update AccountInfo with parent org's Store ID
		accountInfo = &accountsv1alpha1.AccountInfo{ObjectMeta: metav1.ObjectMeta{Name: accountinfo.DefaultAccountInfoName}}
		_, err = controllerutil.CreateOrUpdate(ctx, workspaceClient, accountInfo, func() error {
			accountInfo.Spec.FGA.Store.Id = store.Status.StoreID
			// Also set Creator if available
			if account.Spec.Creator != nil {
				creatorValue := *account.Spec.Creator
				accountInfo.Spec.Creator = &creatorValue
				fmt.Printf("[DEBUG] Setting Creator to: %s (from account.Spec.Creator)\n", creatorValue)
			}
			return nil
		})
		if err != nil {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to update accountInfo with store ID: %w", err), true, true)
		}
		fmt.Printf("[DEBUG] AccountInfo updated with parent org Store ID\n")

		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// Resolve Store name and location for org accounts
	fmt.Printf("[DEBUG] Resolving store for org account\n")
	path, ok := lc.Annotations["kcp.io/path"]
	if !ok {
		fmt.Printf("[ERROR] Missing kcp.io/path annotation\n")
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to get workspace path"), true, false)
	}
	storeName := generateStoreName(lc)
	if storeName == "" {
		fmt.Printf("[ERROR] Failed to generate store name from workspace path\n")
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to generate store name from workspace path"), true, false)
	}
	storeClusterName := logicalcluster.Name(path)
	fmt.Printf("[DEBUG] Resolved store: cluster=%s, name=%s\n", storeClusterName, storeName)

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

	coreModuleUpdated := false
	_, err = controllerutil.CreateOrUpdate(ctxStore, w.orgsClient, store, func() error {
		if store.Spec.CoreModule != w.coreModule {
			coreModuleUpdated = true
			store.Spec.CoreModule = w.coreModule
		}
		return nil
	})
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to create/update store: %w", err), true, true)
	}
	if coreModuleUpdated {
		fmt.Printf("[DEBUG] Store %s core module refreshed from ConfigMap contents\n", storeName)
	}

	// Re-fetch to get store status
	if err := w.orgsClient.Get(ctxStore, client.ObjectKey{Name: storeName}, store); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to refresh store status: %w", err), true, true)
	}

	if store.Status.StoreID == "" {
		fmt.Printf("[DEBUG] Store not ready yet (StoreID empty), will retry reconcile\n")
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("store not ready"), true, false)
	}
	fmt.Printf("[DEBUG] Store ready with ID: %s\n", store.Status.StoreID)

	// Update AccountInfo with Store ID and Creator
	fmt.Printf("[DEBUG] Updating AccountInfo with StoreID=%s and Creator\n", store.Status.StoreID)
	accountInfo = &accountsv1alpha1.AccountInfo{ObjectMeta: metav1.ObjectMeta{Name: accountinfo.DefaultAccountInfoName}}
	_, err = controllerutil.CreateOrUpdate(ctx, workspaceClient, accountInfo, func() error {
		accountInfo.Spec.FGA.Store.Id = store.Status.StoreID
		// Copy creator value (not pointer) to avoid issues with pointer sharing
		if account.Spec.Creator != nil {
			creatorValue := *account.Spec.Creator
			accountInfo.Spec.Creator = &creatorValue
			fmt.Printf("[DEBUG] Setting Creator to: %s (from account.Spec.Creator)\n", creatorValue)
		} else {
			fmt.Printf("[DEBUG] account.Spec.Creator is nil, not setting Creator\n")
		}
		return nil
	})
	if err != nil {
		fmt.Printf("[ERROR] Failed to update AccountInfo: %v\n", err)
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to update accountInfo: %w", err), true, true)
	}
	fmt.Printf("[DEBUG] AccountInfo updated successfully\n")

	fmt.Printf("[SUCCESS] WorkspaceInitializer completed successfully for org workspace\n")
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

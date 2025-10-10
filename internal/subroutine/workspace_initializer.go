package subroutine

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	kcpv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"
	accountsv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	lifecycleruntimeobject "github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"

	"github.com/platform-mesh/golang-commons/errors"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/kontext"

	"github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/config"
)

const initializerName = "root:security"

type workspaceClientFactoryFunc func(path string) (client.Client, error)

type workspaceInitializer struct {
	cl                     client.Client
	orgsClient             client.Client
	restCfg                *rest.Config
	coreModule             string
	fgaObjectType          string
	fgaParentRelation      string
	fgaCreatorRelation     string
	newWorkspaceClientFunc workspaceClientFactoryFunc
}

var _ lifecyclesubroutine.Subroutine = &workspaceInitializer{}

func NewWorkspaceInitializer(cl, orgsClient client.Client, restCfg *rest.Config, cfg config.Config) *workspaceInitializer {
	coreModulePath := cfg.CoreModulePath

	data, err := os.ReadFile(coreModulePath)
	if err != nil {
		panic(err)
	}

	wi := &workspaceInitializer{
		cl:                 cl,
		orgsClient:         orgsClient,
		restCfg:            restCfg,
		coreModule:         string(data),
		fgaObjectType:      cfg.FGA.ObjectType,
		fgaParentRelation:  cfg.FGA.ParentRelation,
		fgaCreatorRelation: cfg.FGA.CreatorRelation,
	}

	wi.newWorkspaceClientFunc = func(path string) (client.Client, error) {
		cfgCopy := rest.CopyConfig(restCfg)
		cfgCopy.Host = strings.ReplaceAll(cfgCopy.Host, "/services/initializingworkspaces/root:security", "/clusters/"+path)
		return client.New(cfgCopy, client.Options{Scheme: cl.Scheme()})
	}

	return wi
}

func (w *workspaceInitializer) Finalize(ctx context.Context, instance lifecycleruntimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil
}

func (w *workspaceInitializer) Finalizers() []string { return nil }

func (w *workspaceInitializer) GetName() string { return "WorkspaceInitializer" }

func (w *workspaceInitializer) Process(ctx context.Context, instance lifecycleruntimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	lc := instance.(*kcpv1alpha1.LogicalCluster)

	path, ok := lc.Annotations["kcp.io/path"]
	if !ok {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to get workspace path"), true, false)
	}

	account, opErr := w.getAccount(ctx, lc)
	if opErr != nil {
		return ctrl.Result{}, opErr
	}

	ctxWithTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	wsClient, err := w.newWorkspaceClientFunc(path)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to create workspace client: %w", err), true, true)
	}

	// Ensure the store resource exists for organizations so that status can be observed.
	storeName := ""
	if account.Spec.Type == accountsv1alpha1.AccountTypeOrg {
		storeName = generateStoreName(lc)
		store := &v1alpha1.Store{ObjectMeta: metav1.ObjectMeta{Name: storeName}}
		_, err = controllerutil.CreateOrUpdate(ctxWithTimeout, w.orgsClient, store, func() error {
			store.Spec.CoreModule = w.coreModule
			return nil
		})
		if err != nil {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to create/update store: %w", err), true, true)
		}

		if store.Status.StoreID == "" {
			return ctrl.Result{Requeue: true}, nil
		}

		if err := w.ensureAccountInfoStoreID(ctxWithTimeout, wsClient, store.Status.StoreID); err != nil {
			return ctrl.Result{}, err
		}
	}

	accountInfo := &accountsv1alpha1.AccountInfo{}
	if err := wsClient.Get(ctxWithTimeout, client.ObjectKey{Name: "account"}, accountInfo); err != nil {
		if kerrors.IsNotFound(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to get accountInfo: %w", err), true, true)
	}

	if accountInfo.Spec.Account.Name == "" || accountInfo.Spec.Account.OriginClusterId == "" {
		return ctrl.Result{Requeue: true}, nil
	}

	if accountInfo.Spec.FGA.Store.Id == "" {
		return ctrl.Result{Requeue: true}, nil
	}

	if account.Spec.Type == accountsv1alpha1.AccountTypeAccount {
		if accountInfo.Spec.ParentAccount == nil || accountInfo.Spec.ParentAccount.OriginClusterId == "" || accountInfo.Spec.ParentAccount.Name == "" {
			return ctrl.Result{Requeue: true}, nil
		}
		if accountInfo.Spec.Organization.Path == "" {
			return ctrl.Result{Requeue: true}, nil
		}
		storeName = generateStoreNameFromPath(accountInfo.Spec.Organization.Path)
		if storeName == "" {
			return ctrl.Result{Requeue: true}, nil
		}
	}

	store := &v1alpha1.Store{ObjectMeta: metav1.ObjectMeta{Name: storeName}}
	if err := w.orgsClient.Get(ctxWithTimeout, client.ObjectKeyFromObject(store), store); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to get store: %w", err), true, true)
	}

	if store.Status.StoreID == "" {
		return ctrl.Result{Requeue: true}, nil
	}

	if accountInfo.Spec.FGA.Store.Id != store.Status.StoreID {
		if err := w.ensureAccountInfoStoreID(ctxWithTimeout, wsClient, store.Status.StoreID); err != nil {
			return ctrl.Result{}, err
		}
	}

	additionalTuples, opErr := w.buildAdditionalTuples(account, accountInfo)
	if opErr != nil {
		return ctrl.Result{}, opErr
	}

	desiredTuples := baseTuples(w.fgaObjectType, accountInfo)
	desiredTuples = append(desiredTuples, additionalTuples...)

	_, err = controllerutil.CreateOrUpdate(ctxWithTimeout, w.orgsClient, store, func() error {
		store.Spec.CoreModule = w.coreModule
		store.Spec.Tuples = mergeTuples(store.Spec.Tuples, desiredTuples...)
		return nil
	})
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to update store tuples: %w", err), true, true)
	}

	original := lc.DeepCopy()
	lc.Status.Initializers = slices.DeleteFunc(lc.Status.Initializers, func(s kcpv1alpha1.LogicalClusterInitializer) bool {
		return s == initializerName
	})

	if err := w.cl.Status().Patch(ctx, lc, client.MergeFrom(original)); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to patch out initializers: %w", err), true, true)
	}

	return ctrl.Result{}, nil
}

func (w *workspaceInitializer) ensureAccountInfoStoreID(ctx context.Context, wsClient client.Client, storeID string) errors.OperatorError {
	accountInfo := &accountsv1alpha1.AccountInfo{ObjectMeta: metav1.ObjectMeta{Name: "account"}}
	_, err := controllerutil.CreateOrUpdate(ctx, wsClient, accountInfo, func() error {
		accountInfo.Spec.FGA.Store.Id = storeID
		return nil
	})
	if err != nil {
		return errors.NewOperatorError(fmt.Errorf("unable to create/update accountInfo: %w", err), true, true)
	}
	return nil
}

func (w *workspaceInitializer) getAccount(ctx context.Context, lc *kcpv1alpha1.LogicalCluster) (*accountsv1alpha1.Account, errors.OperatorError) {
	account := &accountsv1alpha1.Account{}
	ownerCluster := logicalcluster.Name(lc.Spec.Owner.Cluster)
	if err := w.cl.Get(kontext.WithCluster(ctx, ownerCluster), client.ObjectKey{Name: lc.Spec.Owner.Name}, account); err != nil {
		return nil, errors.NewOperatorError(fmt.Errorf("unable to get account: %w", err), true, true)
	}
	return account, nil
}

func (w *workspaceInitializer) buildAdditionalTuples(account *accountsv1alpha1.Account, accountInfo *accountsv1alpha1.AccountInfo) ([]v1alpha1.Tuple, errors.OperatorError) {
	tuples := []v1alpha1.Tuple{}

	if account.Spec.Type != accountsv1alpha1.AccountTypeOrg {
		parentAccountName := accountInfo.Spec.ParentAccount.Name
		tuples = append(tuples, v1alpha1.Tuple{
			Object:   fmt.Sprintf("%s:%s/%s", w.fgaObjectType, accountInfo.Spec.Account.OriginClusterId, account.Name),
			Relation: w.fgaParentRelation,
			User:     fmt.Sprintf("%s:%s/%s", w.fgaObjectType, accountInfo.Spec.ParentAccount.OriginClusterId, parentAccountName),
		})
	}

	if account.Spec.Creator != nil {
		if !validateCreator(*account.Spec.Creator) {
			return nil, errors.NewOperatorError(fmt.Errorf("creator string is in the protected service account prefix range"), false, false)
		}
		creator := formatUser(*account.Spec.Creator)

		tuples = append(tuples, v1alpha1.Tuple{
			Object:   fmt.Sprintf("role:%s/%s/%s/owner", w.fgaObjectType, accountInfo.Spec.Account.OriginClusterId, account.Name),
			Relation: "assignee",
			User:     fmt.Sprintf("user:%s", creator),
		})

		tuples = append(tuples, v1alpha1.Tuple{
			Object:   fmt.Sprintf("%s:%s/%s", w.fgaObjectType, accountInfo.Spec.Account.OriginClusterId, account.Name),
			Relation: w.fgaCreatorRelation,
			User:     fmt.Sprintf("role:%s/%s/%s/owner#assignee", w.fgaObjectType, accountInfo.Spec.Account.OriginClusterId, account.Name),
		})
	}

	return tuples, nil
}

func baseTuples(objectType string, accountInfo *accountsv1alpha1.AccountInfo) []v1alpha1.Tuple {
	return []v1alpha1.Tuple{
		{
			Object:   "role:authenticated",
			Relation: "assignee",
			User:     "user:*",
		},
		{
			Object:   fmt.Sprintf("%s:%s/%s", objectType, accountInfo.Spec.Account.OriginClusterId, accountInfo.Spec.Account.Name),
			Relation: "member",
			User:     "role:authenticated#assignee",
		},
	}
}

func mergeTuples(existing []v1alpha1.Tuple, additions ...v1alpha1.Tuple) []v1alpha1.Tuple {
	seen := make(map[string]struct{})
	result := make([]v1alpha1.Tuple, 0, len(existing)+len(additions))

	for _, tuple := range existing {
		key := tuple.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, tuple)
	}

	for _, tuple := range additions {
		key := tuple.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, tuple)
	}

	return result
}

func generateStoreName(lc *kcpv1alpha1.LogicalCluster) string {
	if path, ok := lc.Annotations["kcp.io/path"]; ok {
		pathElements := strings.Split(path, ":")
		return pathElements[len(pathElements)-1]
	}
	return ""
}

func generateStoreNameFromPath(path string) string {
	parts := strings.Split(path, ":")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
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

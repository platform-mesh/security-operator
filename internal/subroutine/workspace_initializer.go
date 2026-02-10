package subroutine

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	accountsv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/security-operator/api/v1alpha1"
	iclient "github.com/platform-mesh/security-operator/internal/client"
	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/security-operator/pkg/fga"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kcp-dev/logicalcluster/v3"
	kcpcore "github.com/kcp-dev/sdk/apis/core"
	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

func NewWorkspaceInitializer(orgsClient client.Client, cfg config.Config, mgr mcmanager.Manager, creatorRelation, parentRelation, objectType string) *workspaceInitializer {
	// read file from path
	res, err := os.ReadFile(cfg.CoreModulePath)
	if err != nil {
		panic(err)
	}

	return &workspaceInitializer{
		orgsClient:      orgsClient,
		coreModule:      string(res),
		initializerName: cfg.InitializerName(),
		mgr:             mgr,
		cfg:             cfg,
		creatorRelation: creatorRelation,
		parentRelation:  parentRelation,
		objectType:      objectType,
	}
}

var _ lifecyclesubroutine.Subroutine = &workspaceInitializer{}

type workspaceInitializer struct {
	orgsClient      client.Client
	mgr             mcmanager.Manager
	cfg             config.Config
	coreModule      string
	initializerName string

	objectType      string
	parentRelation  string
	creatorRelation string
}

func (w *workspaceInitializer) Finalize(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	// TODO: implement once finalizing workspaces are a thing
	return ctrl.Result{}, nil
}

func (w *workspaceInitializer) Finalizers(_ runtimeobject.RuntimeObject) []string {
	return nil
}

func (w *workspaceInitializer) GetName() string { return "WorkspaceInitializer" }

func (w *workspaceInitializer) Process(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)

	lc := instance.(*kcpcorev1alpha1.LogicalCluster)
	p := lc.Annotations[kcpcore.LogicalClusterPathAnnotationKey]
	if p == "" {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("annotation on LogicalCluster is not set"), true, true)
	}
	lcID, _ := mccontext.ClusterFrom(ctx)
	log = log.ChildLogger("ID", lcID).ChildLogger("path", p)
	log.Info().Msgf("Processing logical cluster")

	lcClient, err := iclient.NewForLogicalCluster(w.mgr.GetLocalManager().GetConfig(), w.mgr.GetLocalManager().GetScheme(), logicalcluster.Name(lcID))
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

	orgsClient, err := iclient.NewForLogicalCluster(w.mgr.GetLocalManager().GetConfig(), w.mgr.GetLocalManager().GetScheme(), logicalcluster.Name("root:orgs"))
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("getting parent organisation client: %w", err), true, true)
	}

	var acc accountsv1alpha1.Account
	if err := orgsClient.Get(ctx, client.ObjectKey{
		Name: ai.Spec.Account.Name,
	}, &acc); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("getting Account in platform-mesh-system: %w", err), true, true)
	}

	store := v1alpha1.Store{
		ObjectMeta: metav1.ObjectMeta{Name: generateStoreName(lc)},
	}

	tuples := fga.TuplesForOrganization(acc, ai, w.creatorRelation, w.objectType)
	if w.cfg.AllowMemberTuplesEnabled { // TODO: remove this flag once the feature is tested and stable
		tuples = append(tuples, []v1alpha1.Tuple{
			{
				Object:   "role:authenticated",
				Relation: "assignee",
				User:     "user:*",
			},
			{
				Object:   fmt.Sprintf("core_platform-mesh_io_account:%s/%s", lc.Spec.Owner.Cluster, lc.Spec.Owner.Name),
				Relation: "member",
				User:     "role:authenticated#assignee",
			},
		}...)
	}
	if result, err := controllerutil.CreateOrUpdate(ctx, w.orgsClient, &store, func() error {
		store.Spec = v1alpha1.StoreSpec{
			CoreModule: w.coreModule,
		}
		store.Spec.Tuples = tuples

		return nil
	}); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to create/update store: %w", err), true, true)
	} else if result == controllerutil.OperationResultCreated || result == controllerutil.OperationResultUpdated {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("store needed to be updated, requeueing"), true, false)
	}

	// Check if Store applied tuple changes
	for _, t := range tuples {
		if !slices.Contains(store.Status.ManagedTuples, t) {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("store does not yet contain all specified tuples, requeueing"), true, false)
		}
	}

	if store.Status.StoreID == "" {
		// Store is not ready yet, requeue
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("store id is empty"), true, false)
	}

	cluster, err := w.mgr.ClusterFromContext(ctx)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to get cluster from context: %w", err), true, false)
	}

	accountInfo := accountsv1alpha1.AccountInfo{
		ObjectMeta: metav1.ObjectMeta{Name: "account"},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, cluster.GetClient(), &accountInfo, func() error {
		accountInfo.Spec.FGA.Store.Id = store.Status.StoreID
		return nil
	})
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to create/update accountInfo: %w", err), true, true)
	}

	return ctrl.Result{}, nil
}

func generateStoreName(lc *kcpcorev1alpha1.LogicalCluster) string {
	if path, ok := lc.Annotations["kcp.io/path"]; ok {
		pathElements := strings.Split(path, ":")
		return pathElements[len(pathElements)-1]
	}
	return ""
}

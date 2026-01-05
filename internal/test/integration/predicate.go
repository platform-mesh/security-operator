package test

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	kcpapiv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"
	"github.com/kcp-dev/multicluster-provider/apiexport"
	clusterclient "github.com/kcp-dev/multicluster-provider/client"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	crpredicate "sigs.k8s.io/controller-runtime/pkg/predicate"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	kcpcorev1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
)

func RunPredicateManager(ctx context.Context, kubeconfig string, log logr.Logger) error {
	baseCfg, err := loadKCPConfig(kubeconfig)
	if err != nil {
		return err
	}

	sch := clientgoscheme.Scheme
	cli, err := clusterclient.New(baseCfg, client.Options{Scheme: sch})
	if err != nil {
		return fmt.Errorf("failed to build admin client: %w", err)
	}

	platformMeshSystemPath := logicalcluster.NewPath("root:platform-mesh-system")

	var endpointSlice kcpapiv1alpha1.APIExportEndpointSlice
	exportName := "core.platform-mesh.io"
	if err := cli.Cluster(platformMeshSystemPath).Get(ctx, client.ObjectKey{Name: exportName}, &endpointSlice); err != nil {
		return fmt.Errorf("failed to get APIExportEndpointSlice %q in %s: %w", exportName, platformMeshSystemPath, err)
	}

	url := endpointSlice.Status.APIExportEndpoints[0].URL
	log.Info("using APIExport virtual workspace endpoint", "url", url)

	vwCfg := rest.CopyConfig(baseCfg)
	vwCfg.Host = url

	provider, err := apiexport.New(vwCfg, apiexport.Options{Scheme: sch})
	if err != nil {
		return fmt.Errorf("failed to create apiexport provider: %w", err)
	}

	mgr, err := mcmanager.New(vwCfg, provider, mcmanager.Options{Scheme: sch})
	if err != nil {
		return fmt.Errorf("failed to create multicluster manager: %w", err)
	}

	if err := (&LogicalClusterPredicateReconciler{log: log.WithName("logicalcluster-predicate"), mgr: mgr}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to set up logicalcluster predicate controller: %w", err)
	}

	go func() {
		if err := provider.Run(ctx, mgr); err != nil {
			log.Error(err, "apiexport provider exited")
		}
	}()

	log.Info("starting manager")
	return mgr.Start(ctx)
}

var OnlyTestLogicalClusterPredicate crpredicate.Predicate = crpredicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return shouldReconcile(e.Object)
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		return shouldReconcile(e.ObjectNew)
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return shouldReconcile(e.Object)
	},
	GenericFunc: func(e event.GenericEvent) bool {
		return shouldReconcile(e.Object)
	},
}

func shouldReconcile(obj client.Object) bool {
	lc, ok := obj.(*kcpcorev1alpha1.LogicalCluster)
	if !ok {
		ctrl.Log.WithName("logicalcluster-predicate").Info("predicate: skipping non-LogicalCluster object", "type", fmt.Sprintf("%T", obj))
		return false
	}

	match := strings.Contains(lc.Spec.Owner.Name, "test")
	ctrl.Log.WithName("logicalcluster-predicate").Info("predicate decision", "cluster", lc.Spec.Owner.Name, "should be reconciled ?", match)
	return match
}

type LogicalClusterPredicateReconciler struct {
	log logr.Logger
	mgr mcmanager.Manager
}

func (r *LogicalClusterPredicateReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	m, err := r.mgr.GetManager(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get manager for %q: %w", req.ClusterName, err)
	}
	var lc kcpcorev1alpha1.LogicalCluster
	if err := m.GetClient().Get(ctx, client.ObjectKey{Name: kcpcorev1alpha1.LogicalClusterName}, &lc); err != nil {
		return ctrl.Result{}, err
	}

	r.log.Info("reconciled LogicalCluster",
		"clusterKey", req.ClusterName,
		"phase", string(lc.Status.Phase),
		"ownerName", lc.Spec.Owner.Name,
		"ownerCluster", lc.Spec.Owner.Cluster,
	)
	return ctrl.Result{}, nil
}

func (r *LogicalClusterPredicateReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	return mcbuilder.ControllerManagedBy(mgr).
		Named("logicalcluster_predicate").
		For(&kcpcorev1alpha1.LogicalCluster{}, mcbuilder.WithPredicates(OnlyTestLogicalClusterPredicate)).
		Complete(r)
}

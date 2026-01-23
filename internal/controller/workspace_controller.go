package controller

import (
	"context"
	"slices"

	platformeshconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/builder"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/multicluster"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/security-operator/internal/subroutine"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

type WorkspaceReconciler struct {
	log         *logger.Logger
	mgr         mcmanager.Manager
	initializer kcpcorev1alpha1.LogicalClusterInitializer
	mclifecycle *multicluster.LifecycleManager
}

func NewWorkspaceReconciler(log *logger.Logger, orgClient client.Client, cfg config.Config, inClusterClient client.Client, mgr mcmanager.Manager) *WorkspaceReconciler {
	return &WorkspaceReconciler{
		log: log,
		mgr: mgr,
		mclifecycle: builder.NewBuilder("logicalcluster", "LogicalClusterReconciler", []lifecyclesubroutine.Subroutine{
			subroutine.NewWorkspaceInitializer(orgClient, cfg, mgr),
			subroutine.NewIDPSubroutine(orgClient, mgr, cfg),
			subroutine.NewInviteSubroutine(orgClient, mgr),
			subroutine.NewWorkspaceAuthConfigurationSubroutine(orgClient, inClusterClient, mgr, cfg),
		}, log).WithReadOnly().BuildMultiCluster(mgr),
	}
}

func (r *WorkspaceReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	ctxWithCluster := mccontext.WithCluster(ctx, req.ClusterName)
	return r.mclifecycle.Reconcile(ctxWithCluster, req, &kcpcorev1alpha1.LogicalCluster{})
}

func (r *WorkspaceReconciler) SetupWithManager(mgr mcmanager.Manager, cfg *platformeshconfig.CommonServiceConfig, initializerName string, evp ...predicate.Predicate) error {
	r.initializer = kcpcorev1alpha1.LogicalClusterInitializer(initializerName)
	allPredicates := append([]predicate.Predicate{HasInitializerPredicate(initializerName)}, evp...)
	return r.mclifecycle.SetupWithManager(mgr, cfg.MaxConcurrentReconciles, "LogicalCluster", &kcpcorev1alpha1.LogicalCluster{}, cfg.DebugLabelValue, r, r.log, allPredicates...)
}

func HasInitializerPredicate(initializerName string) predicate.Predicate {
	initializer := kcpcorev1alpha1.LogicalClusterInitializer(initializerName)
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			lc := e.Object.(*kcpcorev1alpha1.LogicalCluster)
			return shouldReconcile(lc, initializer)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			newLC := e.ObjectNew.(*kcpcorev1alpha1.LogicalCluster)
			return shouldReconcile(newLC, initializer)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			lc := e.Object.(*kcpcorev1alpha1.LogicalCluster)
			return shouldReconcile(lc, initializer)
		},
		GenericFunc: func(e event.GenericEvent) bool {
			lc := e.Object.(*kcpcorev1alpha1.LogicalCluster)
			return shouldReconcile(lc, initializer)
		},
	}
}

func shouldReconcile(lc *kcpcorev1alpha1.LogicalCluster, initializer kcpcorev1alpha1.LogicalClusterInitializer) bool {
	return slices.Contains(lc.Spec.Initializers, initializer) && !slices.Contains(lc.Status.Initializers, initializer)
}

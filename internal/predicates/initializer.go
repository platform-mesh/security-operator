package predicates

import (
	"slices"

	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

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

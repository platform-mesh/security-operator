package predicates

import (
	"fmt"
	"slices"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

func HasInitializerPredicate(initializerName string) predicate.Predicate {
	initializer := kcpcorev1alpha1.LogicalClusterInitializer(initializerName)
	return predicate.NewPredicateFuncs(func(object client.Object) bool {
		lc, ok := object.(*kcpcorev1alpha1.LogicalCluster)
		if !ok {
			panic(fmt.Errorf("received non-LogicalCluster resource in HasInitializer predicate"))
		}
		return shouldReconcile(lc, initializer)
	})
}

func shouldReconcile(lc *kcpcorev1alpha1.LogicalCluster, initializer kcpcorev1alpha1.LogicalClusterInitializer) bool {
	return slices.Contains(lc.Spec.Initializers, initializer) && !slices.Contains(lc.Status.Initializers, initializer)
}

package predicates

import (
	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// IsAccountTypeOrg returns a predicate that filters for LogicalClusters
// belonging to an Account of type "org".
// todo(simontesar): more stable implementation not relying on static orgs path
func IsAccountTypeOrg() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(object client.Object) bool {
		lc := object.(*kcpcorev1alpha1.LogicalCluster)

		return lc.Spec.Owner.Name == "orgs"
	})
}

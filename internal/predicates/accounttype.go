package predicates

import (
	"strings"

	kcpcore "github.com/kcp-dev/sdk/apis/core"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// IsAccountTypeOrg returns a predicate that filters for LogicalClusters
// belonging to an Account of type "org".
// todo(simontesar): more stable implementation not relying on static orgs path
func IsAccountTypeOrg() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(object client.Object) bool {
		a := object.GetAnnotations()
		lc := a[kcpcore.LogicalClusterPathAnnotationKey]
		return strings.Contains(lc, ":orgs:")
	})
}

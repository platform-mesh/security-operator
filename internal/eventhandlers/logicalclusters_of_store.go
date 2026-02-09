package eventhandlers

import (
	"context"
	"fmt"

	accountsv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/platform-mesh/security-operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
)

// LogicalClusersOfStore returns a reconcile request for every LogicalCluster
// a that is a child of a Store.
func LogicalClusersOfStore(platformMeshClient client.Client) func(_ string, c cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
	return func(_ string, _ cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
		return mchandler.TypedEnqueueRequestsFromMapFuncWithClusterPreservation(func(ctx context.Context, obj client.Object) []mcreconcile.Request {
			store, ok := obj.(*v1alpha1.Store)
			if !ok {
				panic("received event for non-Store object")
			}

			var ail accountsv1alpha1.AccountInfoList
			if err := platformMeshClient.List(ctx, &ail); err != nil {
				panic(fmt.Errorf("listing AccountInfos in mapper function: %w", err))
			}

			reqs := []mcreconcile.Request{}
			for _, ai := range ail.Items {
				if ai.Spec.Organization.Name == store.GetName() {
					reqs = append(reqs, mcreconcile.Request{
						Request: reconcile.Request{
							NamespacedName: types.NamespacedName{
								Name: "cluster",
							},
						},
						ClusterName: ai.Spec.Account.GeneratedClusterId,
					})
				}
			}

			return reqs
		})
	}
}

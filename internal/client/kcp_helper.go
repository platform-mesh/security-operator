package client

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
)

type KCPHelper interface {
	NewClientForLogicalCluster(ctx context.Context, cluster string) (client.Client, error)
	GetAllClient(ctx context.Context, apiexportEndpointSliceName string) (client.Client, error)
}

type ManagerKcpHelper struct {
	config *rest.Config
	scheme *runtime.Scheme
	mgr    mcmanager.Manager
}

func NewKcpHelper(mgr mcmanager.Manager) *ManagerKcpHelper {
	return &ManagerKcpHelper{config: mgr.GetLocalManager().GetConfig(), scheme: mgr.GetLocalManager().GetScheme(), mgr: mgr}
}

func (f *ManagerKcpHelper) NewClientForLogicalCluster(ctx context.Context, cluster string) (client.Client, error) {
	kcpCluster, err := f.mgr.GetCluster(ctx, cluster)
	if err != nil {
		return nil, fmt.Errorf("getting cluster: %w", err)
	}

	return kcpCluster.GetClient(), nil
}

func (f *ManagerKcpHelper) GetAllClient(ctx context.Context, apiexportEndpointSliceName string) (client.Client, error) {
	return GetAllClient(ctx, f.config, f.scheme, apiexportEndpointSliceName)
}

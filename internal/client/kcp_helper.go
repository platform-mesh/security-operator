package client

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
)

type KCPClientGetter interface {
	NewClientForLogicalCluster(ctx context.Context, cluster string) (client.Client, error)
}

type KCPAllClientGetter interface {
	GetAllClient(ctx context.Context, apiexportEndpointSliceName string) (client.Client, error)
}

type KCPCombinedClientGetter interface {
	KCPClientGetter
	KCPAllClientGetter
}

type ManagerKcpHelper struct {
	mgr mcmanager.Manager
}

func NewKcpHelper(mgr mcmanager.Manager) *ManagerKcpHelper {
	return &ManagerKcpHelper{mgr: mgr}
}

func (f *ManagerKcpHelper) NewClientForLogicalCluster(ctx context.Context, cluster string) (client.Client, error) {
	kcpCluster, err := f.mgr.GetCluster(ctx, cluster)
	if err != nil {
		return nil, fmt.Errorf("getting cluster: %w", err)
	}

	return kcpCluster.GetClient(), nil
}

func (f *ManagerKcpHelper) GetAllClient(ctx context.Context, apiexportEndpointSliceName string) (client.Client, error) {
	return GetAllClient(ctx, f.mgr.GetLocalManager().GetConfig(), f.mgr.GetLocalManager().GetScheme(), apiexportEndpointSliceName)
}

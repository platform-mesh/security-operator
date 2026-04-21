package client

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"

	"github.com/kcp-dev/logicalcluster/v3"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
)

type KCPClientGetter interface {
	NewClientForLogicalCluster(ctx context.Context, cluster string) (client.Client, error)
}

type KCPAllClientGetter interface {
	AllClient(ctx context.Context, apiexportEndpointSliceName string) (client.Client, error)
}

type KCPCombinedClientGetter interface {
	KCPClientGetter
	KCPAllClientGetter
}

type ManagerKCPClientGetter struct {
	mgr mcmanager.Manager
}

func NewManagerKCPClientGetter(mgr mcmanager.Manager) *ManagerKCPClientGetter {
	return &ManagerKCPClientGetter{mgr: mgr}
}

func (f *ManagerKCPClientGetter) NewClientForLogicalCluster(ctx context.Context, cluster string) (client.Client, error) {
	kcpCluster, err := f.mgr.GetCluster(ctx, cluster)
	if err != nil {
		return nil, fmt.Errorf("getting cluster: %w", err)
	}

	return kcpCluster.GetClient(), nil
}

func (f *ManagerKCPClientGetter) AllClient(ctx context.Context, apiexportEndpointSliceName string) (client.Client, error) {
	return NewAll(ctx, f.mgr.GetLocalManager().GetConfig(), f.mgr.GetLocalManager().GetScheme(), apiexportEndpointSliceName)
}

type ConfigSchemeKCPClientGetter struct {
	config *rest.Config
	scheme *runtime.Scheme
}

func NewConfigSchemeKCPClientGetter(config *rest.Config, scheme *runtime.Scheme) *ConfigSchemeKCPClientGetter {
	return &ConfigSchemeKCPClientGetter{
		config: config,
		scheme: scheme,
	}
}

func (f *ConfigSchemeKCPClientGetter) NewClientForLogicalCluster(ctx context.Context, cluster string) (client.Client, error) {
	_ = ctx
	return NewForLogicalCluster(f.config, f.scheme, logicalcluster.Name(cluster))
}

func (f *ConfigSchemeKCPClientGetter) AllClient(ctx context.Context, apiexportEndpointSliceName string) (client.Client, error) {
	return NewAll(ctx, f.config, f.scheme, apiexportEndpointSliceName)
}

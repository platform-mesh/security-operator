package client

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"

	"github.com/kcp-dev/logicalcluster/v3"
)

type KcpClientHelper interface {
	NewClientForLogicalCluster(clusterKey logicalcluster.Name) (client.Client, error)
	GetAllClient(ctx context.Context, apiexportEndpointSliceName string) (client.Client, error)
}

type KcpHelper struct {
	config *rest.Config
	scheme *runtime.Scheme
}

func NewKcpHelper(config *rest.Config, scheme *runtime.Scheme) *KcpHelper {
	return &KcpHelper{config: config, scheme: scheme}
}

func (f *KcpHelper) NewClientForLogicalCluster(clusterKey logicalcluster.Name) (client.Client, error) {
	return NewForLogicalCluster(f.config, f.scheme, clusterKey)
}

func (f *KcpHelper) GetAllClient(ctx context.Context, apiexportEndpointSliceName string) (client.Client, error) {
	return GetAllClient(ctx, f.config, f.scheme, apiexportEndpointSliceName)
}

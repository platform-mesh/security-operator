package client

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"

	"github.com/kcp-dev/logicalcluster/v3"
	mcpprovider "github.com/kcp-dev/multicluster-provider/pkg/provider"
)

type KcpClientHelper interface {
	NewClientForLogicalCluster(clusterKey logicalcluster.Name) (client.Client, error)
	List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error
}

type KcpHelper struct {
	config   *rest.Config
	scheme   *runtime.Scheme
	provider *mcpprovider.Provider
}

func NewKcpHelper(config *rest.Config, scheme *runtime.Scheme, provider *mcpprovider.Provider) *KcpHelper {
	return &KcpHelper{
		config:   config,
		scheme:   scheme,
		provider: provider,
	}
}

func (f *KcpHelper) NewClientForLogicalCluster(clusterKey logicalcluster.Name) (client.Client, error) {
	return NewForLogicalCluster(f.config, f.scheme, clusterKey)
}

func (f *KcpHelper) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return f.provider.Lister().List(ctx, list, opts...)
}

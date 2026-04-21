package pathaware

import (
	"context"
	"fmt"

	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	"github.com/kcp-dev/logicalcluster/v3"
	kcpcore "github.com/kcp-dev/sdk/apis/core"

	provider "github.com/kcp-dev/multicluster-provider/initializingworkspaces"
	"github.com/kcp-dev/multicluster-provider/pkg/handlers"
	"github.com/kcp-dev/multicluster-provider/pkg/paths"
)

var _ multicluster.Provider = &Provider{}
var _ multicluster.ProviderRunnable = &Provider{}
var _ handlers.Handler = &pathHandler{}

// Provider wraps the initializingworkspaces provider and adds best-effort path
// awareness by indexing logical cluster paths from watched objects.
type Provider struct {
	*provider.Provider
	// pathStore maps logical cluster paths to cluster names.
	pathStore *paths.Store
}

// New creates a new path-aware initializingworkspaces provider.
func New(cfg *rest.Config, workspaceTypeName string, options provider.Options) (*Provider, error) {
	store := paths.New()

	h := &pathHandler{
		pathStore: store,
	}
	options.Handlers = append(options.Handlers, h)

	p, err := provider.New(cfg, workspaceTypeName, options)
	if err != nil {
		return nil, err
	}

	return &Provider{
		Provider:  p,
		pathStore: store,
	}, nil
}

// Get returns the cluster with the given name as a cluster.Cluster.
func (p *Provider) Get(ctx context.Context, clusterName string) (cluster.Cluster, error) {
	if p.pathStore != nil {
		if lcName, exists := p.pathStore.Get(clusterName); exists {
			clusterName = lcName.String()
		}
	}

	return p.Provider.Get(ctx, clusterName)
}

// IndexField adds an indexer to the clusters managed by this provider.
func (p *Provider) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
	return p.Provider.IndexField(ctx, obj, field, extractValue)
}

// Start starts the provider and blocks.
func (p *Provider) Start(ctx context.Context, aware multicluster.Aware) error {
	return p.Provider.Start(ctx, aware)
}

type pathHandler struct {
	pathStore *paths.Store
}

func (p *pathHandler) OnAdd(obj client.Object) {
	cluster := logicalcluster.From(obj)

	path := obj.GetAnnotations()[kcpcore.LogicalClusterPathAnnotationKey]
	if path == "" {
		return
	}

	fmt.Printf("Adding cluster %s with path %s\n", cluster, path)
	p.pathStore.Add(path, cluster)
}

func (p *pathHandler) OnUpdate(oldObj, newObj client.Object) {
	// Not used.
}

func (p *pathHandler) OnDelete(obj client.Object) {
	path, ok := obj.GetAnnotations()[kcpcore.LogicalClusterPathAnnotationKey]
	if !ok {
		return
	}

	p.pathStore.Remove(path)
}

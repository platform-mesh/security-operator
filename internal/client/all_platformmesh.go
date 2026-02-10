package client

import (
	"context"
	"fmt"
	"net/url"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"

	"github.com/kcp-dev/logicalcluster/v3"
	kcpapisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
)

const (
	corePlatformMeshIOAPIExportEndpointSlice = "core.platform-mesh.io"
	platformMeshSystemWorkspace              = "root:platform-mesh-system"
)

// NewForAllPlatformMeshResources returns a client that can query all resources
// of the core.platform-mesh.io APIExportEndpoint slice, based on a given KCP
// base config.
func NewForAllPlatformMeshResources(ctx context.Context, config *rest.Config, scheme *runtime.Scheme) (client.Client, error) {
	platformMeshClient, err := NewForLogicalCluster(config, scheme, logicalcluster.Name(platformMeshSystemWorkspace))
	if err != nil {
		return nil, fmt.Errorf("creating %s client: %w", platformMeshSystemWorkspace, err)
	}

	var apiExportEndpointSlice kcpapisv1alpha1.APIExportEndpointSlice
	if err := platformMeshClient.Get(ctx, types.NamespacedName{Name: corePlatformMeshIOAPIExportEndpointSlice}, &apiExportEndpointSlice); err != nil {
		return nil, fmt.Errorf("getting %s APIExportEndpointSlice: %w", corePlatformMeshIOAPIExportEndpointSlice, err)
	}

	virtualWorkspaceUrl, err := url.Parse(apiExportEndpointSlice.Status.APIExportEndpoints[0].URL)
	if err != nil {
		return nil, fmt.Errorf("parsing virtual workspace URL: %w", err)
	}

	path, err := url.JoinPath(virtualWorkspaceUrl.Path, "clusters", logicalcluster.Wildcard.String())
	if err != nil {
		return nil, fmt.Errorf("joining path: %w", err)
	}

	return clientForPath(config, scheme, path)
}

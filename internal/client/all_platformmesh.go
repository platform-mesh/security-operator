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
	platformMeshSystemWorkspace = "root:platform-mesh-system"
)

// GetAllClient returns a client that can query all resources
// of the APIExportEndpointSlice, based on a given KCP
// base config and APIExportEndpointSlice name
func GetAllClient(ctx context.Context, config *rest.Config, scheme *runtime.Scheme, apiexportEndpointSliceName string) (client.Client, error) {
	platformMeshClient, err := NewForLogicalCluster(config, scheme, logicalcluster.Name(platformMeshSystemWorkspace))
	if err != nil {
		return nil, fmt.Errorf("creating %s client: %w", platformMeshSystemWorkspace, err)
	}

	var apiExportEndpointSlice kcpapisv1alpha1.APIExportEndpointSlice
	if err := platformMeshClient.Get(ctx, types.NamespacedName{Name: apiexportEndpointSliceName}, &apiExportEndpointSlice); err != nil {
		return nil, fmt.Errorf("getting %s APIExportEndpointSlice: %w", apiexportEndpointSliceName, err)
	}

	if len(apiExportEndpointSlice.Status.APIExportEndpoints) == 0 {
		return nil, fmt.Errorf("no endpoints found in %s APIExportEndpointSlice", apiexportEndpointSliceName)
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

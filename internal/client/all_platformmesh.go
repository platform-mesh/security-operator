package client

import (
	"context"
	"fmt"
	"net/url"

	"sigs.k8s.io/controller-runtime/pkg/client"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"

	"github.com/kcp-dev/logicalcluster/v3"
	kcpapisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
)

const (
	platformMeshSystemWorkspace = "root:platform-mesh-system"
)

type AllPlatformMeshClient interface {
	List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error
}

type allPlatformMeshClient struct {
	clients []client.Client
}

// List aggregates results from all clients into a single list
func (m *allPlatformMeshClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	allItems := make([]runtime.Object, 0)

	for _, c := range m.clients {
		tempList := list.DeepCopyObject().(client.ObjectList)

		if err := c.List(ctx, tempList, opts...); err != nil {
			// If the resource is not found, continue checking other shards
			if kerrors.IsNotFound(err) {
				continue
			}
			return err
		}

		clientItems, err := meta.ExtractList(tempList)
		if err != nil {
			return fmt.Errorf("extracting items from client list: %w", err)
		}

		allItems = append(allItems, clientItems...)
	}

	if err := meta.SetList(list, allItems); err != nil {
		return fmt.Errorf("setting aggregated list: %w", err)
	}
	return nil
}

// GetAllClient returns a client that can query all resources
// of the APIExportEndpointSlice, based on a given KCP
// base config and APIExportEndpointSlice name
func GetAllClient(ctx context.Context, config *rest.Config, scheme *runtime.Scheme, apiexportEndpointSliceName string) (AllPlatformMeshClient, error) {
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

	// Create a client for each endpoint
	clients := make([]client.Client, 0, len(apiExportEndpointSlice.Status.APIExportEndpoints))
	for i, endpoint := range apiExportEndpointSlice.Status.APIExportEndpoints {
		virtualWorkspaceUrl, err := url.Parse(endpoint.URL)
		if err != nil {
			return nil, fmt.Errorf("parsing virtual workspace URL for endpoint %d: %w", i, err)
		}

		wildcardURL := *virtualWorkspaceUrl
		wildcardURL.Path, err = url.JoinPath(virtualWorkspaceUrl.Path, "clusters", logicalcluster.Wildcard.String())
		if err != nil {
			return nil, fmt.Errorf("joining path for endpoint %d: %w", i, err)
		}

		endpointConfig := rest.CopyConfig(config)
		endpointConfig.Host = wildcardURL.String()

		c, err := client.New(endpointConfig, client.Options{
			Scheme: scheme,
		})
		if err != nil {
			return nil, fmt.Errorf("creating client for endpoint %d: %w", i, err)
		}

		clients = append(clients, c)
	}

	return &allPlatformMeshClient{clients: clients}, nil
}

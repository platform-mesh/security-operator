package client

import (
	"fmt"
	"net/url"

	"github.com/kcp-dev/logicalcluster/v3"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func NewForLogicalCluster(config *rest.Config, scheme *runtime.Scheme, clusterKey logicalcluster.Name) (client.Client, error) {
	path := fmt.Sprintf("/clusters/%s", clusterKey)

	return clientForPath(config, scheme, path)
}

func clientForPath(config *rest.Config, scheme *runtime.Scheme, path string) (client.Client, error) {
	copy := rest.CopyConfig(config)

	parsed, err := url.Parse(copy.Host)
	if err != nil {
		return nil, fmt.Errorf("parsing host from config: %w", err)
	}
	parsed.Path = path
	copy.Host = parsed.String()

	return client.New(copy, client.Options{
		Scheme: scheme,
	})
}

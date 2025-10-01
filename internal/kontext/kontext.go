package kontext

import (
	"context"

	"github.com/kcp-dev/logicalcluster/v3"
)

type key int

const (
	keyCluster key = iota
)

// WithClusterName stores a logical cluster name in the context
func WithCluster(ctx context.Context, name logicalcluster.Name) context.Context {
	return context.WithValue(ctx, keyCluster, name)
}

// ClusterFrom extracts a cluster name from the context.
func ClusterFrom(ctx context.Context) (logicalcluster.Name, bool) {
	s, ok := ctx.Value(keyCluster).(logicalcluster.Name)
	return s, ok
}

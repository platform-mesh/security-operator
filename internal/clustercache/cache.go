package clustercache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"github.com/kcp-dev/logicalcluster/v3"
	"github.com/platform-mesh/golang-commons/logger"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
)

type Provider[V any] interface {
	mcmanager.Runnable
	Get(clusterID string) (V, error)
}

type AccountInformation struct {
	StoreID         string
	AccountName     string
	AccountCreator  string
	ParentClusterID string
}

type AccountInformationCache struct {
	cache  *ttlcache.Cache[string, *AccountInformation]
	logger *logger.Logger
}

// New returns a cache backed by an in-memory TTL store.
func New(l *logger.Logger) *AccountInformationCache {
	return &AccountInformationCache{
		cache:  ttlcache.New[string, *AccountInformation](),
		logger: l,
	}
}

// Start is noop but implements manager.Runnable.
func (c *AccountInformationCache) Start(ctx context.Context) error {
	return nil
}

// Engage records account metadata from the workspace LogicalCluster.
// Implements multicluster.Aware.
func (c *AccountInformationCache) Engage(ctx context.Context, name string, cl cluster.Cluster) error {
	var lc unstructured.Unstructured
	err := wait.PollUntilContextCancel(ctx, time.Second, true, func(ctx context.Context) (bool, error) {
		lc = unstructured.Unstructured{}
		lc.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "core.kcp.io",
			Version: "v1alpha1",
			Kind:    "LogicalCluster",
		})
		if err := cl.GetClient().Get(ctx, types.NamespacedName{Name: "cluster"}, &lc); err != nil {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return err
	}

	annotationPath := lc.GetAnnotations()["kcp.io/path"]
	if annotationPath == "" {
		return nil
	}

	accountName := logicalcluster.NewPath(annotationPath).Base()

	parentClusterID, found, err := unstructured.NestedString(lc.Object, "spec", "owner", "cluster")
	if err != nil {
		return fmt.Errorf("owner.cluster from LogicalCluster: %w", err)
	}
	if !found {
		return errors.New("owner.cluster not found in LogicalCluster spec")
	}

	info := &AccountInformation{
		AccountName:     accountName,
		ParentClusterID: parentClusterID,
	}
	c.cache.Set(name, info, ttlcache.NoTTL)

	c.logger.Info().
		Str("cluster", name).
		Str("accountPath", annotationPath).
		Str("accountName", accountName).
		Str("parentClusterID", parentClusterID).
		Msg("account information cache entry populated")

	return nil
}

var _ mcmanager.Runnable = (*AccountInformationCache)(nil)

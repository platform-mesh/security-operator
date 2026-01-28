package subroutine

import (
	"context"
	"fmt"

	mcclient "github.com/kcp-dev/multicluster-provider/client"
	kcpcore "github.com/kcp-dev/sdk/apis/core"
	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
	openfga "github.com/openfga/go-sdk"

	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	ctrl "sigs.k8s.io/controller-runtime"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
)

type AccountTuplesSubroutine struct {
	fga *openfga.APIClient
	mgr mcmanager.Manager
	mcc mcclient.ClusterClient
}

// Finalize implements lifecycle.Subroutine.
func (s *AccountTuplesSubroutine) Finalize(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)

	lc := instance.(*kcpcorev1alpha1.LogicalCluster)
	p := lc.Annotations[kcpcore.LogicalClusterPathAnnotationKey]
	if p == "" {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("annotation on LogicalCluster is not set"), true, true)
	}
	log.Info().Msgf("Finalizing logical cluster of path %s", p)

	return ctrl.Result{}, nil
}

// Finalizers implements lifecycle.Subroutine.
func (s *AccountTuplesSubroutine) Finalizers(_ runtimeobject.RuntimeObject) []string {
	return []string{"core.platform-mesh.io/account-fga-tuples"}
}

// GetName implements lifecycle.Subroutine.
func (s *AccountTuplesSubroutine) GetName() string { return "AccountTuplesSubroutine" }

// Process implements lifecycle.Subroutine.
func (s *AccountTuplesSubroutine) Process(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)

	lc := instance.(*kcpcorev1alpha1.LogicalCluster)
	p := lc.Annotations[kcpcore.LogicalClusterPathAnnotationKey]
	if p == "" {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("annotation on LogicalCluster is not set"), true, true)
	}
	cluster, _ := mccontext.ClusterFrom(ctx)
	log.Info().Msgf("Processing logical cluster of path %s with %s in context", p, cluster)

	return ctrl.Result{}, nil
}

func NewAccountTuplesSubroutine(fga *openfga.APIClient, mcc mcclient.ClusterClient, mgr mcmanager.Manager) *AccountTuplesSubroutine {
	return &AccountTuplesSubroutine{
		fga: fga,
		mgr: mgr,
		mcc: mcc,
	}
}

var _ lifecyclesubroutine.Subroutine = &AccountTuplesSubroutine{}

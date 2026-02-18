package subroutine

import (
	"context"
	"fmt"
	"slices"

	"github.com/kcp-dev/logicalcluster/v3"
	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	iclient "github.com/platform-mesh/security-operator/internal/client"
	"github.com/platform-mesh/security-operator/internal/config"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
)

type RemoveTerminatorSubroutine struct {
	terminatorName string
	mgr            mcmanager.Manager
}

// Finalize implements subroutine.Subroutine.
func (s *RemoveTerminatorSubroutine) Finalize(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	_ = ctx
	_ = instance
	return ctrl.Result{}, nil
}

// Finalizers implements subroutine.Subroutine.
func (s *RemoveTerminatorSubroutine) Finalizers(_ runtimeobject.RuntimeObject) []string {
	return []string{}
}

// GetName implements subroutine.Subroutine.
func (s *RemoveTerminatorSubroutine) GetName() string { return "RemoveTerminator" }

// Process implements subroutine.Subroutine.
func (s *RemoveTerminatorSubroutine) Process(_ context.Context, _ runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil
}

// Terminate implements subroutine.Terminator.
func (s *RemoveTerminatorSubroutine) Terminate(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	lc := *(instance.(*kcpcorev1alpha1.LogicalCluster))

	lcID, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("cluster name not found in context"), true, true)
	}
	lcClient, err := iclient.NewForLogicalCluster(s.mgr.GetLocalManager().GetConfig(), s.mgr.GetLocalManager().GetScheme(), logicalcluster.Name(lcID))
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("getting client: %w", err), true, true)
	}

	copy := lc.DeepCopy()
	lc.Status.Terminators = slices.DeleteFunc(lc.Status.Terminators, func(t kcpcorev1alpha1.LogicalClusterTerminator) bool {
		return t == kcpcorev1alpha1.LogicalClusterTerminator(s.terminatorName)
	})

	if err := lcClient.Status().Patch(ctx, &lc, client.MergeFrom(copy)); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("patching LogicalCluster: %w", err), true, true)
	}

	return ctrl.Result{}, nil
}

// NewRemoveTerminator returns a new removeTerminator subroutine.
func NewRemoveTerminator(mgr mcmanager.Manager, cfg config.Config) *RemoveTerminatorSubroutine {
	return &RemoveTerminatorSubroutine{
		terminatorName: cfg.TerminatorName(),
		mgr:            mgr,
	}
}

var _ subroutine.Subroutine = &RemoveTerminatorSubroutine{}
var _ subroutine.Terminator = &RemoveTerminatorSubroutine{}

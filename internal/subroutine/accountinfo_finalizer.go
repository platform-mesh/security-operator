package subroutine

import (
	"context"
	"time"

	accountv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	lifecyclecontrollerruntime "github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	kcpapisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
)

const (
	AccountInfoFinalizer = "security.platform-mesh.io/accountinfo-finalizer"
	APIBindingFinalizer  = "core.platform-mesh.io/apibinding-finalizer"
)

type AccountInfoFinalizerSubroutine struct {
	mgr mcmanager.Manager
}

func NewAccountInfoFinalizerSubroutine(mgr mcmanager.Manager) *AccountInfoFinalizerSubroutine {
	return &AccountInfoFinalizerSubroutine{
		mgr: mgr,
	}
}

var _ lifecyclesubroutine.Subroutine = &AccountInfoFinalizerSubroutine{}

func (a *AccountInfoFinalizerSubroutine) GetName() string {
	return "AccountInfoFinalizer"
}

func (a *AccountInfoFinalizerSubroutine) Finalizers(_ lifecyclecontrollerruntime.RuntimeObject) []string {
	return []string{AccountInfoFinalizer}
}

func (a *AccountInfoFinalizerSubroutine) Process(_ context.Context, _ lifecyclecontrollerruntime.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil
}

func (a *AccountInfoFinalizerSubroutine) Finalize(ctx context.Context, instance lifecyclecontrollerruntime.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)
	_ = instance.(*accountv1alpha1.AccountInfo)

	cluster, err := a.mgr.ClusterFromContext(ctx)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	var apiBindings kcpapisv1alpha1.APIBindingList
	if err := cluster.GetClient().List(ctx, &apiBindings); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	for _, binding := range apiBindings.Items {
		if controllerutil.ContainsFinalizer(&binding, APIBindingFinalizer) {
			log.Debug().
				Str("apibinding", binding.Name).
				Msg("APIBinding still has finalizer, requeuing AccountInfo deletion")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
	}

	log.Info().Msg("No APIBindings with finalizer found, allowing AccountInfo deletion")
	return ctrl.Result{}, nil
}

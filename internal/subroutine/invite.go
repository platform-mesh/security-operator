package subroutine

import (
	"context"
	"fmt"

	kcpv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	accountv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/rs/zerolog/log"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/platform-mesh/golang-commons/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/platform-mesh/security-operator/api/v1alpha1"
)

func NewInviteSubroutine(orgsClient client.Client, mgr mcmanager.Manager) *inviteSubroutine {
	return &inviteSubroutine{
		orgsClient: orgsClient,
		mgr:        mgr,
	}
}

var _ lifecyclesubroutine.Subroutine = &inviteSubroutine{}

type inviteSubroutine struct {
	orgsClient client.Client
	mgr        mcmanager.Manager
}

func (w *inviteSubroutine) Finalize(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil
}

func (w *inviteSubroutine) Finalizers(_ runtimeobject.RuntimeObject) []string {
	return nil
}

func (w *inviteSubroutine) GetName() string { return "inviteSubroutine" }

func (w *inviteSubroutine) Process(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	lc := instance.(*kcpv1alpha1.LogicalCluster)
	wsName := getWorkspaceName(lc)

	cl, err := w.mgr.ClusterFromContext(ctx)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to get cluster from context %w", err), true, true)
	}

	var account accountv1alpha1.Account

	// to find a newly created organiztion account we need to point :root:orgs
	err = w.orgsClient.Get(ctx, types.NamespacedName{Name: wsName}, &account)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to get account resource %w", err), true, true)
	}

	// the Invite resource is created in :root:orgs:<new org> workspace
	obj := &v1alpha1.Invite{ObjectMeta: metav1.ObjectMeta{Name: wsName}}
	_, err = controllerutil.CreateOrUpdate(ctx, cl.GetClient(), obj, func() error {
		obj.Spec.Email = *account.Spec.Creator

		return nil
	})
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to create Invite resource %w", err), true, true)
	}
	log.Info().Msg(fmt.Sprintf("Invite resource for %s has been created", obj.Spec.Email))
	return ctrl.Result{}, nil
}

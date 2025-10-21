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

// this subroutine is responsible for Invite resource creation
// Invite resource reconcilation happens in the subroutine in invite package
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

	// to find a newly created organiztion account we need to point :root:orgs workspace
	err = w.orgsClient.Get(ctx, types.NamespacedName{Name: wsName}, &account)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to get account resource %w", err), true, true)
	}

	// the Invite resource is created in :root:orgs:<new org> workspace
	invite := &v1alpha1.Invite{ObjectMeta: metav1.ObjectMeta{Name: wsName}}
	_, err = controllerutil.CreateOrUpdate(ctx, cl.GetClient(), invite, func() error {
		invite.Spec.Email = *account.Spec.Creator

		return nil
	})
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to create invite resource %w", err), true, true)
	}
	log.Info().Msg(fmt.Sprintf("invite resource for %s has been created", invite.Spec.Email))

	conditions := invite.GetConditions()
	for _, condition := range conditions {
		if condition.Type == "Ready" {
			if condition.Status == metav1.ConditionTrue {
				log.Info().Msg(fmt.Sprintf("invite resource for %s is ready", invite.Spec.Email))
				return ctrl.Result{}, nil
			}
			log.Info().Msg(fmt.Sprintf("invite resource for %s is not ready yet (status: %s, reason: %s)", invite.Spec.Email, condition.Status, condition.Reason))
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("invite resource is not ready yet"), true, false)
		}
	}

	log.Info().Msg(fmt.Sprintf("no ready condition found for invite resource %s, requeuing", invite.Spec.Email))
	return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("no ready condition found for invite resource"), true, false)
}

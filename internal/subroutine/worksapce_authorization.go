package subroutine

import (
	"context"
	_ "embed"
	"fmt"

	kcpv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	kcptenancyv1alphav1 "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	lifecycleruntimeobject "github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type workspaceAuthSubroutine struct {
	client client.Client
}

func NewWorkspaceAuthConfigurationSubroutine(client client.Client) *workspaceAuthSubroutine {
	return &workspaceAuthSubroutine{
		client: client,
	}
}

var _ lifecyclesubroutine.Subroutine = &workspaceAuthSubroutine{}

func (r *workspaceAuthSubroutine) GetName() string { return "workspaceAuthConfiguration" }

func (r *workspaceAuthSubroutine) Finalizers() []string { return []string{} }

func (r *workspaceAuthSubroutine) Finalize(ctx context.Context, instance lifecycleruntimeobject.RuntimeObject) (reconcile.Result, errors.OperatorError) {
	return reconcile.Result{}, nil
}

func (r *workspaceAuthSubroutine) Process(ctx context.Context, instance lifecycleruntimeobject.RuntimeObject) (reconcile.Result, errors.OperatorError) {
	lc := instance.(*kcpv1alpha1.LogicalCluster)

	workspaceName := getWorkspaceName(lc)
	if workspaceName == "" {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to get workspace path"), true, false)
	}

	err := r.createWorkspaceAuthConfiguration(ctx, workspaceName)
	if err != nil {
		return reconcile.Result{}, errors.NewOperatorError(fmt.Errorf("failed to create WorkspaceAuthConfiguration resource: %w", err), true, true)
	}

	return ctrl.Result{}, nil
}

func (r *workspaceAuthSubroutine) createWorkspaceAuthConfiguration(ctx context.Context, resourceName string) error {
	obj := &kcptenancyv1alphav1.WorkspaceAuthenticationConfiguration{ObjectMeta: metav1.ObjectMeta{Name: resourceName}}
	//TODO configure spec.jwt section
	_, err := controllerutil.CreateOrUpdate(ctx, r.client, obj, nil)
	if err != nil {
		return err
	}
	return nil
}

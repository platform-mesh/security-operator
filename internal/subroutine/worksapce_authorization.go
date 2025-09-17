package subroutine

import (
	"context"
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

func (r *workspaceAuthSubroutine) createWorkspaceAuthConfiguration(ctx context.Context, workspaceName string) error {
	obj := &kcptenancyv1alphav1.WorkspaceAuthenticationConfiguration{ObjectMeta: metav1.ObjectMeta{Name: workspaceName}}

	_, err := controllerutil.CreateOrUpdate(ctx, r.client, obj, func() error {
		obj.Spec = kcptenancyv1alphav1.WorkspaceAuthenticationConfigurationSpec{
			JWT: []kcptenancyv1alphav1.JWTAuthenticator{
				{
					Issuer: kcptenancyv1alphav1.Issuer{
						URL:                 fmt.Sprintf("https://portal.dev.local:8443/keycloak/realms/%s", workspaceName),
						Audiences:           []string{workspaceName},
						AudienceMatchPolicy: kcptenancyv1alphav1.AudienceMatchPolicyMatchAny,
					},
					ClaimMappings: kcptenancyv1alphav1.ClaimMappings{
						Groups: kcptenancyv1alphav1.PrefixedClaimOrExpression{
							Claim:  "groups",
							Prefix: nil,
						},
					},
				},
			},
		}

		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

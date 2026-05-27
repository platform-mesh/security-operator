// TokenReview RBAC support for kubernetes-graphql-gateway cross-workspace auth.
//
// kubernetes-graphql-gateway validates end-user JWTs by calling the Kubernetes
// TokenReview API in the *target* workspace (e.g. root:orgs:org1:account1). The
// gateway runs with a provider-scoped kubeconfig from root:platform-mesh-system;
// kcp rewrites that identity cross-workspace to
// Group system:cluster:<gateway-home-cluster-id>. That group needs
// tokenreviews:create and system:kcp:workspace:access in every workspace the
// portal queries—not only org roots (root:orgs:<org>) but also account
// sub-workspaces (root:orgs:<org>:<account>, including nested accounts).
package subroutine

import (
	"context"
	"fmt"

	iclient "github.com/platform-mesh/security-operator/internal/client"
	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/subroutines"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

const (
	// Fixed object names written into each target workspace.
	gatewayTokenReviewClusterRoleName            = "platform-mesh:gateway-tokenreview"
	gatewayTokenReviewClusterRoleBindingName     = "platform-mesh:gateway-tokenreview"
	gatewayTokenReviewWorkspaceAccessBindingName = "platform-mesh:gateway-tokenreview-workspace-access"
	gatewayTokenReviewWorkspaceAccessClusterRole = "system:kcp:workspace:access"

	// Workspace where the gateway's provider kubeconfig is scoped (its "home").
	gatewayHomeWorkspacePath = "root:platform-mesh-system"
)

type tokenReviewRBACSubroutine struct {
	kcpClientGetter iclient.KCPClientGetter

	// cachedGatewayHomeClusterID is set after the first successful lookup only.
	// Transient lookup failures are not cached so controller-runtime backoff can retry.
	cachedGatewayHomeClusterID string
}

// NewTokenReviewRBACSubroutine ensures ClusterRole(Binding)s so the graphql
// gateway can perform TokenReview in org and account workspaces.
func NewTokenReviewRBACSubroutine(kcpClientGetter iclient.KCPClientGetter) *tokenReviewRBACSubroutine {
	return &tokenReviewRBACSubroutine{
		kcpClientGetter: kcpClientGetter,
	}
}

var (
	_ subroutines.Initializer = &tokenReviewRBACSubroutine{}
	_ subroutines.Processor   = &tokenReviewRBACSubroutine{}
)

func (r *tokenReviewRBACSubroutine) GetName() string { return "TokenReviewRBAC" }

func (r *tokenReviewRBACSubroutine) Initialize(ctx context.Context, obj client.Object) (subroutines.Result, error) {
	return r.reconcile(ctx, obj)
}

func (r *tokenReviewRBACSubroutine) Process(ctx context.Context, obj client.Object) (subroutines.Result, error) {
	return r.reconcile(ctx, obj)
}

func (r *tokenReviewRBACSubroutine) reconcile(ctx context.Context, obj client.Object) (subroutines.Result, error) {
	lc := obj.(*kcpcorev1alpha1.LogicalCluster)

	workspacePath := lc.Annotations["kcp.io/path"]
	if workspacePath == "" {
		return subroutines.OK(), fmt.Errorf("LogicalCluster %s has no kcp.io/path annotation", lc.Name)
	}

	gatewayHomeClusterID, err := r.gatewayHomeClusterID(ctx)
	if err != nil {
		return subroutines.OK(), fmt.Errorf("failed to resolve gateway home cluster ID: %w", err)
	}

	if err := r.ensureCrossWorkspaceTokenReviewRBAC(ctx, workspacePath, lc, gatewayHomeClusterID); err != nil {
		return subroutines.OK(), err
	}

	// The portal welcome page queries GraphQL at root:orgs (parent of all orgs).
	// Bindings on individual org/account workspaces alone are not enough for that path.
	if err := r.ensureParentOrgsWorkspaceBindings(ctx, workspacePath, gatewayHomeClusterID); err != nil {
		return subroutines.OK(), err
	}

	return subroutines.OK(), nil
}

func (r *tokenReviewRBACSubroutine) ensureParentOrgsWorkspaceBindings(ctx context.Context, workspacePath, gatewayHomeClusterID string) error {
	if workspacePath == config.OrgsClusterPath {
		return nil
	}
	if err := r.ensureCrossWorkspaceTokenReviewRBAC(ctx, config.OrgsClusterPath, nil, gatewayHomeClusterID); err != nil {
		return fmt.Errorf("failed to ensure gateway TokenReview RBAC in %s: %w", config.OrgsClusterPath, err)
	}
	return nil
}

// ensureCrossWorkspaceTokenReviewRBAC writes the ClusterRole and two ClusterRoleBindings
// that grant the gateway's cross-workspace identity permission to create TokenReviews
// and access the workspace in workspacePath.
func (r *tokenReviewRBACSubroutine) ensureCrossWorkspaceTokenReviewRBAC(
	ctx context.Context,
	workspacePath string,
	owner client.Object,
	gatewayHomeClusterID string,
) error {
	cl, err := r.kcpClientGetter.NewClientForLogicalCluster(ctx, workspacePath)
	if err != nil {
		return fmt.Errorf("failed to get client for workspace %s: %w", workspacePath, err)
	}

	callerGroup := crossWorkspaceGatewayCallerGroup(gatewayHomeClusterID)

	clusterRole := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: gatewayTokenReviewClusterRoleName}}
	_, err = controllerutil.CreateOrUpdate(ctx, cl, clusterRole, func() error {
		clusterRole.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{"authentication.k8s.io"},
				Resources: []string{"tokenreviews"},
				Verbs:     []string{"create"},
			},
		}
		if owner != nil {
			return controllerutil.SetOwnerReference(owner, clusterRole, cl.Scheme())
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to ensure gateway TokenReview ClusterRole in %s: %w", workspacePath, err)
	}

	tokenReviewBinding := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: gatewayTokenReviewClusterRoleBindingName}}
	_, err = controllerutil.CreateOrUpdate(ctx, cl, tokenReviewBinding, func() error {
		tokenReviewBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     gatewayTokenReviewClusterRoleName,
		}
		tokenReviewBinding.Subjects = []rbacv1.Subject{callerGroup}
		if owner != nil {
			return controllerutil.SetOwnerReference(owner, tokenReviewBinding, cl.Scheme())
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to ensure gateway TokenReview ClusterRoleBinding in %s: %w", workspacePath, err)
	}

	workspaceAccessBinding := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: gatewayTokenReviewWorkspaceAccessBindingName}}
	_, err = controllerutil.CreateOrUpdate(ctx, cl, workspaceAccessBinding, func() error {
		workspaceAccessBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     gatewayTokenReviewWorkspaceAccessClusterRole,
		}
		workspaceAccessBinding.Subjects = []rbacv1.Subject{callerGroup}
		if owner != nil {
			return controllerutil.SetOwnerReference(owner, workspaceAccessBinding, cl.Scheme())
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to ensure gateway workspace access ClusterRoleBinding in %s: %w", workspacePath, err)
	}

	return nil
}

func (r *tokenReviewRBACSubroutine) gatewayHomeClusterID(ctx context.Context) (string, error) {
	if r.cachedGatewayHomeClusterID != "" {
		return r.cachedGatewayHomeClusterID, nil
	}

	id, err := clusterIDFromWorkspacePath(ctx, r.kcpClientGetter, gatewayHomeWorkspacePath)
	if err != nil {
		return "", err
	}

	r.cachedGatewayHomeClusterID = id
	return id, nil
}

// crossWorkspaceGatewayCallerGroup is the kcp RBAC group for identities that
// originate from gatewayHomeClusterID and act in another workspace.
func crossWorkspaceGatewayCallerGroup(gatewayHomeClusterID string) rbacv1.Subject {
	return rbacv1.Subject{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Group",
		Name:     fmt.Sprintf("system:cluster:%s", gatewayHomeClusterID),
	}
}

// clusterIDFromWorkspacePath reads kcp.io/cluster from the LogicalCluster named
// "cluster" in workspacePath (same contract as account_tuples and apiexportpolicy).
func clusterIDFromWorkspacePath(ctx context.Context, kcpClientGetter iclient.KCPClientGetter, workspacePath string) (string, error) {
	cl, err := kcpClientGetter.NewClientForLogicalCluster(ctx, workspacePath)
	if err != nil {
		return "", fmt.Errorf("getting client for workspace %s: %w", workspacePath, err)
	}

	var lc kcpcorev1alpha1.LogicalCluster
	if err := cl.Get(ctx, client.ObjectKey{Name: "cluster"}, &lc); err != nil {
		return "", fmt.Errorf("getting logical cluster for path %s: %w", workspacePath, err)
	}

	clusterID, ok := lc.Annotations["kcp.io/cluster"]
	if !ok || clusterID == "" {
		return "", fmt.Errorf("kcp.io/cluster annotation not found on logical cluster %s", workspacePath)
	}

	return clusterID, nil
}

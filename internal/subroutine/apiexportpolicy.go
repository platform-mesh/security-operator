package subroutine

import (
	"context"
	"fmt"
	"slices"
	"strings"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	accountsv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	lifecyclecontrollerruntime "github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	corev1alpha1 "github.com/platform-mesh/security-operator/api/v1alpha1"
	iclient "github.com/platform-mesh/security-operator/internal/client"
	"github.com/platform-mesh/security-operator/pkg/fga"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/kcp-dev/logicalcluster/v3"
	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

const (
	orgsWorkspacePath     = "root:orgs"
	bindRelation          = "bind"
	bindInheritedRelation = "bind_inherited"
)

type APIExportPolicySubroutine struct {
	fga openfgav1.OpenFGAServiceClient
	mgr mcmanager.Manager
}

func NewAPIExportPolicySubroutine(fga openfgav1.OpenFGAServiceClient, mgr mcmanager.Manager) *APIExportPolicySubroutine {
	return &APIExportPolicySubroutine{
		fga: fga,
		mgr: mgr,
	}
}

var _ lifecyclesubroutine.Subroutine = &APIExportPolicySubroutine{}

func (a *APIExportPolicySubroutine) GetName() string {
	return "APIExportPolicySubroutine"
}

func (a *APIExportPolicySubroutine) Finalizers(_ lifecyclecontrollerruntime.RuntimeObject) []string {
	return []string{"authorization.platform-mesh.io/apiexportpolicy-finalizer"}
}

func (a *APIExportPolicySubroutine) Process(ctx context.Context, instance lifecyclecontrollerruntime.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)
	policy := instance.(*corev1alpha1.APIExportPolicy)

	providerClusterID, err := a.getClusterID(ctx, policy.Spec.APIExportRef.ClusterPath)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(
			fmt.Errorf("getting provider cluster ID for %s: %w", policy.Spec.APIExportRef.ClusterPath, err),
			true, true)
	}

	// Delete tuples for expressions that were removed from the spec
	if err := a.deleteRemovedExpressions(ctx, policy); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(
			fmt.Errorf("removing tuples for policy %s: %w", policy.Name, err),
			true, true)
	}

	for _, expression := range policy.Spec.AllowPathExpressions {
		workspacePath, relation, err := parseAllowExpression(expression)
		if err != nil {
			return ctrl.Result{}, errors.NewOperatorError(
				fmt.Errorf("parsing allow expression %s: %w", expression, err),
				true, true)
		}

		// for orgs workspace we need to write 1 tuple in every store
		// for this we need to get cluster id for every org's workspace
		if workspacePath == orgsWorkspacePath {
			allclient, err := iclient.NewForAllPlatformMeshResources(ctx, a.mgr.GetLocalManager().GetConfig(), a.mgr.GetLocalManager().GetScheme())
			if err != nil {
				log.Fatal().Err(err).Msg("unable to create all client")
			}

			var accountInfoList accountsv1alpha1.AccountInfoList
			if err := allclient.List(ctx, &accountInfoList); err != nil {
				return ctrl.Result{}, errors.NewOperatorError(
					fmt.Errorf("listing AccountInfo resources: %w", err),
					true, true)
			}

			for _, ai := range accountInfoList.Items {
				if ai.Spec.Account.Type != accountsv1alpha1.AccountTypeOrg {
					continue
				}

				if ai.Spec.FGA.Store.Id == "" {
					return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("store id is empty in account info %w", err), true, true)
				}

				tuple := corev1alpha1.Tuple{
					Object:   fmt.Sprintf("core_platform-mesh_io_account:%s/%s", ai.Spec.Account.OriginClusterId, ai.Spec.Account.Name),
					Relation: relation,
					User:     fmt.Sprintf("apis_kcp_io_apiexport:%s/%s", providerClusterID, policy.Spec.APIExportRef.Name),
				}

				tm := fga.NewTupleManager(a.fga, ai.Spec.FGA.Store.Id, fga.AuthorizationModelIDLatest, log)
				if err := tm.Apply(ctx, []corev1alpha1.Tuple{tuple}); err != nil {
					return ctrl.Result{}, errors.NewOperatorError(
						fmt.Errorf("applying tuple for expression %s: %w", expression, err),
						true, true)
				}
			}
			continue
		}

		lcClient, err := iclient.NewForLogicalCluster(a.mgr.GetLocalManager().GetConfig(), a.mgr.GetLocalManager().GetScheme(), logicalcluster.Name(workspacePath))
		if err != nil {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("getting client: %w", err), true, true)
		}

		var ai accountsv1alpha1.AccountInfo
		if err := lcClient.Get(ctx, client.ObjectKey{Name: "account"}, &ai); err != nil {
			return ctrl.Result{}, errors.NewOperatorError(
				fmt.Errorf("getting AccountInfo for workspace %s: %w", workspacePath, err),
				true, true)
		}

		tuple := corev1alpha1.Tuple{
			Object:   fmt.Sprintf("core_platform-mesh_io_account:%s/%s", ai.Spec.Account.OriginClusterId, ai.Spec.Account.Name),
			Relation: relation,
			User:     fmt.Sprintf("apis_kcp_io_apiexport:%s/%s", providerClusterID, policy.Spec.APIExportRef.Name),
		}

		tm := fga.NewTupleManager(a.fga, ai.Spec.FGA.Store.Id, fga.AuthorizationModelIDLatest, log)
		if err := tm.Apply(ctx, []corev1alpha1.Tuple{tuple}); err != nil {
			return ctrl.Result{}, errors.NewOperatorError(
				fmt.Errorf("applying tuple for expression %s: %w", expression, err),
				true, true)
		}
	}

	cluster, err := a.mgr.ClusterFromContext(ctx)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to get cluster from context %w", err), true, true)
	}

	// Update status with managed expressions
	original := policy.DeepCopy()
	policy.Status.ManagedAllowExpressions = policy.Spec.AllowPathExpressions

	if err := cluster.GetClient().Status().Patch(ctx, policy, client.MergeFrom(original)); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(
			fmt.Errorf("failed to patch APIExportPolicy status: %w", err),
			true, true)
	}

	log.Info().Msg("Successfully processed APIExportPolicy")
	return ctrl.Result{}, nil
}

func (a *APIExportPolicySubroutine) Finalize(ctx context.Context, instance lifecyclecontrollerruntime.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)
	policy := instance.(*corev1alpha1.APIExportPolicy)

	providerClusterID, err := a.getClusterID(ctx, policy.Spec.APIExportRef.ClusterPath)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(
			fmt.Errorf("getting provider cluster ID for %s: %w", policy.Spec.APIExportRef.ClusterPath, err),
			true, true)
	}

	for _, expression := range policy.Spec.AllowPathExpressions {
		err := a.deleteTuplesForExpression(ctx, expression, providerClusterID, policy.Spec.APIExportRef.Name)
		if err != nil {
			return ctrl.Result{}, errors.NewOperatorError(
				fmt.Errorf("deleting tuples for expression %s: %w", expression, err),
				true, true)
		}
	}

	log.Info().Msg("Finalized APIExportPolicy")
	return ctrl.Result{}, nil
}

func (a *APIExportPolicySubroutine) getClusterID(ctx context.Context, clusterPath string) (string, error) {
	lcClient, err := iclient.NewForLogicalCluster(a.mgr.GetLocalManager().GetConfig(), a.mgr.GetLocalManager().GetScheme(), logicalcluster.Name(clusterPath))
	if err != nil {
		return "", fmt.Errorf("getting client for workspace %s: %w", clusterPath, err)
	}

	var lc kcpcorev1alpha1.LogicalCluster
	if err := lcClient.Get(ctx, client.ObjectKey{Name: "cluster"}, &lc); err != nil {
		return "", fmt.Errorf("getting logical cluster for path %s: %w", clusterPath, err)
	}

	clusterID, ok := lc.Annotations["kcp.io/cluster"]
	if !ok {
		return "", fmt.Errorf("kcp.io/cluster annotation not found on logical cluster %s", clusterPath)
	}
	return clusterID, nil
}

func parseAllowExpression(expr string) (workspacePath string, relation string, err error) {
	expr = strings.TrimPrefix(expr, ":")

	if !strings.HasPrefix(expr, "root:orgs:") {
		return "", "", fmt.Errorf("invalid path expression: must start with root:orgs")
	}

	if strings.HasSuffix(expr, ":*") {
		// Wildcard pattern, use bind_inherited relation
		// Remove the trailing :*
		workspacePath = strings.TrimSuffix(expr, ":*")
		relation = bindInheritedRelation
		return workspacePath, relation, nil
	}
	return expr, bindRelation, nil
}

func (a *APIExportPolicySubroutine) deleteRemovedExpressions(ctx context.Context, policy *corev1alpha1.APIExportPolicy) error {
	providerClusterID, err := a.getClusterID(ctx, policy.Spec.APIExportRef.ClusterPath)
	if err != nil {
		return fmt.Errorf("getting provider cluster ID for %s: %w", policy.Spec.APIExportRef.ClusterPath, err)
	}

	for _, managedExpr := range policy.Status.ManagedAllowExpressions {
		exists := slices.Contains(policy.Spec.AllowPathExpressions, managedExpr)
		if exists {
			continue
		}

		err := a.deleteTuplesForExpression(ctx, managedExpr, providerClusterID, policy.Spec.APIExportRef.Name)
		if err != nil {
			return fmt.Errorf("removing tuples for expression %s: %w", managedExpr, err)
		}
	}
	return nil

}

func (a *APIExportPolicySubroutine) deleteTuplesForExpression(ctx context.Context, expression string, providerClusterID string, apiExportName string) error {
	log := logger.LoadLoggerFromContext(ctx)

	workspacePath, relation, err := parseAllowExpression(expression)
	if err != nil {
		return fmt.Errorf("parsing expression %s: %w", expression, err)
	}

	if workspacePath == orgsWorkspacePath {
		allclient, err := iclient.NewForAllPlatformMeshResources(ctx, a.mgr.GetLocalManager().GetConfig(), a.mgr.GetLocalManager().GetScheme())
		if err != nil {
			return fmt.Errorf("creating all client: %w", err)
		}

		var accountInfoList accountsv1alpha1.AccountInfoList
		if err := allclient.List(ctx, &accountInfoList); err != nil {
			return fmt.Errorf("listing AccountInfo resources for %s: %w", expression, err)
		}

		for _, ai := range accountInfoList.Items {
			if ai.Spec.FGA.Store.Id == "" {
				return fmt.Errorf("empty store id in AccountInfo resources %w", err)
			}

			tupleToDelete := corev1alpha1.Tuple{
				Object:   fmt.Sprintf("core_platform-mesh_io_account:%s/%s", ai.Spec.Account.OriginClusterId, ai.Spec.Account.Name),
				Relation: relation,
				User:     fmt.Sprintf("apis_kcp_io_apiexport:%s/%s", providerClusterID, apiExportName),
			}

			tm := fga.NewTupleManager(a.fga, ai.Spec.FGA.Store.Id, fga.AuthorizationModelIDLatest, log)
			if err := tm.Delete(ctx, []corev1alpha1.Tuple{tupleToDelete}); err != nil {
				return fmt.Errorf("removing tuple in openFGA: %w", err)
			}
		}
		return nil
	}

	lcClient, err := iclient.NewForLogicalCluster(a.mgr.GetLocalManager().GetConfig(), a.mgr.GetLocalManager().GetScheme(), logicalcluster.Name(workspacePath))
	if err != nil {
		return fmt.Errorf("getting client for workspace %s: %w", workspacePath, err)
	}

	var ai accountsv1alpha1.AccountInfo
	if err := lcClient.Get(ctx, client.ObjectKey{Name: "account"}, &ai); err != nil {
		return fmt.Errorf("getting AccountInfo for workspace %s: %w", workspacePath, err)
	}

	tupleToDelete := corev1alpha1.Tuple{
		Object:   fmt.Sprintf("core_platform-mesh_io_account:%s/%s", ai.Spec.Account.OriginClusterId, ai.Spec.Account.Name),
		Relation: relation,
		User:     fmt.Sprintf("apis_kcp_io_apiexport:%s/%s", providerClusterID, apiExportName),
	}

	tm := fga.NewTupleManager(a.fga, ai.Spec.FGA.Store.Id, fga.AuthorizationModelIDLatest, log)
	if err := tm.Delete(ctx, []corev1alpha1.Tuple{tupleToDelete}); err != nil {
		return fmt.Errorf("removing tuples: %w", err)
	}

	return nil
}

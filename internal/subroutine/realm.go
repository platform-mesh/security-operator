package subroutine

import (
	"context"
	"fmt"
	"strings"
	"time"

	kcpv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	lifecycleruntimeobject "github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/release"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type HelmClient struct {
	*action.Configuration
}

type realmSubroutine struct {
	k8s          client.Client
	helmClient   HelmClient
}

func NewRealmSubroutine(k8s client.Client, helmClient HelmClient) *realmSubroutine {
	return &realmSubroutine{
		k8s:          k8s,
		helmClient:   helmClient,
	}
}

const chartTemplatePath = "/orgIDP"

var _ lifecyclesubroutine.Subroutine = &realmSubroutine{}

func (r *realmSubroutine) GetName() string { return "Store" }

func (r *realmSubroutine) Finalizers() []string { return []string{"core.platform-mesh.io/fga-store"} }

func (r *realmSubroutine) Finalize(ctx context.Context, instance lifecycleruntimeobject.RuntimeObject) (reconcile.Result, errors.OperatorError) {
	// log := logger.LoadLoggerFromContext(ctx)
	// store := instance.(*v1alpha1.Store)

	return ctrl.Result{}, nil
}

func (r *realmSubroutine) Process(ctx context.Context, instance lifecycleruntimeobject.RuntimeObject) (reconcile.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)

	lc := instance.(*kcpv1alpha1.LogicalCluster)

	realmName := getWorkspaceName(lc)
	log.Info().Msg(fmt.Sprintf("realm NAME -- %s", realmName))

	installClient := action.NewInstall(r.helmClient.Configuration)
	installClient.ReleaseName = realmName
	installClient.Namespace = "default"
	installClient.CreateNamespace = true
	installClient.Timeout = 5 * time.Minute
	installClient.Wait = true

	log.Info().Msg("install client is initialized")

	chart, err := loader.Load(chartTemplatePath)
	if err != nil {
		log.Error().Msg(fmt.Sprintf("chart loading error -- %s", err))
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to load chart: %w", err), true, true)
	}

	values := map[string]interface{}{
		"crossplane": map[string]interface{}{
			"realm": map[string]interface{}{
				"name": realmName,
				"displayName": realmName,
			},
		},
	}

	rel, err := installClient.RunWithContext(ctx, chart, values)
	if err != nil {
		log.Error().Msg(fmt.Sprintf("helm release creation error -- %s", err))
		if rel != nil && rel.Info.Status == release.StatusDeployed {
			log.Info().Msg(fmt.Sprintf("release %s is already exist", realmName))
		}
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to create a helm release: %w", err), true, true)
	}

	return ctrl.Result{}, nil
}

func getWorkspaceName(lc *kcpv1alpha1.LogicalCluster) string {
	if path, ok := lc.Annotations["kcp.io/path"]; ok {
		pathElements := strings.Split(path, ":")
		return pathElements[len(pathElements)-1]
	}
	return ""
}

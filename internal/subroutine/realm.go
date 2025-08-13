package subroutine

import (
	"context"
	"fmt"
	"strings"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta2"
	kcpv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	lifecycleruntimeobject "github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"helm.sh/helm/v3/pkg/action"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type HelmClient struct {
	*action.Configuration
}

type realmSubroutine struct {
	k8s        client.Client
	orgsClient client.Client
}

func NewRealmSubroutine(k8s client.Client, orgsClient client.Client) *realmSubroutine {
	return &realmSubroutine{
		k8s:        k8s,
		orgsClient: orgsClient,
	}
}

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
	log.Info().Msg(fmt.Sprintf("realm name -- %s", realmName))

	gvk := schema.GroupVersionKind{
		Group:   "helm.toolkit.fluxcd.io",
		Version: "v2",
		Kind:    "HelmRelease",
	}

	if err := wait.PollUntilContextTimeout(ctx, 1*time.Second, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		if _, err := r.k8s.RESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version); err != nil {
			return false, nil
		}
		return true, nil
	}); err != nil {
		log.Error().Err(err).Msg("HelmRelease v2 API not available")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	nginxTest(ctx, r.k8s)

	return ctrl.Result{}, nil
}

func getWorkspaceName(lc *kcpv1alpha1.LogicalCluster) string {
	if path, ok := lc.Annotations["kcp.io/path"]; ok {
		pathElements := strings.Split(path, ":")
		return pathElements[len(pathElements)-1]
	}
	return ""
}

// func createOrUpdateHelmRelease(ctx context.Context, kubeClient client.Client, releaseName, namespace, chartName, chartVersion string, values map[string]interface{}) error {
// 	data, err := json.Marshal(values)
// 	if err != nil {
// 		return err
// 	}
// 	helmRelease := helmv2.HelmRelease{
// 		ObjectMeta: metav1.ObjectMeta{
// 			Name:      releaseName,
// 			Namespace: namespace,
// 		},
// 		Spec: helmv2.HelmReleaseSpec{
// 			ReleaseName: releaseName,
// 			Chart: &helmv2.HelmChartTemplate{
// 				Spec: helmv2.HelmChartTemplateSpec{
// 					Chart:   chartName,
// 					Version: chartVersion,
// 					SourceRef: helmv2.CrossNamespaceObjectReference{
// 						Kind:      "HelmRepository",
// 						Name:      "my-helm-repo",
// 						Namespace: "flux-system",
// 					},
// 				},
// 			},
// 			Values: &apiextensionsv1.JSON{data},
// 		},
// 	}

// 	existing := &helmv2.HelmRelease{}
// 	err = kubeClient.Get(ctx, types.NamespacedName{Name: releaseName, Namespace: namespace}, existing)
// 	if err == nil {
// 		existing.Spec = helmRelease.Spec
// 		return kubeClient.Update(ctx, existing)
// 	}
// 	return kubeClient.Create(ctx, &helmRelease)
// }

// func createOrUpdateOCIRepository(ctx context.Context, kubeClient client.Client, repoName, namespace, ociURL string) error {
// 	repo := &sourcev1.OCIRepository{
// 		ObjectMeta: metav1.ObjectMeta{
// 			Name:      repoName,
// 			Namespace: namespace,
// 		},
// 		Spec: sourcev1.OCIRepositorySpec{
// 			Interval: metav1.Duration{Duration: time.Minute},
// 			URL:      ociURL,
// 		},
// 	}

// 	existing := &sourcev1.OCIRepository{}
// 	err := kubeClient.Get(ctx, types.NamespacedName{Name: repoName, Namespace: namespace}, existing)
// 	if err == nil {
// 		existing.Spec = repo.Spec
// 		return kubeClient.Update(ctx, existing)
// 	}
// 	return kubeClient.Create(ctx, repo)
// }

func nginxTest(ctx context.Context, kubeClient client.Client) {

	helmRepository := &sourcev1.HelmRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bitnami",
			Namespace: "default",
		},
		Spec: sourcev1.HelmRepositorySpec{
			URL: "https://charts.bitnami.com/bitnami",
			Interval: metav1.Duration{
				Duration: 30 * time.Minute,
			},
		},
	}
	if err := kubeClient.Create(ctx, helmRepository); err != nil {
		fmt.Println(err)
	} else {
		fmt.Println("HelmRepository bitnami created")
	}

	helmRelease := &helmv2.HelmRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx",
			Namespace: "default",
		},
		Spec: helmv2.HelmReleaseSpec{
			ReleaseName: "nginx",
			Interval: metav1.Duration{
				Duration: 5 * time.Minute,
			},
			Chart: &helmv2.HelmChartTemplate{
				Spec: helmv2.HelmChartTemplateSpec{
					Chart:   "nginx",
					Version: "8.x",
					SourceRef: helmv2.CrossNamespaceObjectReference{
						Kind: sourcev1.HelmRepositoryKind,
						Name: "bitnami",
					},
				},
			},
			Values: &apiextensionsv1.JSON{Raw: []byte(`{"service": {"type": "ClusterIP"}}`)},
		},
	}
	if err := kubeClient.Create(ctx, helmRelease); err != nil {
		fmt.Println(err)
	} else {
		fmt.Println("HelmRelease nginx created")
	}

}

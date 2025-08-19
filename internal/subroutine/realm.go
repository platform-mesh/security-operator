package subroutine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/template"

	kcpv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	lifecycleruntimeobject "github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"helm.sh/helm/v3/pkg/action"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"
)

type HelmClient struct {
	*action.Configuration
}

type realmSubroutine struct {
	k8s client.Client
}

func NewRealmSubroutine(k8s client.Client) *realmSubroutine {
	return &realmSubroutine{
		k8s: k8s,
	}
}

const (
	//TODO move it in operator config
	manifestsPath = "/operator/manifests/"
)

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

	values := apiextensionsv1.JSON{}

	patch := map[string]interface{}{
		"crossplane": map[string]interface{}{
			"realm": map[string]interface{}{
				"name":        realmName,
				"displayName": realmName,
			},
		},
		"clients": map[string]interface{}{
			"organization": map[string]interface{}{
				"name": realmName,
			},
		},
	}

	marshalledPatch, err := json.Marshal(patch)
	if err != nil {
		log.Err(err).Msg("cannot marshall path map")
		return ctrl.Result{}, nil
	}

	values.Raw = marshalledPatch

	OCIpath := manifestsPath + "organizationIDP/repository.yaml"

	err = applyManifestFromFileWithMergedValues(ctx, OCIpath, r.k8s, nil)
	if err != nil {
		log.Error().Err(err).Msg("Cannot create OCI repository")
		return ctrl.Result{}, nil
	}

	ReleasePath := manifestsPath + "organizationIDP/helmrelease.yaml"

	err = applyReleaseWithValues(ctx, ReleasePath, r.k8s, values, realmName)
	if err != nil {
		log.Error().Err(err).Msg("Cannot create helm release")
		return ctrl.Result{}, nil
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

func applyManifestFromFileWithMergedValues(ctx context.Context, path string, k8sClient client.Client, templateData map[string]string) error {
	log := logger.LoadLoggerFromContext(ctx)

	obj, err := unstructuredFromFile(path, templateData, log)
	if err != nil {
		return err
	}

	err = k8sClient.Patch(ctx, &obj, client.Apply, client.FieldOwner("security-operator"))
	if err != nil {
		return errors.Wrap(err, "Failed to apply manifest file: %s (%s/%s)", path, obj.GetKind(), obj.GetName())
	}
	return nil
}

func applyReleaseWithValues(ctx context.Context, path string, k8sClient client.Client, values apiextensionsv1.JSON, orgName string) error {
	log := logger.LoadLoggerFromContext(ctx)

	obj, err := unstructuredFromFile(path, map[string]string{}, log)
	if err != nil {
		return errors.Wrap(err, "Failed to get unstructuredFromFile")
	}
	obj.SetName(orgName)

	if err := unstructured.SetNestedField(obj.Object, orgName, "spec", "releaseName"); err != nil {
		return errors.Wrap(err, "failed to set spec.releaseName")
	}

	obj.Object["spec"].(map[string]interface{})["values"] = values

	err = k8sClient.Patch(ctx, &obj, client.Apply, client.FieldOwner("security-operator"))
	if err != nil {
		return errors.Wrap(err, "Failed to apply manifest file: %s (%s/%s)", path, obj.GetKind(), obj.GetName())
	}
	return nil
}

func unstructuredFromFile(path string, templateData map[string]string, log *logger.Logger) (unstructured.Unstructured, error) {
	manifestBytes, err := os.ReadFile(path)
	if err != nil {
		return unstructured.Unstructured{}, errors.Wrap(err, "Failed to read file, pwd: %s", path)
	}

	res, err := ReplaceTemplate(templateData, manifestBytes)
	if err != nil {
		return unstructured.Unstructured{}, errors.Wrap(err, "Failed to replace template with path: %s", path)
	}

	var objMap map[string]interface{}
	if err := yaml.Unmarshal(res, &objMap); err != nil {
		return unstructured.Unstructured{}, errors.Wrap(err, "Failed to unmarshal YAML from template %s. Output:\n%s", path, string(res))
	}

	log.Debug().Str("obj", fmt.Sprintf("%+v", objMap)).Msg("Unmarshalled object")

	obj := unstructured.Unstructured{Object: objMap}

	log.Debug().Str("file", path).Str("kind", obj.GetKind()).Str("name", obj.GetName()).Str("namespace", obj.GetNamespace()).Msg("Applying manifest")
	return obj, err
}

func ReplaceTemplate(templateData map[string]string, templateBytes []byte) ([]byte, error) {
	tmpl, err := template.New("manifest").Parse(string(templateBytes))
	if err != nil {
		return []byte{}, errors.Wrap(err, "Failed to parse template")
	}
	var result bytes.Buffer
	err = tmpl.Execute(&result, templateData)
	if err != nil {
		keys := make([]string, 0, len(templateData))
		for k := range templateData {
			keys = append(keys, k)
		}
		return []byte{}, errors.Wrap(err, "Failed to execute template with keys %v", keys)
	}
	if result.Len() == 0 {
		return []byte{}, nil
	}
	return result.Bytes(), nil
}

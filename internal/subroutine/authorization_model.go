package subroutine

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strings"
	"text/template"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	language "github.com/openfga/language/pkg/go/transformer"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/security-operator/api/v1alpha1"
	"google.golang.org/protobuf/encoding/protojson"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
)

const (
	schemaVersion = "1.2"
)

var (
	priviledgedGroupVersions = []string{"rbac.authorization.k8s.io/v1"}
	groupVersions            = []string{"authentication.k8s.io/v1", "authorization.k8s.io/v1", "v1"}

	coreModelTemplate = template.Must(template.New("model").Parse(`module internal_core_types_{{ .Name }}
type {{ .Group }}_{{ .Singular }}
	relations
		define parent: [{{ if eq .Scope "Namespaced" }}core_namespace{{ else }}core_platform-mesh_io_account{{ end }}]
		define member: [role#assignee] or owner or member from parent
		define owner: [role#assignee] or owner from parent
		
		define get: member
		define update: member
		define delete: member
		define patch: member
		define watch: member

		define manage_iam_roles: owner
		define get_iam_roles: member
		define get_iam_users: member
`))
	priviledgedTemplate = template.Must(template.New("model").Parse(`module internal_core_types_{{ .Name }}
type {{ .Group }}_{{ .Singular }}
	relations
		define parent: [{{ if eq .Scope "Namespaced" }}core_namespace{{ else }}core_platform-mesh_io_account{{ end }}]
		define member: [role#assignee] or owner or member from parent
		define owner: [role#assignee] or owner from parent
		
		define get: member
		define update: owner
		define delete: owner
		define patch: owner
		define watch: member

		define manage_iam_roles: owner
		define get_iam_roles: member
		define get_iam_users: member
`))
)

type authorizationModelSubroutine struct {
	fga       openfgav1.OpenFGAServiceClient
	mgr       mcmanager.Manager
	allClient client.Client
}

func NewAuthorizationModelSubroutine(fga openfgav1.OpenFGAServiceClient, mgr mcmanager.Manager, allClient client.Client, log *logger.Logger) *authorizationModelSubroutine {
	return &authorizationModelSubroutine{
		fga:       fga,
		mgr:       mgr,
		allClient: allClient,
	}
}

var _ subroutine.Subroutine = &authorizationModelSubroutine{}

func (a *authorizationModelSubroutine) Finalizers(_ runtimeobject.RuntimeObject) []string { return nil }

func (a *authorizationModelSubroutine) GetName() string { return "AuthorizationModel" }

func (a *authorizationModelSubroutine) Finalize(ctx context.Context, instance runtimeobject.RuntimeObject) (reconcile.Result, errors.OperatorError) {
	return ctrl.Result{}, nil
}

func getRelatedAuthorizationModels(ctx context.Context, k8s client.Client, store *v1alpha1.Store) (v1alpha1.AuthorizationModelList, error) {

	storeClusterKey, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return v1alpha1.AuthorizationModelList{}, fmt.Errorf("unable to get cluster key from context")
	}

	allCtx := mccontext.WithCluster(ctx, "")
	allAuthorizationModels := v1alpha1.AuthorizationModelList{}

	if err := k8s.List(allCtx, &allAuthorizationModels); err != nil {
		return v1alpha1.AuthorizationModelList{}, err
	}

	var extendingModules v1alpha1.AuthorizationModelList
	for _, model := range allAuthorizationModels.Items {
		if model.Spec.StoreRef.Name != store.Name || model.Spec.StoreRef.Path != storeClusterKey {
			continue
		}

		extendingModules.Items = append(extendingModules.Items, model)
	}

	return extendingModules, nil
}

func (a *authorizationModelSubroutine) Process(ctx context.Context, instance runtimeobject.RuntimeObject) (reconcile.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)
	store := instance.(*v1alpha1.Store)

	extendingModules, err := getRelatedAuthorizationModels(ctx, a.allClient, store)
	if err != nil {
		log.Error().Err(err).Msg("unable to get related authorization models")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	moduleFiles := []language.ModuleFile{{
		Name:     fmt.Sprintf("%s.fga", client.ObjectKeyFromObject(store)),
		Contents: store.Spec.CoreModule,
	}}
	for _, module := range extendingModules.Items {
		moduleFiles = append(moduleFiles, language.ModuleFile{
			Name:     fmt.Sprintf("%s.fga", client.ObjectKeyFromObject(&module)),
			Contents: module.Spec.Model,
		})
	}

	if store.Name != "orgs" {
		cfg := rest.CopyConfig(a.mgr.GetLocalManager().GetConfig())

		parsed, err := url.Parse(cfg.Host)
		if err != nil {
			log.Error().Err(err).Msg("unable to parse host from config")
			return ctrl.Result{}, errors.NewOperatorError(err, true, false)
		}

		parsed.Path, err = url.JoinPath("clusters", fmt.Sprintf("root:orgs:%s", store.Name))
		if err != nil {
			log.Error().Err(err).Msg("unable to join path")
			return ctrl.Result{}, errors.NewOperatorError(err, true, false)
		}

		cfg.Host = parsed.String()

		discoveryClient := discovery.NewDiscoveryClientForConfigOrDie(cfg)

		coreModules, err := discoverAndRender(discoveryClient, coreModelTemplate, groupVersions)
		if err != nil {
			return ctrl.Result{}, errors.NewOperatorError(err, true, false)
		}
		moduleFiles = append(moduleFiles, coreModules...)

		priviledgedModules, err := discoverAndRender(discoveryClient, priviledgedTemplate, priviledgedGroupVersions)
		if err != nil {
			return ctrl.Result{}, errors.NewOperatorError(err, true, false)
		}
		moduleFiles = append(moduleFiles, priviledgedModules...)
	}

	authorizationModel, err := language.TransformModuleFilesToModel(moduleFiles, schemaVersion)
	if err != nil {
		log.Error().Err(err).Msg("unable to transform module files to model")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	if store.Status.AuthorizationModelID != "" {
		res, err := a.fga.ReadAuthorizationModel(ctx, &openfgav1.ReadAuthorizationModelRequest{
			StoreId: store.Status.StoreID,
			Id:      store.Status.AuthorizationModelID,
		})
		if err != nil {
			// TODO: if its not found we could continue with just writing the model again
			log.Error().Err(err).Msg("unable to read authorization model")
			return ctrl.Result{}, errors.NewOperatorError(err, true, false)
		}

		// the following ignore comments are due to the fact, that its incredibly hard to setup a specific condition where one of the parsing methods would fail

		currentRaw, err := protojson.Marshal(res.AuthorizationModel)
		if err != nil { // coverage-ignore
			log.Error().Err(err).Msg("unable to marshal current model")
			return ctrl.Result{}, errors.NewOperatorError(err, true, false)
		}

		current, err := language.TransformJSONStringToDSL(string(currentRaw))
		if err != nil { // coverage-ignore
			log.Error().Err(err).Msg("unable to transform current model to dsl")
			return ctrl.Result{}, errors.NewOperatorError(err, true, false)
		}

		desiredRaw, err := protojson.Marshal(authorizationModel)
		if err != nil { // coverage-ignore
			log.Error().Err(err).Msg("unable to marshal desired model")
			return ctrl.Result{}, errors.NewOperatorError(err, true, false)
		}

		desired, err := language.TransformJSONStringToDSL(string(desiredRaw))
		if err != nil { // coverage-ignore
			log.Error().Err(err).Msg("unable to transform desired model to dsl")
			return ctrl.Result{}, errors.NewOperatorError(err, true, false)
		}

		if *current == *desired {
			return ctrl.Result{}, nil
		}

	}

	res, err := a.fga.WriteAuthorizationModel(ctx, &openfgav1.WriteAuthorizationModelRequest{
		StoreId:         store.Status.StoreID,
		TypeDefinitions: authorizationModel.TypeDefinitions,
		SchemaVersion:   schemaVersion,
		Conditions:      authorizationModel.Conditions,
	})
	if err != nil {
		log.Error().Err(err).Msg("unable to write authorization model")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	store.Status.AuthorizationModelID = res.AuthorizationModelId

	return ctrl.Result{}, nil
}

func processAPIResourceIntoModel(resource metav1.APIResource, tpl *template.Template) (bytes.Buffer, error) {

	scope := apiextensionsv1.ClusterScoped
	if resource.Namespaced {
		scope = apiextensionsv1.NamespaceScoped
	}

	group := "core"
	if resource.Group != "" {
		group = resource.Group
	}

	var buffer bytes.Buffer
	err := tpl.Execute(&buffer, modelInput{
		Name:     resource.Name,
		Group:    strings.ReplaceAll(group, ".", "_"),
		Singular: resource.SingularName,
		Scope:    string(scope),
	})
	if err != nil {
		return buffer, err
	}

	return buffer, nil
}

func discoverAndRender(dc discovery.DiscoveryInterface, tpl *template.Template, groupVersions []string) ([]language.ModuleFile, error) {
	var files []language.ModuleFile
	for _, gv := range groupVersions {
		resourceList, err := dc.ServerResourcesForGroupVersion(gv)
		if err != nil {
			return nil, fmt.Errorf("discover resources for %s: %w", gv, err)
		}

		for _, apiRes := range resourceList.APIResources {
			if strings.Contains(apiRes.Name, "/") { // skip subresources
				continue
			}

			buf, err := processAPIResourceIntoModel(apiRes, tpl)
			if err != nil {
				return nil, fmt.Errorf("process api resource %s in %s: %w", apiRes.Name, gv, err)
			}

			files = append(files, language.ModuleFile{
				Name:     fmt.Sprintf("internal_core_types_%s.fga", apiRes.Name),
				Contents: buf.String(),
			})
		}
	}
	return files, nil
}

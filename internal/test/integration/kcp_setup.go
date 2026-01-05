package test

import (
	"context"
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	kcpapiv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	apisv1alpha2 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha2"
	"github.com/kcp-dev/kcp/sdk/apis/core"
	kcpcorev1alpha1 "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	tenancyv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"
	clusterclient "github.com/kcp-dev/multicluster-provider/client"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(clientgoscheme.Scheme))
	utilruntime.Must(kcpapiv1alpha1.AddToScheme(clientgoscheme.Scheme))
	utilruntime.Must(apisv1alpha2.AddToScheme(clientgoscheme.Scheme))
	utilruntime.Must(kcpcorev1alpha1.AddToScheme(clientgoscheme.Scheme))
	utilruntime.Must(tenancyv1alpha1.AddToScheme(clientgoscheme.Scheme))
}

var (
	//go:embed yaml/apiresourceschema-accountinfos.core.platform-mesh.io.yaml
	accountInfoSchemaYAML []byte

	//go:embed yaml/apiresourceschema-accounts.core.platform-mesh.io.yaml
	accountSchemaYAML []byte

	//go:embed yaml/apiresourceschema-authorizationmodels.core.platform-mesh.io.yaml
	authorizationModelSchemaYAML []byte

	//go:embed yaml/apiresourceschema-stores.core.platform-mesh.io.yaml
	storeSchemaYAML []byte

	//go:embed yaml/apiexport-core.platform-mesh.io.yaml
	apiExportPlatformMeshYAML []byte

	//go:embed yaml/apibinding-core-platform-mesh.io.yaml
	apiBindingCorePlatformMeshYAML []byte

	//go:embed yaml/workspace-type-org.yaml
	workspaceTypeOrgYAML []byte

	//go:embed yaml/workspace-type-orgs.yaml
	workspaceTypeOrgsYAML []byte

	//go:embed yaml/workspace-type-account.yaml
	workspaceTypeAccountYAML []byte
)

func KcpSetup(ctx context.Context, kubeconfig string) error {
	cfg, err := loadKCPConfig(kubeconfig)
	if err != nil {
		return err
	}

	cli, err := clusterclient.New(cfg, client.Options{Scheme: clientgoscheme.Scheme})
	if err != nil {
		return fmt.Errorf("failed to build cluster client: %w", err)
	}

	rootPath := logicalcluster.NewPath("root")
	pmsPath, err := ensureWorkspace(ctx, rootPath, "platform-mesh-system", nil, cli)
	if err != nil {
		return err
	}

	// Create APIResourceSchemas, APIExport, APIBinding in platform-mesh-system
	pmsClient := cli.Cluster(pmsPath)
	for _, schemaYAML := range [][]byte{accountInfoSchemaYAML, accountSchemaYAML, authorizationModelSchemaYAML, storeSchemaYAML} {
		var schema kcpapiv1alpha1.APIResourceSchema
		if err := yaml.Unmarshal(schemaYAML, &schema); err != nil {
			return fmt.Errorf("failed to unmarshal APIResourceSchema: %w", err)
		}
		if err := pmsClient.Create(ctx, &schema); err != nil && !kerrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create APIResourceSchema/%s: %w", schema.Name, err)
		}
		fmt.Printf("applied APIResourceSchema/%s\n", schema.Name)
	}

	var exp kcpapiv1alpha1.APIExport
	if err := yaml.Unmarshal(apiExportPlatformMeshYAML, &exp); err != nil {
		return fmt.Errorf("failed to unmarshal APIExport: %w", err)
	}
	if err := pmsClient.Create(ctx, &exp); err != nil && !kerrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create APIExport/%s: %w", exp.Name, err)
	}
	fmt.Printf("applied APIExport/%s\n", exp.Name)

	var b apisv1alpha2.APIBinding
	if err := yaml.Unmarshal(apiBindingCorePlatformMeshYAML, &b); err != nil {
		return fmt.Errorf("failed to unmarshal APIBinding: %w", err)
	}
	if err := pmsClient.Create(ctx, &b); err != nil && !kerrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create APIBinding/%s: %w", b.Name, err)
	}
	fmt.Printf("applied APIBinding/%s\n", b.Name)

	rootClient := cli.Cluster(core.RootCluster.Path())

	for _, wtYAML := range [][]byte{workspaceTypeOrgYAML, workspaceTypeOrgsYAML, workspaceTypeAccountYAML} {
		var wt tenancyv1alpha1.WorkspaceType
		if err := yaml.Unmarshal(wtYAML, &wt); err != nil {
			return fmt.Errorf("failed to unmarshal WorkspaceType: %w", err)
		}
		if err := rootClient.Create(ctx, &wt); err != nil && !kerrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create WorkspaceType/%s: %w", wt.Name, err)
		}
		fmt.Printf("applied WorkspaceType/%s\n", wt.Name)
	}

	orgsPath, err := ensureWorkspace(ctx, rootPath, "orgs", &tenancyv1alpha1.WorkspaceTypeReference{
		Name: "orgs",
		Path: rootPath.String(),
	}, cli)
	if err != nil {
		return err
	}

	_, err = ensureWorkspace(ctx, orgsPath, "test", &tenancyv1alpha1.WorkspaceTypeReference{
		Name: "org",
		Path: rootPath.String(),
	}, cli)
	if err != nil {
		return err
	}

	_, err = ensureWorkspace(ctx, orgsPath, "no-reconcile-org", &tenancyv1alpha1.WorkspaceTypeReference{
		Name: "org",
		Path: rootPath.String(),
	}, cli)
	if err != nil {
		return err
	}

	return nil
}

func loadKCPConfig(kubeconfigPath string) (*rest.Config, error) {
	if kubeconfigPath != "" {
		return nil, fmt.Errorf("explicit kubeconfig path is not supported; set KUBECONFIG instead")
	}

	kubeconfig := os.Getenv("KUBECONFIG")
	if strings.TrimSpace(kubeconfig) == "" {
		return nil, fmt.Errorf("KUBECONFIG is not set")
	}

	rawCfg, err := clientcmd.LoadFromFile(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig from %q: %w", kubeconfig, err)
	}

	cfg, err := clientcmd.NewDefaultClientConfig(*rawCfg, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to build rest config from %q: %w", kubeconfig, err)
	}

	parsed, err := url.Parse(cfg.Host)
	if err != nil {
		return nil, fmt.Errorf("failed to parse kubeconfig host %q: %w", cfg.Host, err)
	}

	if strings.HasPrefix(parsed.Path, "/clusters/") {
		parsed.Path = ""
		cfg = rest.CopyConfig(cfg)
		cfg.Host = parsed.String()
	}
	return cfg, nil
}

func ensureWorkspace(ctx context.Context, parentPath logicalcluster.Path, name string, wsType *tenancyv1alpha1.WorkspaceTypeReference, cli clusterclient.ClusterClient) (logicalcluster.Path, error) {
	wsPath := logicalcluster.NewPath(fmt.Sprintf("%s:%s", parentPath.String(), name))
	ws := &tenancyv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: tenancyv1alpha1.WorkspaceSpec{},
	}
	if wsType != nil {
		ws.Spec.Type = wsType
	}

	err := cli.Cluster(parentPath).Create(ctx, ws)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		return wsPath, fmt.Errorf("failed to create workspace %s under %s: %w", name, parentPath, err)
	}
	fmt.Printf("workspace %s created (or existed) at %s\n", name, wsPath)

	// Wait until the workspace is ready
	for i := 0; i < 240; i++ {
		var current tenancyv1alpha1.Workspace
		getErr := cli.Cluster(parentPath).Get(ctx, client.ObjectKey{Name: name}, &current)
		if getErr == nil && string(current.Status.Phase) == "Ready" {
			return wsPath, nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	return wsPath, nil
}

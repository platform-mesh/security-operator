package subroutine

import (
	"context"
	"fmt"
	"slices"
	"strings"

	accountv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/ratelimiter"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/rs/zerolog/log"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"

	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

const (
	kubectlClientName = "kubectl"
	secretNamespace   = "default"
)

func NewIDPSubroutine(orgsClient client.Client, mgr mcmanager.Manager, cfg config.Config) *IDPSubroutine {
	limiter, _ := ratelimiter.NewStaticThenExponentialRateLimiter[*v1alpha1.IdentityProviderConfiguration](ratelimiter.NewConfig()) //nolint:errcheck
	return &IDPSubroutine{
		orgsClient:                orgsClient,
		mgr:                       mgr,
		additionalRedirectURLs:    cfg.IDP.AdditionalRedirectURLs,
		kubectlClientRedirectURLs: cfg.IDP.KubectlClientRedirectURLs,
		baseDomain:                cfg.BaseDomain,
		limiter:                   limiter,
	}
}

var _ lifecyclesubroutine.Subroutine = &IDPSubroutine{}

type IDPSubroutine struct {
	orgsClient                client.Client
	mgr                       mcmanager.Manager
	additionalRedirectURLs    []string
	kubectlClientRedirectURLs []string
	baseDomain                string
	limiter                   workqueue.TypedRateLimiter[*v1alpha1.IdentityProviderConfiguration]
}

func (i *IDPSubroutine) Finalize(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil
}

func (i *IDPSubroutine) Finalizers(_ runtimeobject.RuntimeObject) []string {
	return nil
}

func (i *IDPSubroutine) GetName() string { return "IDPSubroutine" }

func (i *IDPSubroutine) Process(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	lc := instance.(*kcpcorev1alpha1.LogicalCluster)

	workspaceName := getWorkspaceName(lc)
	if workspaceName == "" {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to get workspace name"), true, false)
	}

	cl, err := i.mgr.ClusterFromContext(ctx)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to get cluster from context %w", err), true, true)
	}

	var account accountv1alpha1.Account
	err = i.orgsClient.Get(ctx, types.NamespacedName{Name: workspaceName}, &account)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to get account resource %w", err), true, true)
	}

	if account.Spec.Type != accountv1alpha1.AccountTypeOrg {
		log.Debug().Str("workspace", workspaceName).Msg("account is not of type organization, skipping idp creation")
		return ctrl.Result{}, nil
	}

	clients := []v1alpha1.IdentityProviderClientConfig{
		{
			ClientName:             workspaceName,
			ClientType:             v1alpha1.IdentityProviderClientTypeConfidential,
			RedirectURIs:           append(i.additionalRedirectURLs, fmt.Sprintf("https://%s.%s/*", workspaceName, i.baseDomain)),
			PostLogoutRedirectURIs: []string{fmt.Sprintf("https://%s.%s/logout*", workspaceName, i.baseDomain)},
			SecretRef: corev1.SecretReference{
				Name:      fmt.Sprintf("portal-client-secret-%s-%s", workspaceName, workspaceName),
				Namespace: secretNamespace,
			},
		},
		{
			ClientName:   kubectlClientName,
			ClientType:   v1alpha1.IdentityProviderClientTypePublic,
			RedirectURIs: i.kubectlClientRedirectURLs,
			SecretRef: corev1.SecretReference{
				Name:      fmt.Sprintf("portal-client-secret-%s-%s", workspaceName, kubectlClientName),
				Namespace: secretNamespace,
			},
		},
	}

	idp := &v1alpha1.IdentityProviderConfiguration{ObjectMeta: metav1.ObjectMeta{Name: workspaceName}}
	_, err = controllerutil.CreateOrPatch(ctx, cl.GetClient(), idp, func() error {
		for _, desired := range clients {
			idp.Spec.Clients = ensureClient(idp.Spec.Clients, desired)
		}
		return nil
	})
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to create idp resource %w", err), true, true)
	}

	log.Info().Str("workspace", workspaceName).Msg("idp configuration resource is created")

	if err := cl.GetClient().Get(ctx, types.NamespacedName{Name: workspaceName}, idp); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to get idp resource %w", err), true, true)
	}

	if !meta.IsStatusConditionTrue(idp.GetConditions(), "Ready") {
		log.Debug().Str("workspace", workspaceName).Msg("idp resource is not ready yet, requeuing")
		return ctrl.Result{RequeueAfter: i.limiter.When(idp)}, nil
	}

	if len(idp.Spec.Clients) == 0 || len(idp.Status.ManagedClients) == 0 {
		return reconcile.Result{}, errors.NewOperatorError(fmt.Errorf("IdentityProviderConfiguration %s has no clients in spec or status", workspaceName), true, false)
	}

	for _, specClient := range idp.Spec.Clients {
		managedClient, ok := idp.Status.ManagedClients[specClient.ClientName]
		if !ok {
			return reconcile.Result{}, errors.NewOperatorError(fmt.Errorf("managed client %s not found in IdentityProviderConfiguration status", specClient.ClientName), true, false)
		}
		if managedClient.ClientID == "" {
			return reconcile.Result{}, errors.NewOperatorError(fmt.Errorf("managed client %s has empty ClientID in IdentityProviderConfiguration status", specClient.ClientName), true, false)
		}
	}

	i.limiter.Forget(idp)

	if err := i.patchAccountInfo(ctx, cl.GetClient(), workspaceName, idp); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("unable to update accountInfo: %w", err), true, true)
	}

	log.Info().Str("workspace", workspaceName).Msg("idp resource is ready")
	return ctrl.Result{}, nil
}

func (i *IDPSubroutine) patchAccountInfo(ctx context.Context, cl client.Client, workspaceName string, idp *v1alpha1.IdentityProviderConfiguration) error {
	accountInfo := accountv1alpha1.AccountInfo{
		ObjectMeta: metav1.ObjectMeta{Name: "account"},
	}
	if err := cl.Get(ctx, types.NamespacedName{Name: "account"}, &accountInfo); err != nil {
		return fmt.Errorf("failed to get accountInfo: %w", err)
	}

	desiredIssuerURL := fmt.Sprintf("https://%s/keycloak/realms/%s", i.baseDomain, workspaceName)
	desiredClients := make(map[string]accountv1alpha1.ClientInfo)
	for clientName, managedClient := range idp.Status.ManagedClients {
		desiredClients[clientName] = accountv1alpha1.ClientInfo{
			ClientID: managedClient.ClientID,
		}
	}

	desiredOIDC := &accountv1alpha1.OIDCInfo{
		IssuerURL: desiredIssuerURL,
		Clients:   desiredClients,
	}

	if equality.Semantic.DeepEqual(accountInfo.Spec.OIDC, desiredOIDC) {
		log.Debug().Str("workspace", workspaceName).Msg("accountInfo OIDC configuration already up to date, skip patching")
		return nil
	}

	original := accountInfo.DeepCopy()
	accountInfo.Spec.OIDC = desiredOIDC

	if err := cl.Patch(ctx, &accountInfo, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("failed to patch accountInfo: %w", err)
	}
	return nil
}

// ensureClient updates only fields managed by this subroutine, preserving ClientID and RegistrationClientURI
// that are set by reconciling an idp resource
func ensureClient(existing []v1alpha1.IdentityProviderClientConfig, desired v1alpha1.IdentityProviderClientConfig) []v1alpha1.IdentityProviderClientConfig {
	idx := slices.IndexFunc(existing, func(c v1alpha1.IdentityProviderClientConfig) bool {
		return c.ClientName == desired.ClientName
	})

	if idx != -1 {
		existing[idx].ClientType = desired.ClientType
		existing[idx].RedirectURIs = desired.RedirectURIs
		existing[idx].PostLogoutRedirectURIs = desired.PostLogoutRedirectURIs
		existing[idx].SecretRef = desired.SecretRef
		return existing
	}

	return append(existing, desired)
}

func getWorkspaceName(lc *kcpcorev1alpha1.LogicalCluster) string {
	if path, ok := lc.Annotations["kcp.io/path"]; ok {
		pathElements := strings.Split(path, ":")
		return pathElements[len(pathElements)-1]
	}
	return ""
}

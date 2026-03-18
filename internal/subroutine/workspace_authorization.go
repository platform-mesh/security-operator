package subroutine

import (
	"context"
	"fmt"

	"github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/subroutines"
	"github.com/rs/zerolog/log"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
	kcptenancyv1alphav1 "github.com/kcp-dev/sdk/apis/tenancy/v1alpha1"
)

type workspaceAuthSubroutine struct {
	orgClient     client.Client
	runtimeClient client.Client
	mgr           mcmanager.Manager
	cfg           config.Config
}

func NewWorkspaceAuthConfigurationSubroutine(orgClient, runtimeClient client.Client, mgr mcmanager.Manager, cfg config.Config) *workspaceAuthSubroutine {
	return &workspaceAuthSubroutine{
		orgClient:     orgClient,
		runtimeClient: runtimeClient,
		mgr:           mgr,
		cfg:           cfg,
	}
}

var _ subroutines.Initializer = &workspaceAuthSubroutine{}

func (r *workspaceAuthSubroutine) GetName() string { return "workspaceAuthConfiguration" }

// Initialize implements subroutines.Initializer.
func (r *workspaceAuthSubroutine) Initialize(ctx context.Context, obj client.Object) (subroutines.Result, error) {
	lc := obj.(*kcpcorev1alpha1.LogicalCluster)

	workspaceName := getWorkspaceName(lc)
	if workspaceName == "" {
		return subroutines.OK(), fmt.Errorf("failed to get workspace path")
	}

	var domainCASecret corev1.Secret
	if r.cfg.DomainCALookup {
		err := r.runtimeClient.Get(ctx, client.ObjectKey{Name: "domain-certificate-ca", Namespace: "platform-mesh-system"}, &domainCASecret)
		if err != nil {
			return subroutines.OK(), fmt.Errorf("failed to get domain CA secret: %w", err)
		}
	}

	cluster, err := r.mgr.ClusterFromContext(ctx)
	if err != nil {
		return subroutines.OK(), fmt.Errorf("failed to get cluster from context %w", err)
	}

	var idpConfig v1alpha1.IdentityProviderConfiguration
	err = cluster.GetClient().Get(ctx, types.NamespacedName{Name: workspaceName}, &idpConfig)
	if err != nil {
		return subroutines.OK(), fmt.Errorf("failed to get IdentityProviderConfiguration: %w", err)
	}

	if len(idpConfig.Spec.Clients) == 0 || len(idpConfig.Status.ManagedClients) == 0 {
		return subroutines.OK(), fmt.Errorf("IdentityProviderConfiguration %s has no clients in spec or status", workspaceName)
	}

	audiences := make([]string, 0, len(idpConfig.Spec.Clients))
	for _, specClient := range idpConfig.Spec.Clients {
		managedClient, ok := idpConfig.Status.ManagedClients[specClient.ClientName]
		if !ok {
			return subroutines.OK(), fmt.Errorf("managed client %s not found in IdentityProviderConfiguration status", specClient.ClientName)
		}
		if managedClient.ClientID == "" {
			return subroutines.OK(), fmt.Errorf("managed client %s has empty ClientID in IdentityProviderConfiguration status", specClient.ClientName)
		}
		audiences = append(audiences, managedClient.ClientID)
	}

	jwtAuthenticationConfiguration := kcptenancyv1alphav1.JWTAuthenticator{
		Issuer: kcptenancyv1alphav1.Issuer{
			URL:                 fmt.Sprintf("https://%s/keycloak/realms/%s", r.cfg.BaseDomain, workspaceName),
			AudienceMatchPolicy: kcptenancyv1alphav1.AudienceMatchPolicyMatchAny,
			Audiences:           audiences,
		},
		ClaimMappings: kcptenancyv1alphav1.ClaimMappings{
			Groups: kcptenancyv1alphav1.PrefixedClaimOrExpression{
				Claim:  r.cfg.GroupClaim,
				Prefix: ptr.To(""),
			},
			Username: kcptenancyv1alphav1.PrefixedClaimOrExpression{}, // to be set based on environment
		},
	}

	// If production - default behavior - only verified emails.
	if !r.cfg.DevelopmentAllowUnverifiedEmails {
		jwtAuthenticationConfiguration.ClaimMappings.Username = kcptenancyv1alphav1.PrefixedClaimOrExpression{
			Claim:  r.cfg.UserClaim,
			Prefix: ptr.To(""),
		}
	} else {
		// Development mode - allow both verified and unverified emails.
		jwtAuthenticationConfiguration.ClaimMappings.Username = kcptenancyv1alphav1.PrefixedClaimOrExpression{
			Expression: "claims.email",
		}
		jwtAuthenticationConfiguration.ClaimValidationRules = []kcptenancyv1alphav1.ClaimValidationRule{
			{
				Expression: "claims.?email_verified.orValue(true) == true || claims.?email_verified.orValue(true) == false",
				Message:    "Allowing both verified and unverified emails",
			}}

	}

	authConfig := &kcptenancyv1alphav1.WorkspaceAuthenticationConfiguration{ObjectMeta: metav1.ObjectMeta{Name: workspaceName}}
	_, err = controllerutil.CreateOrUpdate(ctx, r.orgClient, authConfig, func() error {
		authConfig.Spec = kcptenancyv1alphav1.WorkspaceAuthenticationConfigurationSpec{
			JWT: []kcptenancyv1alphav1.JWTAuthenticator{
				jwtAuthenticationConfiguration,
			},
		}

		if r.cfg.DomainCALookup {
			authConfig.Spec.JWT[0].Issuer.CertificateAuthority = string(domainCASecret.Data["tls.crt"])
		}

		return nil
	})
	if err != nil {
		return subroutines.OK(), fmt.Errorf("failed to create WorkspaceAuthConfiguration resource: %w", err)
	}

	err = r.patchWorkspaceTypes(ctx, r.orgClient, workspaceName)
	if err != nil {
		return subroutines.OK(), fmt.Errorf("failed to patch workspace types: %w", err)
	}

	return subroutines.OK(), nil
}

func (r *workspaceAuthSubroutine) patchWorkspaceTypes(ctx context.Context, cl client.Client, workspaceName string) error {
	wsTypeList := &kcptenancyv1alphav1.WorkspaceTypeList{}
	if err := cl.List(ctx, wsTypeList, client.MatchingLabels{"core.platform-mesh.io/org": workspaceName}); err != nil {
		return fmt.Errorf("failed to list WorkspaceTypes: %w", err)
	}

	desiredAuthConfig := []kcptenancyv1alphav1.AuthenticationConfigurationReference{
		{Name: workspaceName},
	}

	for _, wsType := range wsTypeList.Items {
		if equality.Semantic.DeepEqual(wsType.Spec.AuthenticationConfigurations, desiredAuthConfig) {
			log.Debug().Msg(fmt.Sprintf("workspaceType %s already has authentication configuration, skip patching", wsType.Name))
			continue
		}

		original := wsType.DeepCopy()
		wsType.Spec.AuthenticationConfigurations = desiredAuthConfig

		if err := cl.Patch(ctx, &wsType, client.MergeFrom(original)); err != nil {
			return fmt.Errorf("failed to patch WorkspaceType %s: %w", wsType.Name, err)
		}
		log.Debug().Msg(fmt.Sprintf("patched workspaceType %s with authentication configuration", wsType.Name))
	}

	return nil
}

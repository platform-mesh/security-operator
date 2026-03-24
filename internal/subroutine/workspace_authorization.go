package subroutine

import (
	"context"
	"fmt"

	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	lifecyclesubroutine "github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/errors"
	accountsv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/rs/zerolog/log"
	ctrl "sigs.k8s.io/controller-runtime"
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

var (
	_ lifecyclesubroutine.Subroutine  = &workspaceAuthSubroutine{}
	_ lifecyclesubroutine.Initializer = &workspaceAuthSubroutine{}
)

func (r *workspaceAuthSubroutine) GetName() string { return "workspaceAuthConfiguration" }

func (r *workspaceAuthSubroutine) Finalizers(_ runtimeobject.RuntimeObject) []string {
	return []string{}
}

func (r *workspaceAuthSubroutine) Finalize(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil
}

// Process implements lifecycle.Subroutine as no-op since Initialize handles the
// work.
func (r *workspaceAuthSubroutine) Process(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil
}

// Initialize implements lifecycle.Initializer.
func (r *workspaceAuthSubroutine) Initialize(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	lc := instance.(*kcpcorev1alpha1.LogicalCluster)

	workspaceName := getWorkspaceName(lc)
	if workspaceName == "" {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to get workspace path"), true, false)
	}

	var domainCASecret corev1.Secret
	if r.cfg.DomainCALookup {
		err := r.runtimeClient.Get(ctx, client.ObjectKey{Name: "domain-certificate-ca", Namespace: "platform-mesh-system"}, &domainCASecret)
		if err != nil {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to get domain CA secret: %w", err), true, false)
		}
	}

	cluster, err := r.mgr.ClusterFromContext(ctx)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to get cluster from context %w", err), true, true)
	}

	var accountInfo accountsv1alpha1.AccountInfo
	err = cluster.GetClient().Get(ctx, types.NamespacedName{Name: "account"}, &accountInfo)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to get AccountInfo: %w", err), true, true)
	}

	if accountInfo.Spec.OIDC == nil || len(accountInfo.Spec.OIDC.Clients) == 0 {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("AccountInfo %s has no OIDC clients", workspaceName), true, false)
	}

	audiences := make([]string, 0, len(accountInfo.Spec.OIDC.Clients))
	for clientName, clientInfo := range accountInfo.Spec.OIDC.Clients {
		if clientInfo.ClientID == "" {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("OIDC client %s has empty ClientID in AccountInfo", clientName), true, false)
		}
		audiences = append(audiences, clientInfo.ClientID)
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

	obj := &kcptenancyv1alphav1.WorkspaceAuthenticationConfiguration{ObjectMeta: metav1.ObjectMeta{Name: workspaceName}}
	_, err = controllerutil.CreateOrUpdate(ctx, r.orgClient, obj, func() error {
		obj.Spec = kcptenancyv1alphav1.WorkspaceAuthenticationConfigurationSpec{
			JWT: []kcptenancyv1alphav1.JWTAuthenticator{
				jwtAuthenticationConfiguration,
			},
		}

		if r.cfg.DomainCALookup {
			obj.Spec.JWT[0].Issuer.CertificateAuthority = string(domainCASecret.Data["tls.crt"])
		}

		return nil
	})
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to create WorkspaceAuthConfiguration resource: %w", err), true, true)
	}

	err = r.patchWorkspaceTypes(ctx, r.orgClient, workspaceName)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to patch workspace types: %w", err), true, true)
	}

	return ctrl.Result{}, nil
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

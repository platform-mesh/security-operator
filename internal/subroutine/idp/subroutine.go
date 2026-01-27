package idp

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/coreos/go-oidc"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/security-operator/pkg/clientreg"
	"github.com/platform-mesh/security-operator/pkg/clientreg/keycloak"
	"golang.org/x/oauth2/clientcredentials"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type subroutine struct {
	keycloakBaseURL string
	adminClient     *http.Client
	orgsClient      client.Client
	mgr             mcmanager.Manager
	cfg             *config.Config
}

func New(ctx context.Context, cfg *config.Config, orgsClient client.Client, mgr mcmanager.Manager) (*subroutine, error) {
	issuer := fmt.Sprintf("%s/realms/master", cfg.Invite.KeycloakBaseURL)
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}

	cCfg := clientcredentials.Config{
		ClientID:     cfg.Invite.KeycloakClientID,
		ClientSecret: cfg.Invite.KeycloakClientSecret,
		TokenURL:     provider.Endpoint().TokenURL,
	}

	adminClient := cCfg.Client(ctx)

	return &subroutine{
		keycloakBaseURL: cfg.Invite.KeycloakBaseURL,
		adminClient:     adminClient,
		orgsClient:      orgsClient,
		mgr:             mgr,
		cfg:             cfg,
	}, nil
}

func (s *subroutine) newOIDCClient(realmName string) (clientreg.Client, *keycloak.AdminClient) {
	adminClient := keycloak.NewAdminClient(s.adminClient, s.keycloakBaseURL, realmName)

	httpClient := &http.Client{
		Timeout:   time.Duration(s.cfg.HttpClientTimeoutSeconds) * time.Second,
		Transport: clientreg.NewRetryTransport(nil, adminClient),
	}

	oidcClient := clientreg.NewClient(
		clientreg.WithHTTPClient(httpClient),
		clientreg.WithTokenProvider(adminClient),
	)

	return oidcClient, adminClient
}

func (s *subroutine) Finalize(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	idpToDelete := instance.(*v1alpha1.IdentityProviderConfiguration)
	log := logger.LoadLoggerFromContext(ctx)
	realmName := idpToDelete.Name

	oidcClient, adminClient := s.newOIDCClient(realmName)

	for _, clientToDelete := range idpToDelete.Spec.Clients {
		registrationAccessToken, err := s.readRegistrationAccessToken(ctx, clientToDelete.SecretRef)
		if err != nil {
			if kerrors.IsNotFound(err) {
				log.Info().Str("secretName", clientToDelete.SecretRef.Name).Msg("Secret not found, client was likely deleted")
				continue
			}
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to get registration access token from secret: %w", err), true, true)
		}

		clientIDToDelete := idpToDelete.Status.ManagedClients[clientToDelete.ClientName].ClientID
		if clientIDToDelete == "" {
			log.Info().Str("clientName", clientToDelete.ClientName).Msg("Client ID not found in status, skipping client deletion")
			continue
		}

		registrationClientURIToDelete := idpToDelete.Status.ManagedClients[clientToDelete.ClientName].RegistrationClientURI
		if registrationClientURIToDelete == "" {
			log.Info().Str("clientName", clientToDelete.ClientName).Msg("Registration client URI not found in status, skipping client deletion")
			continue
		}

		err = oidcClient.Delete(ctx, clientIDToDelete, registrationClientURIToDelete, registrationAccessToken)
		if err != nil && !clientreg.IsNotFound(err) {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to delete oidc client: %w", err), true, false)
		}

		secretToDelete := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clientToDelete.SecretRef.Name,
				Namespace: clientToDelete.SecretRef.Namespace,
			},
		}
		if err := s.orgsClient.Delete(ctx, secretToDelete); err != nil {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to delete client secret: %w", err), true, false)
		}
	}

	if err := adminClient.DeleteRealm(ctx, realmName); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to delete realm: %w", err), true, false)
	}

	return ctrl.Result{}, nil
}

func (s *subroutine) Finalizers(_ runtimeobject.RuntimeObject) []string {
	return []string{"core.platform-mesh.io/idp-finalizer"}
}

func (s *subroutine) GetName() string { return "IdentityProviderConfiguration" }

func (s *subroutine) Process(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	idpConfig := instance.(*v1alpha1.IdentityProviderConfiguration)
	log := logger.LoadLoggerFromContext(ctx)

	cl, err := s.mgr.ClusterFromContext(ctx)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to get cluster from context: %w", err), true, true)
	}
	kcpClient := cl.GetClient()

	realmName := idpConfig.Name
	oidcClient, adminClient := s.newOIDCClient(realmName)

	if err := s.ensureRealm(ctx, adminClient, realmName, log); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	if err := s.deleteRemovedClients(ctx, idpConfig, oidcClient, log); err != nil {
		return ctrl.Result{}, err
	}

	managedClients := make(map[string]v1alpha1.ManagedClient)
	for i := range idpConfig.Spec.Clients {
		clientConfig := &idpConfig.Spec.Clients[i]

		clientInfo, err := s.registerOrUpdateClient(ctx, idpConfig, clientConfig, realmName, oidcClient, adminClient)
		if err != nil {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to process client %s: %w", clientConfig.ClientName, err), true, true)
		}

		if err := s.createOrUpdateSecret(ctx, clientConfig, clientInfo, idpConfig.Name); err != nil {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to create or update kubernetes secret: %w", err), true, true)
		}

		managedClients[clientConfig.ClientName] = v1alpha1.ManagedClient{
			ClientID:              clientInfo.ClientID,
			RegistrationClientURI: clientInfo.RegistrationClientURI,
			SecretRef:             clientConfig.SecretRef,
		}
	}

	// Update status
	original := idpConfig.DeepCopy()
	idpConfig.Status.ManagedClients = managedClients
	if err := kcpClient.Status().Patch(ctx, idpConfig, client.MergeFrom(original)); err != nil { // coverage-ignore
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to patch IDP status: %w", err), true, true) // coverage-ignore
	}

	return ctrl.Result{}, nil
}

func (s *subroutine) ensureRealm(ctx context.Context, adminClient *keycloak.AdminClient, realmName string, log *logger.Logger) error {
	realmConfig := keycloak.RealmConfig{
		Realm:                       realmName,
		DisplayName:                 realmName,
		Enabled:                     true,
		LoginWithEmailAllowed:       true,
		RegistrationEmailAsUsername: true,
		RegistrationAllowed:         s.cfg.IDP.RegistrationAllowed,
		SSOSessionIdleTimeout:       s.cfg.IDP.AccessTokenLifespan,
		AccessTokenLifespan:         s.cfg.IDP.AccessTokenLifespan,
	}

	if s.cfg.IDP.SMTPServer != "" {
		smtpConfig := &keycloak.SMTPConfig{
			Host:     s.cfg.IDP.SMTPServer,
			Port:     fmt.Sprintf("%d", s.cfg.IDP.SMTPPort),
			From:     s.cfg.IDP.FromAddress,
			SSL:      s.cfg.IDP.SSL,
			StartTLS: s.cfg.IDP.StartTLS,
		}

		if s.cfg.IDP.SMTPUser != "" {
			smtpConfig.Auth = true
			smtpConfig.User = s.cfg.IDP.SMTPUser
			smtpConfig.Password = s.cfg.IDP.SMTPPassword
		}

		realmConfig.SMTPServer = smtpConfig
	}

	created, err := adminClient.CreateOrUpdateRealm(ctx, realmConfig)
	if err != nil {
		return fmt.Errorf("failed to create or update realm: %w", err)
	}

	if created {
		log.Info().Str("realm", realmName).Msg("Realm created")
	} else {
		log.Info().Str("realm", realmName).Msg("Realm updated")
	}

	return nil
}

func (s *subroutine) deleteRemovedClients(ctx context.Context, idpConfig *v1alpha1.IdentityProviderConfiguration, oidcClient clientreg.Client, log *logger.Logger) errors.OperatorError {
	for clientName, managedClient := range idpConfig.Status.ManagedClients {
		exists := slices.ContainsFunc(idpConfig.Spec.Clients,
			func(c v1alpha1.IdentityProviderClientConfig) bool {
				return c.ClientName == clientName
			},
		)
		if exists {
			continue
		}

		log.Info().Str("clientName", clientName).Msg("Deleting client that is no longer in spec")

		registrationAccessToken, err := s.readRegistrationAccessToken(ctx, managedClient.SecretRef)
		if err != nil {
			if kerrors.IsNotFound(err) {
				log.Info().Str("secretName", managedClient.SecretRef.Name).Msg("Secret not found, client was likely deleted")
				continue
			}
			return errors.NewOperatorError(fmt.Errorf("failed to get registration access token from secret: %w", err), true, true)
		}

		if err := oidcClient.Delete(ctx, managedClient.ClientID, managedClient.RegistrationClientURI, registrationAccessToken); err != nil && !clientreg.IsNotFound(err) {
			return errors.NewOperatorError(fmt.Errorf("failed to delete client %s: %w", clientName, err), true, false)
		}

		secretToDelete := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      managedClient.SecretRef.Name,
				Namespace: managedClient.SecretRef.Namespace,
			},
		}
		if err := s.orgsClient.Delete(ctx, secretToDelete); err != nil {
			return errors.NewOperatorError(fmt.Errorf("failed to delete client secret %s: %w", managedClient.SecretRef.Name, err), true, false)
		}
	}

	return nil
}

func (s *subroutine) registerOrUpdateClient(ctx context.Context, ipc *v1alpha1.IdentityProviderConfiguration, clientConfig *v1alpha1.IdentityProviderClientConfig, realmName string, oidcClient clientreg.Client, adminClient *keycloak.AdminClient) (clientreg.ClientInformation, error) {
	existingClient, err := adminClient.GetClientByName(ctx, clientConfig.ClientName)
	if err != nil {
		return clientreg.ClientInformation{}, fmt.Errorf("failed to check if client exists: %w", err)
	}

	authMethod := clientreg.TokenEndpointAuthMethodClientSecretBasic
	if clientConfig.ClientType == v1alpha1.IdentityProviderClientTypePublic {
		authMethod = clientreg.TokenEndpointAuthMethodNone
	}

	metadata := clientreg.ClientMetadata{
		ClientName:              clientConfig.ClientName,
		RedirectURIs:            clientConfig.RedirectURIs,
		GrantTypes:              []string{clientreg.GrantTypeAuthorizationCode, clientreg.GrantTypeRefreshToken},
		TokenEndpointAuthMethod: authMethod,
		PostLogoutRedirectURIs:  clientConfig.PostLogoutRedirectURIs,
	}

	if existingClient == nil {
		return oidcClient.Register(ctx, adminClient.RegistrationEndpoint(), metadata)
	}

	// Client exists, update it
	registrationAccessToken, err := s.readRegistrationAccessToken(ctx, clientConfig.SecretRef)
	if err != nil && !kerrors.IsNotFound(err) {
		return clientreg.ClientInformation{}, fmt.Errorf("failed to get registration access token from secret: %w", err)
	}

	registrationClientURI := ipc.Status.ManagedClients[clientConfig.ClientName].RegistrationClientURI
	if registrationClientURI == "" {
		registrationClientURI = fmt.Sprintf("%s/realms/%s/clients-registrations/openid-connect/%s", s.keycloakBaseURL, realmName, existingClient.ClientID)
	}

	metadata.ClientID = ipc.Status.ManagedClients[clientConfig.ClientName].ClientID
	if metadata.ClientID == "" {
		metadata.ClientID = existingClient.ClientID
	}

	return oidcClient.Update(ctx, registrationClientURI, registrationAccessToken, metadata)
}

func (s *subroutine) readRegistrationAccessToken(ctx context.Context, secretRef corev1.SecretReference) (string, error) {
	secret := &corev1.Secret{}
	key := client.ObjectKey{Name: secretRef.Name, Namespace: secretRef.Namespace}
	if err := s.orgsClient.Get(ctx, key, secret); err != nil {
		return "", err
	}
	return string(secret.Data["registration_access_token"]), nil
}

func (s *subroutine) createOrUpdateSecret(ctx context.Context, clientConfig *v1alpha1.IdentityProviderClientConfig, clientInfo clientreg.ClientInformation, idpName string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clientConfig.SecretRef.Name,
			Namespace: clientConfig.SecretRef.Namespace,
			Labels: map[string]string{
				"core.platform-mesh.io/idp-name":    idpName,
				"core.platform-mesh.io/client-name": clientConfig.ClientName,
			},
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, s.orgsClient, secret, func() error {
		if secret.Data == nil {
			secret.Data = make(map[string][]byte)
		}
		if clientInfo.ClientSecret != "" {
			secret.Data["client_secret"] = []byte(clientInfo.ClientSecret)
		}
		if clientInfo.RegistrationAccessToken != "" {
			secret.Data["registration_access_token"] = []byte(clientInfo.RegistrationAccessToken)
		}
		secret.Type = corev1.SecretTypeOpaque
		return nil
	})
	return err
}

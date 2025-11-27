package idp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/coreos/go-oidc"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/config"
	"golang.org/x/oauth2/clientcredentials"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	kcpSecretNamespace  = "default"
	realmClientProtocol = "openid-connect"
)

type subroutine struct {
	keycloakBaseURL string
	keycloak        *http.Client
	orgsClient      client.Client
	cfg             *config.Config
}

func New(ctx context.Context, cfg *config.Config, orgsClient client.Client) (*subroutine, error) {

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

	httpClient := cCfg.Client(ctx)

	return &subroutine{
		keycloakBaseURL: cfg.Invite.KeycloakBaseURL,
		keycloak:        httpClient,
		orgsClient:      orgsClient,
		cfg:             cfg,
	}, nil
}

// Finalize implements subroutine.Subroutine.
func (s *subroutine) Finalize(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil
}

// Finalizers implements subroutine.Subroutine.
func (s *subroutine) Finalizers(_ runtimeobject.RuntimeObject) []string { return []string{} }

// GetName implements subroutine.Subroutine.
func (s *subroutine) GetName() string { return "IdentityProviderConfiguration" }

// Process implements subroutine.Subroutine.
func (s *subroutine) Process(ctx context.Context, instance runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	IdentityProviderConfiguration := instance.(*v1alpha1.IdentityProviderConfiguration)
	log := logger.LoadLoggerFromContext(ctx)

	realmName := IdentityProviderConfiguration.Name
	realm := realm{
		Realm:                       realmName,
		DisplayName:                 realmName,
		Enabled:                     true,
		LoginWithEmailAllowed:       true,
		RegistrationEmailAsUsername: true,
	}

	if s.cfg.IDP.SMTPServer != "" {
		smtpConfig := &smtpServer{
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

		realm.SMTPServer = smtpConfig
	}

	err := s.createRealm(ctx, realm, log)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to create realm %w", err), true, false)
	}

	client := realmClient{
		ClientID:               IdentityProviderConfiguration.Spec.ClientID,
		ClientName:             IdentityProviderConfiguration.Spec.ClientID,
		Enabled:                true,
		PublicClient:           IdentityProviderConfiguration.Spec.ClientType == v1alpha1.IdentityProviderClientTypePublic,
		StandardFlowEnabled:    true,
		ServiceAccountsEnabled: IdentityProviderConfiguration.Spec.ClientType == v1alpha1.IdentityProviderClientTypeConfidential,
		RedirectUris:           IdentityProviderConfiguration.Spec.ValidRedirectURIs,
		Protocol:               realmClientProtocol,
	}

	err = s.createRealmClient(ctx, client, realmName, log)
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to create realm client: %w", err), true, true)
	}

	clientUUID, err := s.getClientUUID(ctx, realmName, IdentityProviderConfiguration.Spec.ClientID)
	if err != nil {
		log.Err(err).Str("realm", realmName).Str("clientId", IdentityProviderConfiguration.Spec.ClientID).Msg("Failed to get client UUID")
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to get client UUID: %w", err), true, true)
	}

	clientSecret, err := s.getClientSecret(ctx, realmName, clientUUID)
	if err != nil {
		log.Err(err).Str("realm", realmName).Str("clientId", IdentityProviderConfiguration.Spec.ClientID).Msg("Failed to get client secret")
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to get client secret: %w", err), true, true)
	}

	secretName := fmt.Sprintf("portal-client-secret-%s", IdentityProviderConfiguration.Name)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: kcpSecretNamespace,
		},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, s.orgsClient, secret, func() error {
		secret.Data = make(map[string][]byte)
		secret.Data["secret"] = []byte(clientSecret)
		secret.Type = corev1.SecretTypeOpaque
		return nil
	})
	if err != nil {
		log.Err(err).Str("realm", realmName).Str("clientId", IdentityProviderConfiguration.Spec.ClientID).Msg("Failed to create or update kubernetes secret")
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("failed to create or update kubernetes secret: %w", err), true, true)
	}
	log.Info().Str("realm", realmName).Str("clientId", IdentityProviderConfiguration.Spec.ClientID).Msg("Client secret stored in kubernetes secret")

	return ctrl.Result{}, nil
}

func (s *subroutine) createRealm(ctx context.Context, realm realm, log *logger.Logger) error {
	realmJSON, err := json.Marshal(realm)
	if err != nil {
		log.Err(err).Msg("Failed to marshal realm data")
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/admin/realms", s.keycloakBaseURL), bytes.NewBuffer(realmJSON))
	if err != nil {// coverage-ignore
		log.Err(err).Msg("Failed to create realm creation request")
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := s.keycloak.Do(req)
	if err != nil {// coverage-ignore
		log.Err(err).Str("realm", realm.Realm).Msg("Failed to create realm")
		return err
	}
	defer res.Body.Close() //nolint:errcheck

	if res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusConflict {
		log.Error().Int("status", res.StatusCode).Str("realm", realm.Realm).Msg("Failed to create realm")
		return fmt.Errorf("failed to create realm, received status %d", res.StatusCode)
	}
	log.Info().Str("realm", realm.Realm).Msg("realm is configured")
	return nil
}

func (s *subroutine) createRealmClient(ctx context.Context, client realmClient, realmName string, log *logger.Logger) error {
	clientJSON, err := json.Marshal(client)
	if err != nil {
		log.Err(err).Msg("Failed to marshal client data")
		return err
	}

	clientReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/admin/realms/%s/clients", s.keycloakBaseURL, realmName), bytes.NewBuffer(clientJSON))
	if err != nil {// coverage-ignore
		log.Err(err).Msg("Failed to create client creation request")
		return err
	}
	clientReq.Header.Set("Content-Type", "application/json")

	clientRes, err := s.keycloak.Do(clientReq)
	if err != nil {// coverage-ignore
		log.Err(err).Str("realm", realmName).Str("clientId", client.ClientID).Msg("Failed to create client")
		return err
	}
	defer clientRes.Body.Close() //nolint:errcheck

	if clientRes.StatusCode != http.StatusCreated && clientRes.StatusCode != http.StatusConflict {
		log.Error().Int("status", clientRes.StatusCode).Str("realm", realmName).Str("clientId", client.ClientID).Msg("Failed to create client")
		return fmt.Errorf("failed to create client, received status %d", clientRes.StatusCode)
	}
	log.Info().Str("realm", realmName).Str("clientId", client.ClientID).Msg("Client is configured")
	return nil
}

func (s *subroutine) getClientUUID(ctx context.Context, realmName, clientID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/admin/realms/%s/clients?clientId=%s", s.keycloakBaseURL, realmName, clientID), nil)
	if err != nil {// coverage-ignore
		return "", fmt.Errorf("failed to create client search request: %w", err)
	}

	res, err := s.keycloak.Do(req)
	if err != nil {// coverage-ignore
		return "", fmt.Errorf("failed to search for client: %w", err)
	}
	defer res.Body.Close() //nolint:errcheck

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to search for client: received status %d", res.StatusCode)
	}

	var clients []keycloakClient
	if err := json.NewDecoder(res.Body).Decode(&clients); err != nil {
		return "", fmt.Errorf("failed to decode client search response: %w", err)
	}

	if len(clients) == 0 {
		return "", fmt.Errorf("client with clientId %s not found", clientID)
	}

	return clients[0].ID, nil
}

func (s *subroutine) getClientSecret(ctx context.Context, realmName, clientUUID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/admin/realms/%s/clients/%s/client-secret", s.keycloakBaseURL, realmName, clientUUID), nil)
	if err != nil {// coverage-ignore
		return "", fmt.Errorf("failed to create client secret request: %w", err)
	}

	res, err := s.keycloak.Do(req)
	if err != nil {// coverage-ignore
		return "", fmt.Errorf("failed to get client secret: %w", err)
	}
	defer res.Body.Close() //nolint:errcheck

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to get client secret: received status %d", res.StatusCode)
	}

	var secretResp struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(res.Body).Decode(&secretResp); err != nil {
		return "", fmt.Errorf("failed to decode client secret response: %w", err)
	}

	return secretResp.Value, nil
}

type realmClient struct {
	ClientID               string   `json:"clientId"`
	ClientName             string   `json:"name"`
	Enabled                bool     `json:"enabled"`
	PublicClient           bool     `json:"publicClient"`
	StandardFlowEnabled    bool     `json:"standardFlowEnabled"`
	ServiceAccountsEnabled bool     `json:"serviceAccountsEnabled"`
	RedirectUris           []string `json:"redirectUris"`
	Protocol               string   `json:"protocol"`
}

type keycloakClient struct {
	ID       string `json:"id"`
	ClientID string `json:"clientId"`
}

type smtpServer struct {
	Host     string `json:"host,omitempty"`
	Port     string `json:"port,omitempty"`
	From     string `json:"from,omitempty"`
	SSL      bool   `json:"ssl,omitempty"`
	StartTLS bool   `json:"starttls,omitempty"`
	Auth     bool   `json:"auth,omitempty"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
}

type realm struct {
	Realm                       string      `json:"realm"`
	DisplayName                 string      `json:"displayName,omitempty"`
	Enabled                     bool        `json:"enabled"`
	LoginWithEmailAllowed       bool        `json:"loginWithEmailAllowed,omitempty"`
	RegistrationEmailAsUsername bool        `json:"registrationEmailAsUsername,omitempty"`
	SMTPServer                  *smtpServer `json:"smtpServer,omitempty"`
}

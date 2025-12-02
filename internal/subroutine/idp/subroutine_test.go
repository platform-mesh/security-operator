package idp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	"github.com/platform-mesh/golang-commons/logger/testlogger"
	"github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/security-operator/internal/subroutine/idp"
	"github.com/platform-mesh/security-operator/internal/subroutine/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"golang.org/x/oauth2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
)

func configureOIDCProvider(t *testing.T, mux *http.ServeMux, baseURL string) {
	mux.HandleFunc("/realms/master/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		err := json.NewEncoder(w).Encode(&map[string]string{
			"issuer":                 fmt.Sprintf("%s/realms/master", baseURL),
			"authorization_endpoint": fmt.Sprintf("%s/realms/master/protocol/openid-connect/auth", baseURL),
			"token_endpoint":         fmt.Sprintf("%s/realms/master/protocol/openid-connect/token", baseURL),
		})
		assert.NoError(t, err)
	})

	mux.HandleFunc("/realms/master/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		err := json.NewEncoder(w).Encode(&map[string]string{
			"access_token": "token",
		})
		assert.NoError(t, err)
	})
}

func setupManagerAndCluster(t *testing.T, kcpClient *mocks.MockClient) (*mocks.MockManager, *mocks.MockCluster) {
	mgr := mocks.NewMockManager(t)
	cluster := mocks.NewMockCluster(t)
	
	mgr.EXPECT().ClusterFromContext(mock.Anything).Return(cluster, nil).Maybe()
	cluster.EXPECT().GetClient().Return(kcpClient).Maybe()
	
	return mgr, cluster
}

func setRegistrationClientURI(obj runtimeobject.RuntimeObject, baseURL string) {
	if idpObj, ok := obj.(*v1alpha1.IdentityProviderConfiguration); ok {
		realmName := idpObj.Name
		for i := range idpObj.Spec.Clients {
			if idpObj.Spec.Clients[i].ClientID != "" {
				idpObj.Spec.Clients[i].RegistrationClientURI = fmt.Sprintf("%s/realms/%s/clients-registrations/openid-connect/%s", baseURL, realmName, idpObj.Spec.Clients[i].ClientID)
			}
		}
	}
}

func getTestConfig(cfg *config.Config, baseURL string) *config.Config {
	if cfg == nil {
		return &config.Config{
			Invite: config.InviteConfig{
				KeycloakBaseURL:  baseURL,
				KeycloakClientID: "security-operator",
			},
		}
	}
	cfg.Invite.KeycloakBaseURL = baseURL
	return cfg
}

func TestSubroutineProcess(t *testing.T) {
	testCases := []struct {
		desc               string
		obj                runtimeobject.RuntimeObject
		cfg                *config.Config
		setupK8sMocks      func(m *mocks.MockClient, kcpClient *mocks.MockClient)
		setupKeycloakMocks func(mux *http.ServeMux, baseURL string)
		expectNewErr       bool
		expectErr          bool
	}{
		{
			desc: "realm and client created successfully without SMTP",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-realm",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					Clients: []v1alpha1.IdentityProviderClientConfig{
						{
							ClientName:        "test-realm",
							ClientType:        v1alpha1.IdentityProviderClientTypeConfidential,
							ValidRedirectURIs: []string{"https://test.example.com/*"},
							ClientSecretRef: v1alpha1.ClientSecretRef{
								SecretReference: corev1.SecretReference{
									Name:      "portal-client-secret-test-realm",
									Namespace: "default",
								},
							},
						},
					},
				},
			},
			cfg: &config.Config{
				Invite: config.InviteConfig{
					KeycloakBaseURL:  "http://localhost",
					KeycloakClientID: "security-operator",
				},
			},
			setupK8sMocks: func(m *mocks.MockClient, kcpClient *mocks.MockClient) {
				m.EXPECT().Create(mock.Anything, mock.Anything).Return(nil).Maybe()
				m.EXPECT().Update(mock.Anything, mock.Anything).Return(nil).Maybe()
				m.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "portal-client-secret-test-realm")).Maybe()
				kcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
				kcpClient.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
			},
			setupKeycloakMocks: func(mux *http.ServeMux, baseURL string) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("GET /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					clients := []map[string]any{}
					_ = json.NewEncoder(w).Encode(clients)
				})
				mux.HandleFunc("POST /admin/realms/test-realm/clients-initial-access", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(map[string]string{"token": "initial-access-token"})
				})
				mux.HandleFunc("POST /realms/test-realm/clients-registrations/openid-connect", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"client_id":                "generated-client-id-123",
						"client_secret":            "client-secret-123",
						"registration_access_token": "registration-token-123",
						"registration_client_uri":   fmt.Sprintf("%s/realms/test-realm/clients-registrations/openid-connect/generated-client-id-123", baseURL),
					})
				})
			},
		},
		{
			desc: "realm already exists",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "existing-realm",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					Clients: []v1alpha1.IdentityProviderClientConfig{
						{
							ClientName:        "existing-realm",
							ClientType:        v1alpha1.IdentityProviderClientTypeConfidential,
							ValidRedirectURIs: []string{"https://test.example.com/*"},
							ClientSecretRef: v1alpha1.ClientSecretRef{
								SecretReference: corev1.SecretReference{
									Name:      "portal-client-secret-existing-realm",
									Namespace: "default",
								},
							},
						},
					},
				},
			},
			cfg: &config.Config{
				Invite: config.InviteConfig{
					KeycloakBaseURL:  "http://localhost",
					KeycloakClientID: "security-operator",
				},
			},
			setupK8sMocks: func(m *mocks.MockClient, kcpClient *mocks.MockClient) {
				m.EXPECT().Create(mock.Anything, mock.Anything).Return(nil).Maybe()
				m.EXPECT().Update(mock.Anything, mock.Anything).Return(nil).Maybe()
				m.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "portal-client-secret-existing-realm")).Maybe()
				kcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
				kcpClient.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
			},
			setupKeycloakMocks: func(mux *http.ServeMux, baseURL string) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusConflict)
				})
				mux.HandleFunc("PUT /admin/realms/existing-realm", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				})
				mux.HandleFunc("GET /admin/realms/existing-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					clients := []map[string]any{}
					_ = json.NewEncoder(w).Encode(clients)
				})
				mux.HandleFunc("POST /admin/realms/existing-realm/clients-initial-access", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(map[string]string{"token": "initial-access-token"})
				})
				mux.HandleFunc("POST /realms/existing-realm/clients-registrations/openid-connect", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"client_id":                "generated-client-id-existing",
						"client_secret":            "client-secret-existing",
						"registration_access_token": "registration-token-existing",
						"registration_client_uri":   fmt.Sprintf("%s/realms/existing-realm/clients-registrations/openid-connect/generated-client-id-existing", baseURL),
					})
				})
			},
		},
		{
			desc: "client already exists - update path",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-realm",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					Clients: []v1alpha1.IdentityProviderClientConfig{
						{
							ClientName:        "test-realm",
							ClientType:        v1alpha1.IdentityProviderClientTypeConfidential,
							ValidRedirectURIs: []string{"https://test.example.com/*"},
							ClientSecretRef: v1alpha1.ClientSecretRef{
								SecretReference: corev1.SecretReference{
									Name:      "portal-client-secret-test-realm",
									Namespace: "default",
								},
							},
						},
					},
				},
			},
			cfg: &config.Config{
				Invite: config.InviteConfig{
					KeycloakBaseURL:  "http://localhost",
					KeycloakClientID: "security-operator",
				},
			},
			setupK8sMocks: func(m *mocks.MockClient, kcpClient *mocks.MockClient) {
				m.EXPECT().Create(mock.Anything, mock.Anything).Return(nil).Maybe()
				m.EXPECT().Update(mock.Anything, mock.Anything).Return(nil).Maybe()
				m.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "portal-client-secret-test-realm")).Maybe()
				kcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
				kcpClient.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
			},
			setupKeycloakMocks: func(mux *http.ServeMux, baseURL string) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("GET /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					clients := []map[string]any{
						{"clientId": "existing-client-id", "name": "test-realm"},
					}
					_ = json.NewEncoder(w).Encode(clients)
				})
				mux.HandleFunc("POST /admin/realms/test-realm/clients/existing-client-id/registration-access-token", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]string{"registrationAccessToken": "new-registration-token"})
				})
				mux.HandleFunc("GET /realms/test-realm/clients-registrations/openid-connect/existing-client-id", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"client_id":                "existing-client-id",
						"client_secret":            "existing-secret",
						"registration_access_token": "rotated-token",
						"registration_client_uri":   fmt.Sprintf("%s/realms/test-realm/clients-registrations/openid-connect/existing-client-id", baseURL),
					})
				})
				mux.HandleFunc("PUT /realms/test-realm/clients-registrations/openid-connect/existing-client-id", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"client_id":                "existing-client-id",
						"client_secret":            "existing-secret",
						"registration_access_token": "updated-token",
						"registration_client_uri":   fmt.Sprintf("%s/realms/test-realm/clients-registrations/openid-connect/existing-client-id", baseURL),
					})
				})
			},
		},
		{
			desc: "error creating realm",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "error-realm",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					Clients: []v1alpha1.IdentityProviderClientConfig{
						{
							ClientName:        "error-realm",
							ClientType:        v1alpha1.IdentityProviderClientTypeConfidential,
							ValidRedirectURIs: []string{"https://test.example.com/*"},
							ClientSecretRef: v1alpha1.ClientSecretRef{
								SecretReference: corev1.SecretReference{
									Name:      "portal-client-secret-error-realm",
									Namespace: "default",
								},
							},
						},
					},
				},
			},
			cfg: &config.Config{
				Invite: config.InviteConfig{
					KeycloakBaseURL:  "http://localhost",
					KeycloakClientID: "security-operator",
				},
			},
			expectErr: true,
			setupK8sMocks: func(m *mocks.MockClient, kcpClient *mocks.MockClient) {
			},
			setupKeycloakMocks: func(mux *http.ServeMux, baseURL string) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":"internal server error"}`))
				})
			},
		},
		{
			desc: "error getting client ID",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-realm",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					Clients: []v1alpha1.IdentityProviderClientConfig{
						{
							ClientName:        "test-realm",
							ClientType:        v1alpha1.IdentityProviderClientTypeConfidential,
							ValidRedirectURIs: []string{"https://test.example.com/*"},
							ClientSecretRef: v1alpha1.ClientSecretRef{
								SecretReference: corev1.SecretReference{
									Name:      "portal-client-secret-test-realm",
									Namespace: "default",
								},
							},
						},
					},
				},
			},
			cfg: &config.Config{
				Invite: config.InviteConfig{
					KeycloakBaseURL:  "http://localhost",
					KeycloakClientID: "security-operator",
				},
			},
			expectErr: true,
			setupK8sMocks: func(m *mocks.MockClient, kcpClient *mocks.MockClient) {
			},
			setupKeycloakMocks: func(mux *http.ServeMux, baseURL string) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("GET /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				})
			},
		},
		{
			desc: "error getting Initial Access Token",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-realm",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					Clients: []v1alpha1.IdentityProviderClientConfig{
						{
							ClientName:        "test-realm",
							ClientType:        v1alpha1.IdentityProviderClientTypeConfidential,
							ValidRedirectURIs: []string{"https://test.example.com/*"},
							ClientSecretRef: v1alpha1.ClientSecretRef{
								SecretReference: corev1.SecretReference{
									Name:      "portal-client-secret-test-realm",
									Namespace: "default",
								},
							},
						},
					},
				},
			},
			cfg: &config.Config{
				Invite: config.InviteConfig{
					KeycloakBaseURL:  "http://localhost",
					KeycloakClientID: "security-operator",
				},
			},
			expectErr: true,
			setupK8sMocks: func(m *mocks.MockClient, kcpClient *mocks.MockClient) {
			},
			setupKeycloakMocks: func(mux *http.ServeMux, baseURL string) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("GET /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					clients := []map[string]any{}
					_ = json.NewEncoder(w).Encode(clients)
				})
				mux.HandleFunc("POST /admin/realms/test-realm/clients-initial-access", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":"internal server error"}`))
				})
			},
		},
		{
			desc: "error registering client",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-realm",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					Clients: []v1alpha1.IdentityProviderClientConfig{
						{
							ClientName:        "test-realm",
							ClientType:        v1alpha1.IdentityProviderClientTypeConfidential,
							ValidRedirectURIs: []string{"https://test.example.com/*"},
							ClientSecretRef: v1alpha1.ClientSecretRef{
								SecretReference: corev1.SecretReference{
									Name:      "portal-client-secret-test-realm",
									Namespace: "default",
								},
							},
						},
					},
				},
			},
			cfg: &config.Config{
				Invite: config.InviteConfig{
					KeycloakBaseURL:  "http://localhost",
					KeycloakClientID: "security-operator",
				},
			},
			expectErr: true,
			setupK8sMocks: func(m *mocks.MockClient, kcpClient *mocks.MockClient) {
			},
			setupKeycloakMocks: func(mux *http.ServeMux, baseURL string) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("GET /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					clients := []map[string]any{}
					_ = json.NewEncoder(w).Encode(clients)
				})
				mux.HandleFunc("POST /admin/realms/test-realm/clients-initial-access", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(map[string]string{"token": "initial-access-token"})
				})
				mux.HandleFunc("POST /realms/test-realm/clients-registrations/openid-connect", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":"invalid client configuration"}`))
				})
			},
		},
		{
			desc: "error updating realm when conflict",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{Name: "conflict-realm"},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					Clients: []v1alpha1.IdentityProviderClientConfig{{
						ClientName:        "conflict-realm",
						ClientType:        v1alpha1.IdentityProviderClientTypeConfidential,
						ValidRedirectURIs: []string{"https://test.example.com/*"},
						ClientSecretRef: v1alpha1.ClientSecretRef{
							SecretReference: corev1.SecretReference{Name: "portal-client-secret-conflict-realm", Namespace: "default"},
						},
					}},
				},
			},
			cfg: &config.Config{Invite: config.InviteConfig{KeycloakBaseURL: "http://localhost", KeycloakClientID: "security-operator"}},
			expectErr: true,
			setupK8sMocks: func(m *mocks.MockClient, kcpClient *mocks.MockClient) {},
			setupKeycloakMocks: func(mux *http.ServeMux, baseURL string) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusConflict) })
				mux.HandleFunc("PUT /admin/realms/conflict-realm", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
			},
		},
		{
			desc: "error regenerating registration access token",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-realm",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					Clients: []v1alpha1.IdentityProviderClientConfig{
						{
							ClientName:        "test-realm",
							ClientType:        v1alpha1.IdentityProviderClientTypeConfidential,
							ValidRedirectURIs: []string{"https://test.example.com/*"},
							ClientSecretRef: v1alpha1.ClientSecretRef{
								SecretReference: corev1.SecretReference{
									Name:      "portal-client-secret-test-realm",
									Namespace: "default",
								},
							},
						},
					},
				},
			},
			cfg: &config.Config{
				Invite: config.InviteConfig{
					KeycloakBaseURL:  "http://localhost",
					KeycloakClientID: "security-operator",
				},
			},
			expectErr: true,
			setupK8sMocks: func(m *mocks.MockClient, kcpClient *mocks.MockClient) {
			},
			setupKeycloakMocks: func(mux *http.ServeMux, baseURL string) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("GET /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					clients := []map[string]any{
						{"clientId": "existing-client-id", "name": "test-realm"},
					}
					_ = json.NewEncoder(w).Encode(clients)
				})
				mux.HandleFunc("POST /admin/realms/test-realm/clients/existing-client-id/registration-access-token", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":"internal server error"}`))
				})
			},
		},
		{
			desc: "error client registration non-201 status",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-realm",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					Clients: []v1alpha1.IdentityProviderClientConfig{
						{
							ClientName:        "test-realm",
							ClientType:        v1alpha1.IdentityProviderClientTypeConfidential,
							ValidRedirectURIs: []string{"https://test.example.com/*"},
							ClientSecretRef: v1alpha1.ClientSecretRef{
								SecretReference: corev1.SecretReference{
									Name:      "portal-client-secret-test-realm",
									Namespace: "default",
								},
							},
						},
					},
				},
			},
			cfg: &config.Config{
				Invite: config.InviteConfig{
					KeycloakBaseURL:  "http://localhost",
					KeycloakClientID: "security-operator",
				},
			},
			expectErr: true,
			setupK8sMocks: func(m *mocks.MockClient, kcpClient *mocks.MockClient) {
			},
			setupKeycloakMocks: func(mux *http.ServeMux, baseURL string) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("GET /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					clients := []map[string]any{}
					_ = json.NewEncoder(w).Encode(clients)
				})
				mux.HandleFunc("POST /admin/realms/test-realm/clients-initial-access", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(map[string]string{"token": "initial-access-token"})
				})
				mux.HandleFunc("POST /realms/test-realm/clients-registrations/openid-connect", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":"bad request"}`))
				})
			},
		},
		{
			desc: "error unmarshaling registration token response",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{Name: "test-realm"},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					Clients: []v1alpha1.IdentityProviderClientConfig{{
						ClientName:        "test-realm",
						ClientType:        v1alpha1.IdentityProviderClientTypeConfidential,
						ValidRedirectURIs: []string{"https://test.example.com/*"},
						ClientSecretRef: v1alpha1.ClientSecretRef{
							SecretReference: corev1.SecretReference{Name: "portal-client-secret-test-realm", Namespace: "default"},
						},
					}},
				},
			},
			cfg: &config.Config{Invite: config.InviteConfig{KeycloakBaseURL: "http://localhost", KeycloakClientID: "security-operator"}},
			expectErr: true,
			setupK8sMocks: func(m *mocks.MockClient, kcpClient *mocks.MockClient) {},
			setupKeycloakMocks: func(mux *http.ServeMux, baseURL string) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusCreated) })
				mux.HandleFunc("GET /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode([]map[string]any{{"clientId": "existing-client-id", "name": "test-realm"}})
				})
				mux.HandleFunc("POST /admin/realms/test-realm/clients/existing-client-id/registration-access-token", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{invalid-json`))
				})
			},
		},
		{
			desc: "error unmarshaling getClientInfo response",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{Name: "test-realm"},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					Clients: []v1alpha1.IdentityProviderClientConfig{{
						ClientName:        "test-realm",
						ClientType:        v1alpha1.IdentityProviderClientTypeConfidential,
						ValidRedirectURIs: []string{"https://test.example.com/*"},
						ClientSecretRef: v1alpha1.ClientSecretRef{
							SecretReference: corev1.SecretReference{Name: "portal-client-secret-test-realm", Namespace: "default"},
						},
					}},
				},
			},
			cfg: &config.Config{Invite: config.InviteConfig{KeycloakBaseURL: "http://localhost", KeycloakClientID: "security-operator"}},
			expectErr: true,
			setupK8sMocks: func(m *mocks.MockClient, kcpClient *mocks.MockClient) {},
			setupKeycloakMocks: func(mux *http.ServeMux, baseURL string) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusCreated) })
				mux.HandleFunc("GET /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode([]map[string]any{{"clientId": "existing-client-id", "name": "test-realm"}})
				})
				mux.HandleFunc("POST /admin/realms/test-realm/clients/existing-client-id/registration-access-token", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]string{"registrationAccessToken": "new-registration-token"})
				})
				mux.HandleFunc("GET /realms/test-realm/clients-registrations/openid-connect/existing-client-id", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{invalid-json`))
				})
			},
		},
		{
			desc: "error unmarshaling updateClient response",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{Name: "test-realm"},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					Clients: []v1alpha1.IdentityProviderClientConfig{{
						ClientName:        "test-realm",
						ClientType:        v1alpha1.IdentityProviderClientTypeConfidential,
						ValidRedirectURIs: []string{"https://test.example.com/*"},
						ClientSecretRef: v1alpha1.ClientSecretRef{
							SecretReference: corev1.SecretReference{Name: "portal-client-secret-test-realm", Namespace: "default"},
						},
					}},
				},
			},
			cfg: &config.Config{Invite: config.InviteConfig{KeycloakBaseURL: "http://localhost", KeycloakClientID: "security-operator"}},
			expectErr: true,
			setupK8sMocks: func(m *mocks.MockClient, kcpClient *mocks.MockClient) {},
			setupKeycloakMocks: func(mux *http.ServeMux, baseURL string) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusCreated) })
				mux.HandleFunc("GET /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode([]map[string]any{{"clientId": "existing-client-id", "name": "test-realm"}})
				})
				mux.HandleFunc("POST /admin/realms/test-realm/clients/existing-client-id/registration-access-token", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]string{"registrationAccessToken": "new-registration-token"})
				})
				mux.HandleFunc("GET /realms/test-realm/clients-registrations/openid-connect/existing-client-id", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"client_id":                "existing-client-id",
						"client_secret":            "existing-secret",
						"registration_access_token": "rotated-token",
						"registration_client_uri":   fmt.Sprintf("%s/realms/test-realm/clients-registrations/openid-connect/existing-client-id", baseURL),
					})
				})
				mux.HandleFunc("PUT /realms/test-realm/clients-registrations/openid-connect/existing-client-id", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{invalid-json`))
				})
			},
		},
		{
			desc: "error getting client info via DCR",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{Name: "test-realm"},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					Clients: []v1alpha1.IdentityProviderClientConfig{{
						ClientName:        "test-realm",
						ClientType:        v1alpha1.IdentityProviderClientTypeConfidential,
						ValidRedirectURIs: []string{"https://test.example.com/*"},
						ClientSecretRef: v1alpha1.ClientSecretRef{
							SecretReference: corev1.SecretReference{Name: "portal-client-secret-test-realm", Namespace: "default"},
						},
					}},
				},
			},
			cfg: &config.Config{Invite: config.InviteConfig{KeycloakBaseURL: "http://localhost", KeycloakClientID: "security-operator"}},
			expectErr: true,
			setupK8sMocks: func(m *mocks.MockClient, kcpClient *mocks.MockClient) {},
			setupKeycloakMocks: func(mux *http.ServeMux, baseURL string) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusCreated) })
				mux.HandleFunc("GET /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode([]map[string]any{{"clientId": "existing-client-id", "name": "test-realm"}})
				})
				mux.HandleFunc("POST /admin/realms/test-realm/clients/existing-client-id/registration-access-token", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]string{"registrationAccessToken": "new-registration-token"})
				})
				mux.HandleFunc("GET /realms/test-realm/clients-registrations/openid-connect/existing-client-id", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				})
			},
		},
		{
			desc: "error updating client via DCR",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{Name: "test-realm"},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					Clients: []v1alpha1.IdentityProviderClientConfig{{
						ClientName:        "test-realm",
						ClientType:        v1alpha1.IdentityProviderClientTypeConfidential,
						ValidRedirectURIs: []string{"https://test.example.com/*"},
						ClientSecretRef: v1alpha1.ClientSecretRef{
							SecretReference: corev1.SecretReference{Name: "portal-client-secret-test-realm", Namespace: "default"},
						},
					}},
				},
			},
			cfg: &config.Config{Invite: config.InviteConfig{KeycloakBaseURL: "http://localhost", KeycloakClientID: "security-operator"}},
			expectErr: true,
			setupK8sMocks: func(m *mocks.MockClient, kcpClient *mocks.MockClient) {},
			setupKeycloakMocks: func(mux *http.ServeMux, baseURL string) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusCreated) })
				mux.HandleFunc("GET /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode([]map[string]any{{"clientId": "existing-client-id", "name": "test-realm"}})
				})
				mux.HandleFunc("POST /admin/realms/test-realm/clients/existing-client-id/registration-access-token", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]string{"registrationAccessToken": "new-registration-token"})
				})
				mux.HandleFunc("GET /realms/test-realm/clients-registrations/openid-connect/existing-client-id", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"client_id":                "existing-client-id",
						"client_secret":            "existing-secret",
						"registration_access_token": "rotated-token",
						"registration_client_uri":   fmt.Sprintf("%s/realms/test-realm/clients-registrations/openid-connect/existing-client-id", baseURL),
					})
				})
				mux.HandleFunc("PUT /realms/test-realm/clients-registrations/openid-connect/existing-client-id", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":"bad request"}`))
				})
			},
		},
	}

	for _, test := range testCases {
		t.Run(test.desc, func(t *testing.T) {
			mux := http.NewServeMux()
			srv := httptest.NewServer(mux)
			defer srv.Close()

			configureOIDCProvider(t, mux, srv.URL)
			ctx := context.WithValue(context.Background(), oauth2.HTTPClient, srv.Client())

			orgsClient := mocks.NewMockClient(t)
			kcpClient := mocks.NewMockClient(t)
			var mgr *mocks.MockManager
			
			if test.desc == "error cluster context fails" {
				mgr = mocks.NewMockManager(t)
				mgr.EXPECT().ClusterFromContext(mock.Anything).Return(nil, fmt.Errorf("cluster context error")).Once()
			} else {
				mgr, _ = setupManagerAndCluster(t, kcpClient)
			}

			if test.setupK8sMocks != nil {
				test.setupK8sMocks(orgsClient, kcpClient)
			}

			if test.setupKeycloakMocks != nil {
				test.setupKeycloakMocks(mux, srv.URL)
			}

			cfg := getTestConfig(test.cfg, srv.URL)

			s, err := idp.New(ctx, cfg, orgsClient, mgr)
			if test.expectNewErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)

			l := testlogger.New()
			ctx = l.WithContext(ctx)

			_, opErr := s.Process(ctx, test.obj)
			if test.expectErr {
				assert.NotNil(t, opErr, "expected an operator error")
			} else {
				assert.Nil(t, opErr, "did not expect an operator error")
			}
		})
	}
}

func TestOIDCAPIErrors(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	configureOIDCProvider(t, mux, srv.URL)
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, srv.Client())

	orgsClient := mocks.NewMockClient(t)
	kcpClient := mocks.NewMockClient(t)
	mgr, _ := setupManagerAndCluster(t, kcpClient)

	s, err := idp.New(ctx, &config.Config{
		Invite: config.InviteConfig{
			KeycloakBaseURL:  srv.URL,
			KeycloakClientID: "security-operator",
		},
	}, orgsClient, mgr)
	assert.NoError(t, err)

	l := testlogger.New()
	ctx = l.WithContext(ctx)

	obj := &v1alpha1.IdentityProviderConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "test-realm"},
		Spec: v1alpha1.IdentityProviderConfigurationSpec{
			Clients: []v1alpha1.IdentityProviderClientConfig{{
				ClientName:        "test-realm",
				ClientType:        v1alpha1.IdentityProviderClientTypeConfidential,
				ValidRedirectURIs: []string{"https://test.example.com/*"},
				ClientSecretRef: v1alpha1.ClientSecretRef{
					SecretReference: corev1.SecretReference{Name: "portal-client-secret-test-realm", Namespace: "default"},
				},
			}},
		},
	}

	mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusCreated) })
	mux.HandleFunc("GET /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode([]map[string]any{{"clientId": "existing-client-id", "name": "test-realm"}})
	})
	mux.HandleFunc("POST /admin/realms/test-realm/clients/existing-client-id/registration-access-token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"registrationAccessToken": "new-registration-token"})
	})
	mux.HandleFunc("GET /realms/test-realm/clients-registrations/openid-connect/existing-client-id", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"client_id":                "existing-client-id",
			"client_secret":            "existing-secret",
			"registration_access_token": "rotated-token",
			"registration_client_uri":   fmt.Sprintf("%s/realms/test-realm/clients-registrations/openid-connect/existing-client-id", srv.URL),
		})
	})
	mux.HandleFunc("PUT /realms/test-realm/clients-registrations/openid-connect/existing-client-id", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{invalid-json`))
	})

	orgsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "portal-client-secret-test-realm")).Maybe()
	orgsClient.EXPECT().Create(mock.Anything, mock.Anything).Return(nil).Maybe()
	kcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	kcpClient.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	_, opErr := s.Process(ctx, obj)
	assert.NotNil(t, opErr)
}

func TestPublicClientType(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	configureOIDCProvider(t, mux, srv.URL)
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, srv.Client())

	orgsClient := mocks.NewMockClient(t)
	kcpClient := mocks.NewMockClient(t)
	mgr, _ := setupManagerAndCluster(t, kcpClient)

	s, err := idp.New(ctx, &config.Config{
		Invite: config.InviteConfig{
			KeycloakBaseURL:  srv.URL,
			KeycloakClientID: "security-operator",
		},
	}, orgsClient, mgr)
	assert.NoError(t, err)

	l := testlogger.New()
	ctx = l.WithContext(ctx)

	obj := &v1alpha1.IdentityProviderConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "public-realm"},
		Spec: v1alpha1.IdentityProviderConfigurationSpec{
			Clients: []v1alpha1.IdentityProviderClientConfig{{
				ClientName:        "public-realm",
				ClientType:        v1alpha1.IdentityProviderClientTypePublic,
				ValidRedirectURIs: []string{"https://test.example.com/*"},
				ClientSecretRef: v1alpha1.ClientSecretRef{
					SecretReference: corev1.SecretReference{Name: "portal-client-secret-public-realm", Namespace: "default"},
				},
			}},
		},
	}

	mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusCreated) })
	mux.HandleFunc("GET /admin/realms/public-realm/clients", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	})
	mux.HandleFunc("POST /admin/realms/public-realm/clients-initial-access", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "initial-access-token"})
	})
	mux.HandleFunc("POST /realms/public-realm/clients-registrations/openid-connect", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"client_id":                "public-client-id",
			"registration_access_token": "registration-token",
			"registration_client_uri":   fmt.Sprintf("%s/realms/public-realm/clients-registrations/openid-connect/public-client-id", srv.URL),
		})
	})

	orgsClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "portal-client-secret-public-realm")).Maybe()
	orgsClient.EXPECT().Create(mock.Anything, mock.Anything).Return(nil).Maybe()
	kcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	kcpClient.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	_, opErr := s.Process(ctx, obj)
	assert.Nil(t, opErr)
}

func TestFinalize(t *testing.T) {
	testCases := []struct {
		desc               string
		obj                runtimeobject.RuntimeObject
		cfg                *config.Config
		setupK8sMocks      func(m *mocks.MockClient)
		setupKeycloakMocks func(mux *http.ServeMux, baseURL string)
		expectErr          bool
	}{
		{
			desc: "finalize deletes client and realm successfully",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-realm",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					Clients: []v1alpha1.IdentityProviderClientConfig{
						{
							ClientName:        "test-realm",
							ClientID:          "client-id-123",
							ClientSecretRef: v1alpha1.ClientSecretRef{
								SecretReference: corev1.SecretReference{
									Name:      "portal-client-secret-test-realm",
									Namespace: "default",
								},
							},
						},
					},
				},
			},
			cfg: &config.Config{
				Invite: config.InviteConfig{
					KeycloakBaseURL:  "http://localhost",
					KeycloakClientID: "security-operator",
				},
			},
			setupK8sMocks: func(m *mocks.MockClient) {
				m.EXPECT().Delete(mock.Anything, mock.Anything).Return(nil).Once()
			},
			setupKeycloakMocks: func(mux *http.ServeMux, baseURL string) {
				mux.HandleFunc("POST /admin/realms/test-realm/clients/client-id-123/registration-access-token", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]string{"registrationAccessToken": "delete-token"})
				})
				mux.HandleFunc("DELETE /realms/test-realm/clients-registrations/openid-connect/client-id-123", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				})
				mux.HandleFunc("DELETE /admin/realms/test-realm", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				})
			},
		},
		{
			desc: "finalize error regenerating token",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-realm",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					Clients: []v1alpha1.IdentityProviderClientConfig{
						{
							ClientName:        "test-realm",
							ClientID:          "client-id-123",
							ClientSecretRef: v1alpha1.ClientSecretRef{
								SecretReference: corev1.SecretReference{
									Name:      "portal-client-secret-test-realm",
									Namespace: "default",
								},
							},
						},
					},
				},
			},
			cfg: &config.Config{
				Invite: config.InviteConfig{
					KeycloakBaseURL:  "http://localhost",
					KeycloakClientID: "security-operator",
				},
			},
			expectErr: true,
			setupK8sMocks: func(m *mocks.MockClient) {
			},
			setupKeycloakMocks: func(mux *http.ServeMux, baseURL string) {
				mux.HandleFunc("POST /admin/realms/test-realm/clients/client-id-123/registration-access-token", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				})
			},
		},
		{
			desc: "finalize error deleting client",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-realm",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					Clients: []v1alpha1.IdentityProviderClientConfig{
						{
							ClientName:        "test-realm",
							ClientID:          "client-id-123",
							ClientSecretRef: v1alpha1.ClientSecretRef{
								SecretReference: corev1.SecretReference{
									Name:      "portal-client-secret-test-realm",
									Namespace: "default",
								},
							},
						},
					},
				},
			},
			cfg: &config.Config{
				Invite: config.InviteConfig{
					KeycloakBaseURL:  "http://localhost",
					KeycloakClientID: "security-operator",
				},
			},
			expectErr: true,
			setupK8sMocks: func(m *mocks.MockClient) {
			},
			setupKeycloakMocks: func(mux *http.ServeMux, baseURL string) {
				mux.HandleFunc("POST /admin/realms/test-realm/clients/client-id-123/registration-access-token", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]string{"registrationAccessToken": "delete-token"})
				})
				mux.HandleFunc("DELETE /realms/test-realm/clients-registrations/openid-connect/client-id-123", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":"bad request"}`))
				})
			},
		},
		{
			desc: "finalize error deleting realm",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-realm",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					Clients: []v1alpha1.IdentityProviderClientConfig{
						{
							ClientName:        "test-realm",
							ClientID:          "client-id-123",
							ClientSecretRef: v1alpha1.ClientSecretRef{
								SecretReference: corev1.SecretReference{
									Name:      "portal-client-secret-test-realm",
									Namespace: "default",
								},
							},
						},
					},
				},
			},
			cfg: &config.Config{
				Invite: config.InviteConfig{
					KeycloakBaseURL:  "http://localhost",
					KeycloakClientID: "security-operator",
				},
			},
			expectErr: true,
			setupK8sMocks: func(m *mocks.MockClient) {
				m.EXPECT().Delete(mock.Anything, mock.Anything).Return(nil).Once()
			},
			setupKeycloakMocks: func(mux *http.ServeMux, baseURL string) {
				mux.HandleFunc("POST /admin/realms/test-realm/clients/client-id-123/registration-access-token", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]string{"registrationAccessToken": "delete-token"})
				})
				mux.HandleFunc("DELETE /realms/test-realm/clients-registrations/openid-connect/client-id-123", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				})
				mux.HandleFunc("DELETE /admin/realms/test-realm", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				})
			},
		},
	}

	for _, test := range testCases {
		t.Run(test.desc, func(t *testing.T) {
			mux := http.NewServeMux()
			srv := httptest.NewServer(mux)
			defer srv.Close()

			configureOIDCProvider(t, mux, srv.URL)
			ctx := context.WithValue(context.Background(), oauth2.HTTPClient, srv.Client())

			orgsClient := mocks.NewMockClient(t)
			mgr := mocks.NewMockManager(t)

			if test.setupK8sMocks != nil {
				test.setupK8sMocks(orgsClient)
			}

			if test.setupKeycloakMocks != nil {
				test.setupKeycloakMocks(mux, srv.URL)
			}

			setRegistrationClientURI(test.obj, srv.URL)
			cfg := getTestConfig(test.cfg, srv.URL)

			s, err := idp.New(ctx, cfg, orgsClient, mgr)
			assert.NoError(t, err)

			l := testlogger.New()
			ctx = l.WithContext(ctx)

			_, opErr := s.Finalize(ctx, test.obj)
			if test.expectErr {
				assert.NotNil(t, opErr, "expected an operator error")
			} else {
				assert.Nil(t, opErr, "did not expect an operator error")
			}
		})
	}
}

func TestHelperFunctions(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	configureOIDCProvider(t, mux, srv.URL)
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, srv.Client())

	kcpClient := mocks.NewMockClient(t)
	mgr, _ := setupManagerAndCluster(t, kcpClient)

	s, err := idp.New(ctx, &config.Config{
		Invite: config.InviteConfig{
			KeycloakBaseURL:  srv.URL,
			KeycloakClientID: "security-operator",
		},
	}, nil, mgr)
	assert.NoError(t, err)

	assert.Equal(t, "IdentityProviderConfiguration", s.GetName())
	assert.Equal(t, []string{"core.platform-mesh.io/idp-finalizer"}, s.Finalizers(nil))

	res, finalizerErr := s.Finalize(ctx, &v1alpha1.IdentityProviderConfiguration{})
	assert.Nil(t, finalizerErr)
	assert.Equal(t, ctrl.Result{}, res)
}

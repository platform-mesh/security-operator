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
			desc: "realm and client created successfully with SMTP config",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-realm-smtp",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					Clients: []v1alpha1.IdentityProviderClientConfig{
						{
							ClientName:        "test-realm-smtp",
							ClientType:        v1alpha1.IdentityProviderClientTypePublic,
							ValidRedirectURIs: []string{"https://test.example.com/*"},
							ClientSecretRef: v1alpha1.ClientSecretRef{
								SecretReference: corev1.SecretReference{
									Name:      "portal-client-secret-test-realm-smtp",
									Namespace: "default",
								},
							},
						},
					},
				},
			},
			cfg: func() *config.Config {
				cfg := &config.Config{
					Invite: config.InviteConfig{
						KeycloakBaseURL:  "http://localhost",
						KeycloakClientID: "security-operator",
					},
				}
				cfg.IDP.SMTPServer = "smtp.example.com"
				cfg.IDP.SMTPPort = 587
				cfg.IDP.FromAddress = "[email protected]"
				cfg.IDP.SSL = false
				cfg.IDP.StartTLS = true
				return cfg
			}(),
			setupK8sMocks: func(m *mocks.MockClient, kcpClient *mocks.MockClient) {
				m.EXPECT().Create(mock.Anything, mock.Anything).Return(nil).Maybe()
				m.EXPECT().Update(mock.Anything, mock.Anything).Return(nil).Maybe()
				m.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "portal-client-secret-test-realm-smtp")).Maybe()
				kcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
				kcpClient.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
			},
			setupKeycloakMocks: func(mux *http.ServeMux, baseURL string) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) {
					var realm map[string]any
					_ = json.NewDecoder(r.Body).Decode(&realm)
					smtpServer, ok := realm["smtpServer"].(map[string]any)
					assert.True(t, ok, "smtpServer should be present")
					assert.Equal(t, "smtp.example.com", smtpServer["host"])
					assert.Equal(t, "587", smtpServer["port"])
					assert.Equal(t, "[email protected]", smtpServer["from"])
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("GET /admin/realms/test-realm-smtp/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					clients := []map[string]any{}
					_ = json.NewEncoder(w).Encode(clients)
				})
				mux.HandleFunc("POST /admin/realms/test-realm-smtp/clients-initial-access", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(map[string]string{"token": "initial-access-token"})
				})
				mux.HandleFunc("POST /realms/test-realm-smtp/clients-registrations/openid-connect", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"client_id":                "generated-client-id-456",
						"registration_access_token": "registration-token-456",
						"registration_client_uri":   fmt.Sprintf("%s/realms/test-realm-smtp/clients-registrations/openid-connect/generated-client-id-456", baseURL),
					})
				})
			},
		},
		{
			desc: "realm and client created successfully with SMTP auth",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-realm-smtp-auth",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					Clients: []v1alpha1.IdentityProviderClientConfig{
						{
							ClientName:        "test-realm-smtp-auth",
							ClientType:        v1alpha1.IdentityProviderClientTypeConfidential,
							ValidRedirectURIs: []string{"https://test.example.com/*"},
							ClientSecretRef: v1alpha1.ClientSecretRef{
								SecretReference: corev1.SecretReference{
									Name:      "portal-client-secret-test-realm-smtp-auth",
									Namespace: "default",
								},
							},
						},
					},
				},
			},
			cfg: func() *config.Config {
				cfg := &config.Config{
					Invite: config.InviteConfig{
						KeycloakBaseURL:  "http://localhost",
						KeycloakClientID: "security-operator",
					},
				}
				cfg.IDP.SMTPServer = "smtp.example.com"
				cfg.IDP.SMTPPort = 587
				cfg.IDP.FromAddress = "[email protected]"
				cfg.IDP.SSL = false
				cfg.IDP.StartTLS = true
				cfg.IDP.SMTPUser = "smtp-user"
				cfg.IDP.SMTPPassword = "smtp-password-123"
				return cfg
			}(),
			setupK8sMocks: func(m *mocks.MockClient, kcpClient *mocks.MockClient) {
				m.EXPECT().Create(mock.Anything, mock.Anything).Return(nil).Maybe()
				m.EXPECT().Update(mock.Anything, mock.Anything).Return(nil).Maybe()
				m.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "portal-client-secret-test-realm-smtp-auth")).Maybe()
				kcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
				kcpClient.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
			},
			setupKeycloakMocks: func(mux *http.ServeMux, baseURL string) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) {
					var realm map[string]any
					_ = json.NewDecoder(r.Body).Decode(&realm)
					smtpServer, ok := realm["smtpServer"].(map[string]any)
					assert.True(t, ok, "smtpServer should be present")
					assert.Equal(t, true, smtpServer["auth"])
					assert.Equal(t, "smtp-user", smtpServer["user"])
					assert.Equal(t, "smtp-password-123", smtpServer["password"])
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("GET /admin/realms/test-realm-smtp-auth/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					clients := []map[string]any{}
					_ = json.NewEncoder(w).Encode(clients)
				})
				mux.HandleFunc("POST /admin/realms/test-realm-smtp-auth/clients-initial-access", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(map[string]string{"token": "initial-access-token"})
				})
				mux.HandleFunc("POST /realms/test-realm-smtp-auth/clients-registrations/openid-connect", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"client_id":                "generated-client-id-789",
						"client_secret":            "client-secret-789",
						"registration_access_token": "registration-token-789",
						"registration_client_uri":   fmt.Sprintf("%s/realms/test-realm-smtp-auth/clients-registrations/openid-connect/generated-client-id-789", baseURL),
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
			desc: "error creating secret in Kubernetes",
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
				m.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "portal-client-secret-test-realm")).Once()
				m.EXPECT().Create(mock.Anything, mock.Anything).Return(fmt.Errorf("failed to create secret")).Once()
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
						"client_id":                "generated-client-id-secret-error",
						"client_secret":            "client-secret",
						"registration_access_token": "registration-token",
						"registration_client_uri":   fmt.Sprintf("%s/realms/test-realm/clients-registrations/openid-connect/generated-client-id-secret-error", baseURL),
					})
				})
			},
		},
		{
			desc: "error patching IdentityProviderConfiguration",
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
				m.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "portal-client-secret-test-realm")).Maybe()
				m.EXPECT().Create(mock.Anything, mock.Anything).Return(nil).Maybe()
				kcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
				kcpClient.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("failed to patch")).Once()
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
						"client_id":                "generated-client-id-patch-error",
						"client_secret":            "client-secret",
						"registration_access_token": "registration-token",
						"registration_client_uri":   fmt.Sprintf("%s/realms/test-realm/clients-registrations/openid-connect/generated-client-id-patch-error", baseURL),
					})
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
			mgr, _ := setupManagerAndCluster(t, kcpClient)

			if test.setupK8sMocks != nil {
				test.setupK8sMocks(orgsClient, kcpClient)
			}

			if test.setupKeycloakMocks != nil {
				test.setupKeycloakMocks(mux, srv.URL)
			}

			cfg := test.cfg
			if cfg == nil {
				cfg = &config.Config{
					Invite: config.InviteConfig{
						KeycloakBaseURL:  srv.URL,
						KeycloakClientID: "security-operator",
					},
				}
			} else {
				cfg.Invite.KeycloakBaseURL = srv.URL
			}

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

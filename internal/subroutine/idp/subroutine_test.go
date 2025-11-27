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

type conditionalFailingTransport struct {
	transport  http.RoundTripper
	failPath   string
	failMethod string
}

func (t *conditionalFailingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path == t.failPath && req.Method == t.failMethod {
		return nil, fmt.Errorf("network error")
	}
	return t.transport.RoundTrip(req)
}

func TestSubroutineProcess(t *testing.T) {
	testCases := []struct {
		desc               string
		obj                runtimeobject.RuntimeObject
		cfg                *config.Config
		setupK8sMocks      func(m *mocks.MockClient)
		setupKeycloakMocks func(mux *http.ServeMux)
		failPath           string
		failMethod         string
		expectErr          bool
	}{
		{
			desc: "realm and client created successfully without SMTP",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-realm",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					ClientID:         "test-client",
					ClientType:       v1alpha1.IdentityProviderClientTypeConfidential,
					ValidRedirectURIs: []string{"https://test.example.com/*"},
				},
			},
			cfg: &config.Config{
				Invite: config.InviteConfig{
					KeycloakBaseURL:  "http://localhost",
					KeycloakClientID: "security-operator",
				},
			},
			setupK8sMocks: func(m *mocks.MockClient) {
				m.EXPECT().Create(mock.Anything, mock.Anything).Return(nil).Maybe()
				m.EXPECT().Update(mock.Anything, mock.Anything).Return(nil).Maybe()
				m.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
			},
			setupKeycloakMocks: func(mux *http.ServeMux) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("POST /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("GET /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					clients := []map[string]any{
						{"id": "client-uuid-123", "clientId": "test-client"},
					}
					_ = json.NewEncoder(w).Encode(clients)
				})
				mux.HandleFunc("GET /admin/realms/test-realm/clients/client-uuid-123/client-secret", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]string{"value": "secret-value-123"})
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
					ClientID:         "test-client",
					ClientType:       v1alpha1.IdentityProviderClientTypePublic,
					ValidRedirectURIs: []string{"https://test.example.com/*"},
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
			setupK8sMocks: func(m *mocks.MockClient) {
				m.EXPECT().Create(mock.Anything, mock.Anything).Return(nil).Maybe()
				m.EXPECT().Update(mock.Anything, mock.Anything).Return(nil).Maybe()
				m.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
			},
			setupKeycloakMocks: func(mux *http.ServeMux) {
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
				mux.HandleFunc("POST /admin/realms/test-realm-smtp/clients", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("GET /admin/realms/test-realm-smtp/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					clients := []map[string]any{
						{"id": "client-uuid-456", "clientId": "test-client"},
					}
					_ = json.NewEncoder(w).Encode(clients)
				})
				mux.HandleFunc("GET /admin/realms/test-realm-smtp/clients/client-uuid-456/client-secret", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]string{"value": "secret-value-456"})
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
					ClientID:         "test-client",
					ClientType:       v1alpha1.IdentityProviderClientTypeConfidential,
					ValidRedirectURIs: []string{"https://test.example.com/*"},
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
			setupK8sMocks: func(m *mocks.MockClient) {
				m.EXPECT().Create(mock.Anything, mock.Anything).Return(nil).Maybe()
				m.EXPECT().Update(mock.Anything, mock.Anything).Return(nil).Maybe()
				m.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
			},
			setupKeycloakMocks: func(mux *http.ServeMux) {
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
				mux.HandleFunc("POST /admin/realms/test-realm-smtp-auth/clients", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("GET /admin/realms/test-realm-smtp-auth/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					clients := []map[string]any{
						{"id": "client-uuid-789", "clientId": "test-client"},
					}
					_ = json.NewEncoder(w).Encode(clients)
				})
				mux.HandleFunc("GET /admin/realms/test-realm-smtp-auth/clients/client-uuid-789/client-secret", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]string{"value": "secret-value-789"})
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
					ClientID:         "test-client",
					ClientType:       v1alpha1.IdentityProviderClientTypeConfidential,
					ValidRedirectURIs: []string{"https://test.example.com/*"},
				},
			},
			cfg: &config.Config{
				Invite: config.InviteConfig{
					KeycloakBaseURL:  "http://localhost",
					KeycloakClientID: "security-operator",
				},
			},
			setupK8sMocks: func(m *mocks.MockClient) {
				m.EXPECT().Create(mock.Anything, mock.Anything).Return(nil).Maybe()
				m.EXPECT().Update(mock.Anything, mock.Anything).Return(nil).Maybe()
				m.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
			},
			setupKeycloakMocks: func(mux *http.ServeMux) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusConflict)
				})
				mux.HandleFunc("POST /admin/realms/existing-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("GET /admin/realms/existing-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					clients := []map[string]any{
						{"id": "client-uuid-existing", "clientId": "test-client"},
					}
					_ = json.NewEncoder(w).Encode(clients)
				})
				mux.HandleFunc("GET /admin/realms/existing-realm/clients/client-uuid-existing/client-secret", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]string{"value": "secret-value-existing"})
				})
			},
		},
		{
			desc: "client already exists",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-realm",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					ClientID:         "existing-client",
					ClientType:       v1alpha1.IdentityProviderClientTypeConfidential,
					ValidRedirectURIs: []string{"https://test.example.com/*"},
				},
			},
			cfg: &config.Config{
				Invite: config.InviteConfig{
					KeycloakBaseURL:  "http://localhost",
					KeycloakClientID: "security-operator",
				},
			},
			setupK8sMocks: func(m *mocks.MockClient) {
				m.EXPECT().Create(mock.Anything, mock.Anything).Return(nil).Maybe()
				m.EXPECT().Update(mock.Anything, mock.Anything).Return(nil).Maybe()
				m.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
			},
			setupKeycloakMocks: func(mux *http.ServeMux) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("POST /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusConflict)
				})
				mux.HandleFunc("GET /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					clients := []map[string]any{
						{"id": "client-uuid-existing", "clientId": "existing-client"},
					}
					_ = json.NewEncoder(w).Encode(clients)
				})
				mux.HandleFunc("GET /admin/realms/test-realm/clients/client-uuid-existing/client-secret", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]string{"value": "secret-value-existing"})
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
					ClientID:         "test-client",
					ClientType:       v1alpha1.IdentityProviderClientTypeConfidential,
					ValidRedirectURIs: []string{"https://test.example.com/*"},
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
			setupKeycloakMocks: func(mux *http.ServeMux) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":"internal server error"}`))
				})
			},
		},
		{
			desc: "error creating client",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-realm",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					ClientID:         "invalid-client",
					ClientType:       v1alpha1.IdentityProviderClientTypeConfidential,
					ValidRedirectURIs: []string{"https://test.example.com/*"},
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
			setupKeycloakMocks: func(mux *http.ServeMux) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("POST /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":"invalid client configuration"}`))
				})
			},
		},
		{
			desc: "error getting client UUID",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-realm",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					ClientID:         "missing-client",
					ClientType:       v1alpha1.IdentityProviderClientTypeConfidential,
					ValidRedirectURIs: []string{"https://test.example.com/*"},
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
			setupKeycloakMocks: func(mux *http.ServeMux) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("POST /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("GET /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					clients := []map[string]any{}
					_ = json.NewEncoder(w).Encode(clients)
				})
			},
		},
		{
			desc: "error getting client secret",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-realm",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					ClientID:         "test-client",
					ClientType:       v1alpha1.IdentityProviderClientTypeConfidential,
					ValidRedirectURIs: []string{"https://test.example.com/*"},
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
			setupKeycloakMocks: func(mux *http.ServeMux) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("POST /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("GET /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					clients := []map[string]any{
						{"id": "client-uuid-error", "clientId": "test-client"},
					}
					_ = json.NewEncoder(w).Encode(clients)
				})
				mux.HandleFunc("GET /admin/realms/test-realm/clients/client-uuid-error/client-secret", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":"internal server error"}`))
				})
			},
		},
		{
			desc: "error creating realm",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-realm",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					ClientID:         "test-client",
					ClientType:       v1alpha1.IdentityProviderClientTypeConfidential,
					ValidRedirectURIs: []string{"https://test.example.com/*"},
				},
			},
			cfg: &config.Config{
				Invite: config.InviteConfig{
					KeycloakBaseURL:  "http://localhost",
					KeycloakClientID: "security-operator",
				},
			},
			failPath:   "/admin/realms",
			failMethod: "POST",
			expectErr:  true,
			setupK8sMocks: func(m *mocks.MockClient) {
			},
			setupKeycloakMocks: func(mux *http.ServeMux) {
			},
		},
		{
			desc: "error creating client",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-realm",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					ClientID:         "test-client",
					ClientType:       v1alpha1.IdentityProviderClientTypeConfidential,
					ValidRedirectURIs: []string{"https://test.example.com/*"},
				},
			},
			cfg: &config.Config{
				Invite: config.InviteConfig{
					KeycloakBaseURL:  "http://localhost",
					KeycloakClientID: "security-operator",
				},
			},
			failPath:   "/admin/realms/test-realm/clients",
			failMethod: "POST",
			expectErr:  true,
			setupK8sMocks: func(m *mocks.MockClient) {
			},
			setupKeycloakMocks: func(mux *http.ServeMux) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
			},
		},
		{
			desc: "error creating realm",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-realm",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					ClientID:         "test-client",
					ClientType:       v1alpha1.IdentityProviderClientTypeConfidential,
					ValidRedirectURIs: []string{"https://test.example.com/*"},
				},
			},
			cfg: &config.Config{
				Invite: config.InviteConfig{
					KeycloakBaseURL:  "http://%gh&%ij",
					KeycloakClientID: "security-operator",
				},
			},
			expectErr: true,
			setupK8sMocks: func(m *mocks.MockClient) {
			},
			setupKeycloakMocks: func(mux *http.ServeMux) {
			},
		},
		{
			desc: "error getting client secret",
			obj: &v1alpha1.IdentityProviderConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-realm",
				},
				Spec: v1alpha1.IdentityProviderConfigurationSpec{
					ClientID:         "test-client",
					ClientType:       v1alpha1.IdentityProviderClientTypeConfidential,
					ValidRedirectURIs: []string{"https://test.example.com/*"},
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
			setupKeycloakMocks: func(mux *http.ServeMux) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("POST /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("GET /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					clients := []map[string]any{
						{"id": "client-uuid-bad-json", "clientId": "test-client"},
					}
					_ = json.NewEncoder(w).Encode(clients)
				})
				mux.HandleFunc("GET /admin/realms/test-realm/clients/client-uuid-bad-json/client-secret", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{invalid-json`))
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
					ClientID:         "test-client",
					ClientType:       v1alpha1.IdentityProviderClientTypeConfidential,
					ValidRedirectURIs: []string{"https://test.example.com/*"},
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
				m.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "portal-client-secret-test-realm")).Once()
				m.EXPECT().Create(mock.Anything, mock.Anything).Return(fmt.Errorf("failed to create secret")).Once()
			},
			setupKeycloakMocks: func(mux *http.ServeMux) {
				mux.HandleFunc("POST /admin/realms", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("POST /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
				})
				mux.HandleFunc("GET /admin/realms/test-realm/clients", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					clients := []map[string]any{
						{"id": "client-uuid-secret-error", "clientId": "test-client"},
					}
					_ = json.NewEncoder(w).Encode(clients)
				})
				mux.HandleFunc("GET /admin/realms/test-realm/clients/client-uuid-secret-error/client-secret", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(map[string]string{"value": "secret-value"})
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

			if test.setupK8sMocks != nil {
				test.setupK8sMocks(orgsClient)
			}

			if test.setupKeycloakMocks != nil {
				test.setupKeycloakMocks(mux)
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

			s, err := idp.New(ctx, cfg, orgsClient)
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

	s, err := idp.New(ctx, &config.Config{
		Invite: config.InviteConfig{
			KeycloakBaseURL:  srv.URL,
			KeycloakClientID: "security-operator",
		},
	}, nil)
	assert.NoError(t, err)

	assert.Equal(t, "IdentityProviderConfiguration", s.GetName())
	assert.Equal(t, []string{}, s.Finalizers(nil))

	res, finalizerErr := s.Finalize(ctx, &v1alpha1.IdentityProviderConfiguration{})
	assert.Nil(t, finalizerErr)
	assert.Equal(t, ctrl.Result{}, res)
}


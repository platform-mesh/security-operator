package keycloak

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminClient_TokenForRegistration(t *testing.T) {
	tests := []struct {
		name        string
		setupServer func(t *testing.T) *httptest.Server
		wantToken   string
		wantErr     bool
	}{
		{
			name: "successful token retrieval",
			setupServer: func(t *testing.T) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, http.MethodPost, r.Method)
					assert.Equal(t, "/admin/realms/test-realm/clients-initial-access", r.URL.Path)
					assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(map[string]string{"token": "initial-access-token-123"})
				}))
			},
			wantToken: "initial-access-token-123",
		},
		{
			name: "server returns error",
			setupServer: func(t *testing.T) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusForbidden)
					w.Write([]byte("access denied"))
				}))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer(t)
			defer server.Close()

			client := NewAdminClient(http.DefaultClient, server.URL, "test-realm")
			token, err := client.TokenForRegistration(context.Background())

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantToken, token)
		})
	}
}

func TestAdminClient_RefreshToken(t *testing.T) {
	tests := []struct {
		name        string
		clientID    string
		setupServer func(t *testing.T) *httptest.Server
		wantToken   string
		wantErr     bool
	}{
		{
			name:     "successful token refresh",
			clientID: "my-client-id",
			setupServer: func(t *testing.T) *httptest.Server {
				callCount := 0
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					callCount++
					if callCount == 1 {
						// First call: list clients to resolve UUID
						assert.Equal(t, "/admin/realms/test-realm/clients", r.URL.Path)
						w.WriteHeader(http.StatusOK)
						json.NewEncoder(w).Encode([]ClientInfo{
							{ID: "uuid-123", ClientID: "my-client-id", Name: "my-client"},
						})
						return
					}
					// Second call: regenerate token
					assert.Equal(t, "/admin/realms/test-realm/clients/uuid-123/registration-access-token", r.URL.Path)
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(map[string]string{"registrationAccessToken": "refreshed-token"})
				}))
			},
			wantToken: "refreshed-token",
		},
		{
			name:     "client not found",
			clientID: "unknown-client",
			setupServer: func(t *testing.T) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode([]ClientInfo{})
				}))
			},
			wantErr: true,
		},
		{
			name:     "token regeneration fails",
			clientID: "my-client-id",
			setupServer: func(t *testing.T) *httptest.Server {
				callCount := 0
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					callCount++
					if callCount == 1 {
						w.WriteHeader(http.StatusOK)
						json.NewEncoder(w).Encode([]ClientInfo{
							{ID: "uuid-123", ClientID: "my-client-id", Name: "my-client"},
						})
						return
					}
					w.WriteHeader(http.StatusInternalServerError)
				}))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer(t)
			defer server.Close()

			client := NewAdminClient(http.DefaultClient, server.URL, "test-realm")
			token, err := client.RefreshToken(context.Background(), tt.clientID)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantToken, token)
		})
	}
}

func TestAdminClient_RegistrationEndpoint(t *testing.T) {
	client := NewAdminClient(nil, "https://keycloak.example.com", "my-realm")
	endpoint := client.RegistrationEndpoint()

	assert.Equal(t, "https://keycloak.example.com/realms/my-realm/clients-registrations/openid-connect", endpoint)
}

func TestAdminClient_RegistrationEndpoint_TrailingSlash(t *testing.T) {
	client := NewAdminClient(nil, "https://keycloak.example.com/", "my-realm")
	endpoint := client.RegistrationEndpoint()

	assert.Equal(t, "https://keycloak.example.com/realms/my-realm/clients-registrations/openid-connect", endpoint)
}

func TestAdminClient_CreateOrUpdateRealm(t *testing.T) {
	tests := []struct {
		name        string
		config      RealmConfig
		setupServer func(t *testing.T) *httptest.Server
		wantCreated bool
		wantErr     bool
	}{
		{
			name: "realm created",
			config: RealmConfig{
				Realm:   "new-realm",
				Enabled: true,
			},
			setupServer: func(t *testing.T) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, http.MethodPost, r.Method)
					assert.Equal(t, "/admin/realms", r.URL.Path)
					w.WriteHeader(http.StatusCreated)
				}))
			},
			wantCreated: true,
		},
		{
			name: "realm updated on conflict",
			config: RealmConfig{
				Realm:   "existing-realm",
				Enabled: true,
			},
			setupServer: func(t *testing.T) *httptest.Server {
				callCount := 0
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					callCount++
					if callCount == 1 {
						assert.Equal(t, http.MethodPost, r.Method)
						w.WriteHeader(http.StatusConflict)
						return
					}
					assert.Equal(t, http.MethodPut, r.Method)
					assert.Equal(t, "/admin/realms/existing-realm", r.URL.Path)
					w.WriteHeader(http.StatusNoContent)
				}))
			},
			wantCreated: false,
		},
		{
			name: "server error",
			config: RealmConfig{
				Realm: "error-realm",
			},
			setupServer: func(t *testing.T) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				}))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer(t)
			defer server.Close()

			client := NewAdminClient(http.DefaultClient, server.URL, "test-realm")
			created, err := client.CreateOrUpdateRealm(context.Background(), tt.config)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantCreated, created)
		})
	}
}

func TestAdminClient_DeleteRealm(t *testing.T) {
	tests := []struct {
		name        string
		realmName   string
		setupServer func(t *testing.T) *httptest.Server
		wantErr     bool
	}{
		{
			name:      "successful delete",
			realmName: "my-realm",
			setupServer: func(t *testing.T) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, http.MethodDelete, r.Method)
					assert.Equal(t, "/admin/realms/my-realm", r.URL.Path)
					w.WriteHeader(http.StatusNoContent)
				}))
			},
		},
		{
			name:      "realm not found is success",
			realmName: "missing-realm",
			setupServer: func(t *testing.T) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				}))
			},
		},
		{
			name:      "server error",
			realmName: "error-realm",
			setupServer: func(t *testing.T) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				}))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer(t)
			defer server.Close()

			client := NewAdminClient(http.DefaultClient, server.URL, "test-realm")
			err := client.DeleteRealm(context.Background(), tt.realmName)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
		})
	}
}

func TestAdminClient_GetClientByName(t *testing.T) {
	tests := []struct {
		name        string
		clientName  string
		setupServer func(t *testing.T) *httptest.Server
		wantClient  *ClientInfo
		wantErr     bool
	}{
		{
			name:       "client found",
			clientName: "my-client",
			setupServer: func(t *testing.T) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "/admin/realms/test-realm/clients", r.URL.Path)
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode([]ClientInfo{
						{ID: "uuid-1", ClientID: "client-id-1", Name: "other-client"},
						{ID: "uuid-2", ClientID: "client-id-2", Name: "my-client"},
					})
				}))
			},
			wantClient: &ClientInfo{ID: "uuid-2", ClientID: "client-id-2", Name: "my-client"},
		},
		{
			name:       "client not found",
			clientName: "missing-client",
			setupServer: func(t *testing.T) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode([]ClientInfo{})
				}))
			},
			wantClient: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer(t)
			defer server.Close()

			client := NewAdminClient(http.DefaultClient, server.URL, "test-realm")
			info, err := client.GetClientByName(context.Background(), tt.clientName)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantClient, info)
		})
	}
}

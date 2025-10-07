package invite_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	accountsv1alpha1 "github.com/platform-mesh/account-operator/api/v1alpha1"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	"github.com/platform-mesh/golang-commons/logger/testlogger"
	"github.com/platform-mesh/security-operator/api/v1alpha1"
	"github.com/platform-mesh/security-operator/internal/config"
	"github.com/platform-mesh/security-operator/internal/subroutine/invite"
	"github.com/platform-mesh/security-operator/internal/subroutine/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"golang.org/x/oauth2"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func TestSubroutineProcess(t *testing.T) {
	testCases := []struct {
		desc               string
		obj                runtimeobject.RuntimeObject
		setupK8sMocks      func(m *mocks.MockClient)
		setupKeycloakMocks func(mux *http.ServeMux)
		expectErr          bool
	}{
		{
			desc: "",
			obj: &v1alpha1.Invite{
				Spec: v1alpha1.InviteSpec{
					Email: "example@acme.corp",
				},
			},
			setupK8sMocks: func(m *mocks.MockClient) {
				m.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
					func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
						accountInfo := &accountsv1alpha1.AccountInfo{
							Spec: accountsv1alpha1.AccountInfoSpec{
								Organization: accountsv1alpha1.AccountLocation{
									Name: "acme",
								},
							},
						}
						*o.(*accountsv1alpha1.AccountInfo) = *accountInfo
						return nil
					},
				)
			},
			setupKeycloakMocks: func(mux *http.ServeMux) {
				users := []map[string]any{}
				mux.HandleFunc("GET /admin/realms/acme/users", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(&users)
				})
				mux.HandleFunc("POST /admin/realms/acme/users", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					user := map[string]any{}
					err := json.NewDecoder(r.Body).Decode(&user)
					assert.NoError(t, err)
					user["id"] = "1234"
					users = append(users, user)
				})
				mux.HandleFunc("PUT /admin/realms/acme/users/{id}/execute-actions-email", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusNoContent)
				})
			},
		},
		{
			desc: "error listing users (500 from Keycloak)",
			obj: &v1alpha1.Invite{
				Spec: v1alpha1.InviteSpec{
					Email: "error1@acme.corp",
				},
			},
			expectErr: true,
			setupK8sMocks: func(m *mocks.MockClient) {
				m.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
					func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
						accountInfo := &accountsv1alpha1.AccountInfo{
							Spec: accountsv1alpha1.AccountInfoSpec{
								Organization: accountsv1alpha1.AccountLocation{
									Name: "acme",
								},
							},
						}
						*o.(*accountsv1alpha1.AccountInfo) = *accountInfo
						return nil
					},
				)
			},
			setupKeycloakMocks: func(mux *http.ServeMux) {
				mux.HandleFunc("GET /admin/realms/acme/users", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":"boom"}`))
				})
			},
		},
		{
			desc: "error creating user (POST returns 400)",
			obj: &v1alpha1.Invite{
				Spec: v1alpha1.InviteSpec{
					Email: "error2@acme.corp",
				},
			},
			expectErr: true,
			setupK8sMocks: func(m *mocks.MockClient) {
				m.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
					func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
						accountInfo := &accountsv1alpha1.AccountInfo{
							Spec: accountsv1alpha1.AccountInfoSpec{
								Organization: accountsv1alpha1.AccountLocation{
									Name: "acme",
								},
							},
						}
						*o.(*accountsv1alpha1.AccountInfo) = *accountInfo
						return nil
					},
				)
			},
			setupKeycloakMocks: func(mux *http.ServeMux) {
				first := true
				mux.HandleFunc("GET /admin/realms/acme/users", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					if first {
						first = false
						_ = json.NewEncoder(w).Encode([]map[string]any{})
						return
					}
					_ = json.NewEncoder(w).Encode([]map[string]any{
						{"id": "should-not-exist", "email": "error2@acme.corp"},
					})
				})
				mux.HandleFunc("POST /admin/realms/acme/users", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":"invalid user"}`))
				})
			},
		},
		{
			desc: "error fetching AccountInfo (not found)",
			obj: &v1alpha1.Invite{
				Spec: v1alpha1.InviteSpec{
					Email: "missing@acme.corp",
				},
			},
			expectErr: true,
			setupK8sMocks: func(m *mocks.MockClient) {
				// Simulate k8s Get failure (AccountInfo missing)
				m.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("accountinfo not found"))
			},
			setupKeycloakMocks: func(mux *http.ServeMux) {
				// No Keycloak calls expected when AccountInfo fetch fails.
			},
		},
	}
	for _, test := range testCases {
		t.Run(test.desc, func(t *testing.T) {
			mux := http.NewServeMux()
			srv := httptest.NewServer(mux)
			defer srv.Close()

			configureOIDCProvider(t, mux, srv.URL)
			ctx := context.WithValue(t.Context(), oauth2.HTTPClient, srv.Client())

			k8s := mocks.NewMockClient(t)
			if test.setupK8sMocks != nil {
				test.setupK8sMocks(k8s)
			}
			if test.setupKeycloakMocks != nil {
				test.setupKeycloakMocks(mux)
			}

			s, err := invite.New(ctx, &config.Config{
				Invite: config.InviteConfig{
					KeycloakBaseURL:  srv.URL,
					KeycloakClientID: "admin-cli",
				},
			}, k8s, "password")
			assert.NoError(t, err)

			l := testlogger.New()
			ctx = l.WithContext(t.Context())

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
	ctx := context.WithValue(t.Context(), oauth2.HTTPClient, srv.Client())

	s, err := invite.New(ctx, &config.Config{
		Invite: config.InviteConfig{
			KeycloakBaseURL:  srv.URL,
			KeycloakClientID: "admin-cli",
		},
	}, nil, "password")
	assert.NoError(t, err)

	assert.Equal(t, "Invite", s.GetName())
	assert.Equal(t, []string{}, s.Finalizers())

	res, finalizerErr := s.Finalize(ctx, &v1alpha1.Invite{})
	assert.Nil(t, finalizerErr)
	assert.Equal(t, ctrl.Result{}, res)
}

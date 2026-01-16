// Package keycloak provides Keycloak-specific extensions for Dynamic Client Registration.
package keycloak

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/platform-mesh/security-operator/pkg/clientreg"
)

const maxErrorBodySize = 4096

// AdminClient provides Keycloak admin operations for OIDC Dynamic Client Registration.
// It implements both clientreg.TokenProvider (for initial registration) and clientreg.TokenRefresher
// (for automatic token refresh on 401 responses).
type AdminClient struct {
	httpClient *http.Client
	baseURL    string
	realm      string
}

// NewAdminClient creates a new Keycloak admin client.
// The httpClient should be configured with appropriate authentication (e.g., OAuth2 client credentials).
func NewAdminClient(httpClient *http.Client, baseURL, realm string) *AdminClient {
	return &AdminClient{
		httpClient: httpClient,
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		realm:      realm,
	}
}

// TokenForRegistration implements clientreg.TokenProvider.
// It creates a new initial access token for client registration.
func (c *AdminClient) TokenForRegistration(ctx context.Context) (string, error) {
	url := fmt.Sprintf("%s/admin/realms/%s/clients-initial-access", c.baseURL, c.realm)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader("{}"))
	if err != nil {
		return "", fmt.Errorf("failed to create initial access token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to request initial access token: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return "", readErrorResponse(resp, "create initial access token")
	}

	var response struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("failed to parse initial access token response: %w", err)
	}

	return response.Token, nil
}

// RefreshToken implements clientreg.TokenRefresher.
// It regenerates the registration access token for a client when a 401 is received.
func (c *AdminClient) RefreshToken(ctx context.Context, clientID string) (string, error) {
	clientUUID, err := c.resolveClientUUID(ctx, clientID)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/admin/realms/%s/clients/%s/registration-access-token", c.baseURL, c.realm, clientUUID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create token regeneration request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to request token regeneration: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", readErrorResponse(resp, "regenerate registration access token")
	}

	var response struct {
		RegistrationAccessToken string `json:"registrationAccessToken"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("failed to parse token regeneration response: %w", err)
	}

	return response.RegistrationAccessToken, nil
}

// RegistrationEndpoint returns the OIDC Dynamic Client Registration endpoint for the realm.
func (c *AdminClient) RegistrationEndpoint() string {
	return fmt.Sprintf("%s/realms/%s/clients-registrations/openid-connect", c.baseURL, c.realm)
}

// CreateOrUpdateRealm creates a new realm or updates it if it already exists.
// Returns true if the realm was created, false if it was updated.
func (c *AdminClient) CreateOrUpdateRealm(ctx context.Context, config RealmConfig) (created bool, err error) {
	body, err := json.Marshal(config)
	if err != nil {
		return false, fmt.Errorf("failed to marshal realm config: %w", err)
	}

	createURL := fmt.Sprintf("%s/admin/realms", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, createURL, bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("failed to create realm request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to create realm: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode == http.StatusCreated {
		return true, nil
	}

	if resp.StatusCode == http.StatusConflict {
		return false, c.updateRealm(ctx, config.Realm, body)
	}

	return false, readErrorResponse(resp, "create realm")
}

func (c *AdminClient) updateRealm(ctx context.Context, realmName string, body []byte) error {
	url := fmt.Sprintf("%s/admin/realms/%s", c.baseURL, realmName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create realm update request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to update realm: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return readErrorResponse(resp, "update realm")
	}

	return nil
}

// DeleteRealm deletes a realm. Returns nil if the realm doesn't exist.
func (c *AdminClient) DeleteRealm(ctx context.Context, realmName string) error {
	url := fmt.Sprintf("%s/admin/realms/%s", c.baseURL, realmName)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create realm delete request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete realm: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		return readErrorResponse(resp, "delete realm")
	}

	return nil
}

// GetClientByName finds a client by its name (display name) in the realm.
// Returns nil if the client is not found.
func (c *AdminClient) GetClientByName(ctx context.Context, clientName string) (*ClientInfo, error) {
	clients, err := c.listClients(ctx)
	if err != nil {
		return nil, err
	}

	for _, client := range clients {
		if client.Name == clientName {
			return &client, nil
		}
	}

	return nil, nil
}

func (c *AdminClient) resolveClientUUID(ctx context.Context, clientID string) (string, error) {
	clients, err := c.listClients(ctx)
	if err != nil {
		return "", err
	}

	for _, client := range clients {
		if client.ClientID == clientID {
			return client.ID, nil
		}
	}

	return "", fmt.Errorf("client with client_id %q not found", clientID)
}

func (c *AdminClient) listClients(ctx context.Context) ([]ClientInfo, error) {
	url := fmt.Sprintf("%s/admin/realms/%s/clients", c.baseURL, c.realm)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create get clients request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get clients: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return nil, readErrorResponse(resp, "get clients")
	}

	var clients []ClientInfo
	if err := json.NewDecoder(resp.Body).Decode(&clients); err != nil {
		return nil, fmt.Errorf("failed to parse clients response: %w", err)
	}

	return clients, nil
}

// readErrorResponse reads an error response body and returns a formatted error.
func readErrorResponse(resp *http.Response, operation string) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
	return fmt.Errorf("failed to %s: status %d body: %s", operation, resp.StatusCode, body)
}

// RealmConfig contains the configuration for a Keycloak realm.
type RealmConfig struct {
	Realm                       string      `json:"realm"`
	DisplayName                 string      `json:"displayName,omitempty"`
	Enabled                     bool        `json:"enabled"`
	LoginWithEmailAllowed       bool        `json:"loginWithEmailAllowed,omitempty"`
	RegistrationEmailAsUsername bool        `json:"registrationEmailAsUsername,omitempty"`
	RegistrationAllowed         bool        `json:"registrationAllowed,omitempty"`
	SMTPServer                  *SMTPConfig `json:"smtpServer,omitempty"`
}

// SMTPConfig contains SMTP server configuration for a realm.
type SMTPConfig struct {
	Host     string `json:"host,omitempty"`
	Port     string `json:"port,omitempty"`
	From     string `json:"from,omitempty"`
	SSL      bool   `json:"ssl,omitempty"`
	StartTLS bool   `json:"starttls,omitempty"`
	Auth     bool   `json:"auth,omitempty"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
}

// ClientInfo contains basic information about a Keycloak client.
type ClientInfo struct {
	ID       string `json:"id"`       // Keycloak's internal UUID
	ClientID string `json:"clientId"` // The client_id used in OIDC
	Name     string `json:"name"`     // Display name
}

var (
	_ clientreg.TokenProvider  = (*AdminClient)(nil)
	_ clientreg.TokenRefresher = (*AdminClient)(nil)
)

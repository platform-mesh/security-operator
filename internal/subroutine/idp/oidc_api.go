package idp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/security-operator/api/v1alpha1"
)

type ClientRegistrationRequest struct {
	ClientID                string   `json:"client_id,omitempty"`
	ClientName              string   `json:"client_name,omitempty"`
	RedirectUris            []string `json:"redirect_uris,omitempty"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
}

type clientInfo struct {
	Secret                  string `json:"client_secret,omitempty"`
	RegistrationAccessToken string `json:"registration_access_token,omitempty"`
	RegistrationClientURI   string `json:"registration_client_uri,omitempty"`
	ClientID                string `json:"client_id,omitempty"`
}

func (s *subroutine) registerClient(ctx context.Context, realmName string, clientConfig v1alpha1.IdentityProviderClientConfig, initialAccessToken string, idpConfig *v1alpha1.IdentityProviderConfiguration, log *logger.Logger) (*clientInfo, error) {
	payload := ClientRegistrationRequest{
		ClientName:   clientConfig.ClientName,
		RedirectUris: clientConfig.ValidRedirectURIs,
		GrantTypes:   []string{"authorization_code", "refresh_token"},
	}

	payload.TokenEndpointAuthMethod = "client_secret_basic"

	if clientConfig.ClientType == v1alpha1.IdentityProviderClientTypePublic {
		payload.TokenEndpointAuthMethod = "none"
	}

	body, err := json.Marshal(payload)
	if err != nil { // coverage-ignore
		return nil, fmt.Errorf("failed to marshal client registration request payload: %w", err)
	}

	url := fmt.Sprintf("%s/realms/%s/clients-registrations/openid-connect", s.keycloakBaseURL, realmName)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil { // coverage-ignore
		return nil, fmt.Errorf("failed to build client registration request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", initialAccessToken))

	res, err := s.oidc.Do(req)
	if err != nil { // coverage-ignore
		return nil, fmt.Errorf("client registration call failed: %w", err)
	}
	defer res.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(res.Body)
	if err != nil { // coverage-ignore
		return nil, fmt.Errorf("failed to read response")
	}

	if res.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("client registration failed: status %d body: %s", res.StatusCode, respBody)
	}

	var resp clientInfo
	if err := json.Unmarshal(respBody, &resp); err != nil { // coverage-ignore
		return nil, fmt.Errorf("failed to parse client registration response: %w body: %s", err, respBody)
	}

	return &resp, nil
}

func (s *subroutine) updateClient(ctx context.Context, registrationClientURI string, registrationAccessToken string, clientConfig v1alpha1.IdentityProviderClientConfig, log *logger.Logger) (*clientInfo, error) {
	payload := ClientRegistrationRequest{
		ClientID:                clientConfig.ClientID,
		ClientName:              clientConfig.ClientName,
		RedirectUris:            clientConfig.ValidRedirectURIs,
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		TokenEndpointAuthMethod: "client_secret_basic",
	}

	if clientConfig.ClientType == v1alpha1.IdentityProviderClientTypePublic {
		payload.TokenEndpointAuthMethod = "none"
	}

	body, err := json.Marshal(payload)
	if err != nil { // coverage-ignore
		return nil, fmt.Errorf("failed to marshal DCR update payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, registrationClientURI, bytes.NewBuffer(body))
	if err != nil { // coverage-ignore
		return nil, fmt.Errorf("failed to build DCR update request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", registrationAccessToken))

	res, err := s.oidc.Do(req)
	if err != nil { // coverage-ignore
		return nil, fmt.Errorf("DCR update call failed: %w", err)
	}
	defer res.Body.Close() //nolint:errcheck

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DCR update failed with status %d", res.StatusCode)
	}

	respBody, err := io.ReadAll(res.Body)
	if err != nil { // coverage-ignore
		return nil, fmt.Errorf("failed to read update response: %w", err)
	}

	var resp clientInfo
	if err := json.Unmarshal(respBody, &resp); err != nil { // coverage-ignore
		return nil, fmt.Errorf("failed to parse update response: %w body: %s", err, respBody)
	}

	return &resp, nil
}

func (s *subroutine) getClientInfo(ctx context.Context, realmName, clientID, registrationAccessToken string, log *logger.Logger) (*clientInfo, error) {
	registrationClientURI := fmt.Sprintf("%s/realms/%s/clients-registrations/openid-connect/%s", s.keycloakBaseURL, realmName, clientID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, registrationClientURI, nil)
	if err != nil { // coverage-ignore
		return nil, fmt.Errorf("failed to create DCR GET request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", registrationAccessToken))

	res, err := s.oidc.Do(req)
	if err != nil { // coverage-ignore
		return nil, fmt.Errorf("DCR GET call failed: %w", err)
	}
	defer res.Body.Close() //nolint:errcheck

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DCR GET failed with status %d", res.StatusCode)
	}

	respBody, err := io.ReadAll(res.Body)
	if err != nil { // coverage-ignore
		return nil, fmt.Errorf("failed to read DCR GET response: %w", err)
	}

	var resp clientInfo
	if err := json.Unmarshal(respBody, &resp); err != nil { // coverage-ignore
		return nil, fmt.Errorf("failed to parse DCR GET response: %w body: %s", err, respBody)
	}

	return &resp, nil
}

func (s *subroutine) deleteClient(ctx context.Context, registrationClientURI string, registrationAccessToken string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, registrationClientURI, nil)
	if err != nil { // coverage-ignore
		return fmt.Errorf("failed to build delete request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", registrationAccessToken))

	res, err := s.oidc.Do(req)
	if err != nil { // coverage-ignore
		return fmt.Errorf("client delete call failed: %w", err)
	}
	defer res.Body.Close() //nolint:errcheck

	if res.StatusCode != http.StatusNoContent && res.StatusCode != http.StatusOK {
		return fmt.Errorf("client delete failed with status %d", res.StatusCode)
	}

	return nil
}

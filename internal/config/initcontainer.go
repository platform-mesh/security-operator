package config

import corev1 "k8s.io/api/core/v1"

type InitContainerClientConfig struct {
	Name      string                 `mapstructure:"name" yaml:"name"`
	SecretRef corev1.SecretReference `mapstructure:"secretRef" yaml:"secretRef"`
}

type InitContainerConfiguration struct {
	KeycloakBaseURL  string                      `mapstructure:"keycloak-base-url" yaml:"keycloakBaseURL"`
	KeycloakClientID string                      `mapstructure:"keycloak-client-id" default:"admin-cli" yaml:"keycloakClientID"`
	KeycloakUser     string                      `mapstructure:"keycloak-user" default:"admin" yaml:"keycloakUser"`
	PasswordFile     string                      `mapstructure:"password-file" default:"/secrets/keycloak-password" yaml:"passwordFile"`
	Clients          []InitContainerClientConfig `mapstructure:"clients" yaml:"clients"`
}

type InitContainerConfig struct {
	ConfigFile string `mapstructure:"config-file" default:"/config/config.yaml"`
}

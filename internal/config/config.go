package config

type InviteConfig struct {
	KeycloakBaseURL      string `mapstructure:"invite-keycloak-base-url"`
	KeycloakClientID     string `mapstructure:"invite-keycloak-client-id" default:"security-operator"`
	KeycloakClientSecret string `mapstructure:"invite-keycloak-client-secret"`
}

type InitializerConfig struct {
	WorkspaceInitializerEnabled bool `mapstructure:"initializer-workspace-enabled" default:"true"`
	IDPEnabled                  bool `mapstructure:"initializer-idp-enabled" default:"true"`
	InviteEnabled               bool `mapstructure:"initializer-invite-enabled" default:"true"`
	WorkspaceAuthEnabled        bool `mapstructure:"initializer-workspace-auth-enabled" default:"true"`
}

// Config struct to hold the app config
type Config struct {
	FGA struct {
		Target string `mapstructure:"fga-target"`
	} `mapstructure:",squash"`
	KCP struct {
		Kubeconfig string `mapstructure:"kcp-kubeconfig" default:"/api-kubeconfig/kubeconfig"`
	} `mapstructure:",squash"`
	APIExportEndpointSliceName       string `mapstructure:"api-export-endpoint-slice-name" default:"core.platform-mesh.io"`
	CoreModulePath                   string `mapstructure:"core-module-path"`
	BaseDomain                       string `mapstructure:"base-domain" default:"portal.dev.local:8443"`
	GroupClaim                       string `mapstructure:"group-claim" default:"groups"`
	UserClaim                        string `mapstructure:"user-claim" default:"email"`
	DevelopmentAllowUnverifiedEmails bool   `mapstructure:"development-allow-unverified-emails" default:"false"`
	WorkspacePath                    string `mapstructure:"workspace-path" default:"root"`
	WorkspaceTypeName                string `mapstructure:"workspace-type-name" default:"security"`
	DomainCALookup                   bool   `mapstructure:"domain-ca-lookup" default:"false"`
	MigrateAuthorizationModels       bool   `mapstructure:"migrate-authorization-models" default:"false"`
	HttpClientTimeoutSeconds         int    `mapstructure:"http-client-timeout-seconds" default:"30"`
	SetDefaultPassword               bool   `mapstructure:"set-default-password" default:"false"`
	AllowMemberTuplesEnabled         bool   `mapstructure:"allow-member-tuples-enabled" default:"false"`
	IDP                              struct {
		// SMTP settings
		SMTPServer  string `mapstructure:"idp-smtp-server"`
		SMTPPort    int    `mapstructure:"idp-smtp-port"`
		FromAddress string `mapstructure:"idp-from-address"`

		// SSL settings
		SSL      bool `mapstructure:"idp-smtp-ssl" default:"false"`
		StartTLS bool `mapstructure:"idp-smtp-starttls" default:"false"`

		// Auth settings
		SMTPUser     string `mapstructure:"idp-smtp-user"`
		SMTPPassword string `mapstructure:"idp-smtp-password"`

		AdditionalRedirectURLs    []string `mapstructure:"idp-additional-redirect-urls"`
		KubectlClientRedirectURLs []string `mapstructure:"idp-kubectl-client-redirect-urls" default:"http://localhost:8000,http://localhost:18000"`

		AccessTokenLifespan int  `mapstructure:"idp-access-token-lifespan" default:"28800"`
		RegistrationAllowed bool `mapstructure:"idp-registration-allowed" default:"false"`
	} `mapstructure:",squash"`
	Invite      InviteConfig      `mapstructure:",squash"`
	Initializer InitializerConfig `mapstructure:",squash"`
}

func (config Config) InitializerName() string {
	return config.WorkspacePath + ":" + config.WorkspaceTypeName
}

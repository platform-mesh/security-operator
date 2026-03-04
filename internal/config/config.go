package config

import "github.com/spf13/pflag"

type InviteConfig struct {
	KeycloakBaseURL      string `mapstructure:"invite-keycloak-base-url"`
	KeycloakClientID     string `mapstructure:"invite-keycloak-client-id" default:"security-operator"`
	KeycloakClientSecret string `mapstructure:"invite-keycloak-client-secret"`
}

type WebhooksConfig struct {
	Enabled bool   `mapstructure:"webhooks-enabled" default:"false"`
	Port    int    `mapstructure:"webhooks-port" default:"9443"`
	CertDir string `mapstructure:"webhooks-cert-dir" default:"/tmp/k8s-webhook-server/serving-certs"`
}

type InitializerConfig struct {
	WorkspaceInitializerEnabled bool `mapstructure:"initializer-workspace-enabled" default:"true"`
	IDPEnabled                  bool `mapstructure:"initializer-idp-enabled" default:"true"`
	InviteEnabled               bool `mapstructure:"initializer-invite-enabled" default:"true"`
	WorkspaceAuthEnabled        bool `mapstructure:"initializer-workspace-auth-enabled" default:"true"`
}

type FGAConfig struct {
	Target          string `mapstructure:"fga-target"`
	ObjectType      string `mapstructure:"fga-object-type" default:"core_platform-mesh_io_account"`
	ParentRelation  string `mapstructure:"fga-parent-relation" default:"parent"`
	CreatorRelation string `mapstructure:"fga-creator-relation" default:"owner"`
}

type KCPConfig struct {
	Kubeconfig string `mapstructure:"kcp-kubeconfig" default:"/api-kubeconfig/kubeconfig"`
}

type IDPConfig struct {
	RealmDenyList []string `mapstructure:"idp-realm-deny-list"`

	SMTPServer  string `mapstructure:"idp-smtp-server"`
	SMTPPort    int    `mapstructure:"idp-smtp-port"`
	FromAddress string `mapstructure:"idp-from-address"`

	SSL      bool `mapstructure:"idp-smtp-ssl" default:"false"`
	StartTLS bool `mapstructure:"idp-smtp-starttls" default:"false"`

	SMTPUser     string `mapstructure:"idp-smtp-user"`
	SMTPPassword string `mapstructure:"idp-smtp-password"`

	AdditionalRedirectURLs    []string `mapstructure:"idp-additional-redirect-urls"`
	KubectlClientRedirectURLs []string `mapstructure:"idp-kubectl-client-redirect-urls" default:"http://localhost:8000,http://localhost:18000"`

	AccessTokenLifespan int  `mapstructure:"idp-access-token-lifespan" default:"28800"`
	RegistrationAllowed bool `mapstructure:"idp-registration-allowed" default:"false"`
}

// Config struct to hold the app config
type Config struct {
	FGA                              FGAConfig         `mapstructure:",squash"`
	KCP                              KCPConfig         `mapstructure:",squash"`
	APIExportEndpointSliceName       string            `mapstructure:"api-export-endpoint-slice-name" default:"core.platform-mesh.io"`
	CoreModulePath                   string            `mapstructure:"core-module-path"`
	BaseDomain                       string            `mapstructure:"base-domain" default:"portal.dev.local:8443"`
	GroupClaim                       string            `mapstructure:"group-claim" default:"groups"`
	UserClaim                        string            `mapstructure:"user-claim" default:"email"`
	DevelopmentAllowUnverifiedEmails bool              `mapstructure:"development-allow-unverified-emails" default:"false"`
	WorkspacePath                    string            `mapstructure:"workspace-path" default:"root"`
	WorkspaceTypeName                string            `mapstructure:"workspace-type-name" default:"security"`
	DomainCALookup                   bool              `mapstructure:"domain-ca-lookup" default:"false"`
	MigrateAuthorizationModels       bool              `mapstructure:"migrate-authorization-models" default:"false"`
	HttpClientTimeoutSeconds         int               `mapstructure:"http-client-timeout-seconds" default:"30"`
	SetDefaultPassword               bool              `mapstructure:"set-default-password" default:"false"`
	AllowMemberTuplesEnabled         bool              `mapstructure:"allow-member-tuples-enabled" default:"false"`
	IDP                              IDPConfig         `mapstructure:",squash"`
	Invite                           InviteConfig      `mapstructure:",squash"`
	Initializer                      InitializerConfig `mapstructure:",squash"`
	Webhooks                         WebhooksConfig    `mapstructure:",squash"`
}

func NewConfig() Config {
	return Config{
		FGA: FGAConfig{
			ObjectType:      "core_platform-mesh_io_account",
			ParentRelation:  "parent",
			CreatorRelation: "owner",
		},
		KCP: KCPConfig{
			Kubeconfig: "/api-kubeconfig/kubeconfig",
		},
		APIExportEndpointSliceName: "core.platform-mesh.io",
		BaseDomain:                 "portal.dev.local:8443",
		GroupClaim:                 "groups",
		UserClaim:                  "email",
		WorkspacePath:              "root",
		WorkspaceTypeName:          "security",
		HttpClientTimeoutSeconds:   30,
		IDP: IDPConfig{
			KubectlClientRedirectURLs: []string{"http://localhost:8000", "http://localhost:18000"},
			AccessTokenLifespan:       28800,
		},
		Invite: InviteConfig{
			KeycloakClientID: "security-operator",
		},
		Initializer: InitializerConfig{
			WorkspaceInitializerEnabled: true,
			IDPEnabled:                  true,
			InviteEnabled:               true,
			WorkspaceAuthEnabled:        true,
		},
		Webhooks: WebhooksConfig{
			Port:    9443,
			CertDir: "/tmp/k8s-webhook-server/serving-certs",
		},
	}
}

func (c *Config) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&c.FGA.Target, "fga-target", c.FGA.Target, "Set the OpenFGA API target")
	fs.StringVar(&c.FGA.ObjectType, "fga-object-type", c.FGA.ObjectType, "Set the OpenFGA object type for account tuples")
	fs.StringVar(&c.FGA.ParentRelation, "fga-parent-relation", c.FGA.ParentRelation, "Set the OpenFGA parent relation name")
	fs.StringVar(&c.FGA.CreatorRelation, "fga-creator-relation", c.FGA.CreatorRelation, "Set the OpenFGA creator relation name")
	fs.StringVar(&c.KCP.Kubeconfig, "kcp-kubeconfig", c.KCP.Kubeconfig, "Set the KCP kubeconfig path")
	fs.StringVar(&c.APIExportEndpointSliceName, "api-export-endpoint-slice-name", c.APIExportEndpointSliceName, "Set the APIExportEndpointSlice name")
	fs.StringVar(&c.CoreModulePath, "core-module-path", c.CoreModulePath, "Set the path to the core module FGA model file")
	fs.StringVar(&c.BaseDomain, "base-domain", c.BaseDomain, "Set the base domain used to construct issuer URLs")
	fs.StringVar(&c.GroupClaim, "group-claim", c.GroupClaim, "Set the ID token group claim")
	fs.StringVar(&c.UserClaim, "user-claim", c.UserClaim, "Set the ID token user claim")
	fs.BoolVar(&c.DevelopmentAllowUnverifiedEmails, "development-allow-unverified-emails", c.DevelopmentAllowUnverifiedEmails, "Allow unverified emails in development mode")
	fs.StringVar(&c.WorkspacePath, "workspace-path", c.WorkspacePath, "Set the parent workspace path for created workspaces")
	fs.StringVar(&c.WorkspaceTypeName, "workspace-type-name", c.WorkspaceTypeName, "Set the workspace type name")
	fs.BoolVar(&c.DomainCALookup, "domain-ca-lookup", c.DomainCALookup, "Enable lookup of domain CA from Kubernetes secret")
	fs.BoolVar(&c.MigrateAuthorizationModels, "migrate-authorization-models", c.MigrateAuthorizationModels, "Enable one-time authorization model migration")
	fs.IntVar(&c.HttpClientTimeoutSeconds, "http-client-timeout-seconds", c.HttpClientTimeoutSeconds, "Set HTTP client timeout in seconds")
	fs.BoolVar(&c.SetDefaultPassword, "set-default-password", c.SetDefaultPassword, "Enable setting default password for identity provider users")
	fs.BoolVar(&c.AllowMemberTuplesEnabled, "allow-member-tuples-enabled", c.AllowMemberTuplesEnabled, "Enable allow-member tuples management")
	fs.StringSliceVar(&c.IDP.RealmDenyList, "idp-realm-deny-list", c.IDP.RealmDenyList, "Comma-separated list of Keycloak realms to ignore")
	fs.StringVar(&c.IDP.SMTPServer, "idp-smtp-server", c.IDP.SMTPServer, "Set Keycloak SMTP server host")
	fs.IntVar(&c.IDP.SMTPPort, "idp-smtp-port", c.IDP.SMTPPort, "Set Keycloak SMTP server port")
	fs.StringVar(&c.IDP.FromAddress, "idp-from-address", c.IDP.FromAddress, "Set SMTP from address")
	fs.BoolVar(&c.IDP.SSL, "idp-smtp-ssl", c.IDP.SSL, "Enable SMTP SSL")
	fs.BoolVar(&c.IDP.StartTLS, "idp-smtp-starttls", c.IDP.StartTLS, "Enable SMTP STARTTLS")
	fs.StringVar(&c.IDP.SMTPUser, "idp-smtp-user", c.IDP.SMTPUser, "Set SMTP username")
	fs.StringVar(&c.IDP.SMTPPassword, "idp-smtp-password", c.IDP.SMTPPassword, "Set SMTP password")
	fs.StringSliceVar(&c.IDP.AdditionalRedirectURLs, "idp-additional-redirect-urls", c.IDP.AdditionalRedirectURLs, "Additional redirect URLs for Keycloak clients")
	fs.StringSliceVar(&c.IDP.KubectlClientRedirectURLs, "idp-kubectl-client-redirect-urls", c.IDP.KubectlClientRedirectURLs, "Redirect URLs for the kubectl Keycloak client")
	fs.IntVar(&c.IDP.AccessTokenLifespan, "idp-access-token-lifespan", c.IDP.AccessTokenLifespan, "Keycloak access token lifespan in seconds")
	fs.BoolVar(&c.IDP.RegistrationAllowed, "idp-registration-allowed", c.IDP.RegistrationAllowed, "Enable Keycloak self-registration")
	fs.StringVar(&c.Invite.KeycloakBaseURL, "invite-keycloak-base-url", c.Invite.KeycloakBaseURL, "Set Keycloak base URL for invite flow")
	fs.StringVar(&c.Invite.KeycloakClientID, "invite-keycloak-client-id", c.Invite.KeycloakClientID, "Set Keycloak client ID for invite flow")
	fs.StringVar(&c.Invite.KeycloakClientSecret, "invite-keycloak-client-secret", c.Invite.KeycloakClientSecret, "Set Keycloak client secret for invite flow")
	fs.BoolVar(&c.Initializer.WorkspaceInitializerEnabled, "initializer-workspace-enabled", c.Initializer.WorkspaceInitializerEnabled, "Enable workspace initialization")
	fs.BoolVar(&c.Initializer.IDPEnabled, "initializer-idp-enabled", c.Initializer.IDPEnabled, "Enable IDP initialization")
	fs.BoolVar(&c.Initializer.InviteEnabled, "initializer-invite-enabled", c.Initializer.InviteEnabled, "Enable invite initialization")
	fs.BoolVar(&c.Initializer.WorkspaceAuthEnabled, "initializer-workspace-auth-enabled", c.Initializer.WorkspaceAuthEnabled, "Enable workspace auth initialization")
	fs.BoolVar(&c.Webhooks.Enabled, "webhooks-enabled", c.Webhooks.Enabled, "Enable validating webhooks")
	fs.IntVar(&c.Webhooks.Port, "webhooks-port", c.Webhooks.Port, "Set webhook server port")
	fs.StringVar(&c.Webhooks.CertDir, "webhooks-cert-dir", c.Webhooks.CertDir, "Set webhook certificate directory")
}

func (config Config) InitializerName() string {
	return config.WorkspacePath + ":" + config.WorkspaceTypeName
}

func (config Config) TerminatorName() string {
	return config.WorkspacePath + ":" + config.WorkspaceTypeName
}

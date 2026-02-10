package main

import (
	"errors"
	"fmt"
	"os"

	"sigs.k8s.io/yaml"
)

// kcpBaseContext is the context name that must exist in the kubeconfig.
const kcpBaseContext = "workspace.kcp.io/current"

func main() {
	path := os.Getenv("KUBECONFIG")
	if path == "" {
		panic("KUBECONFIG is empty")
	}

	cfg, err := LoadKubeconfig(path)
	if err != nil {
		panic(fmt.Errorf("loading Kubeconfig from %s: %w", path, err))
	}

	ca, clientCert, clientKey, err := ExtractContextAuth(cfg)
	if err != nil {
		panic(fmt.Errorf("extracting auth values from Kubeconfig: %w", err))
	}

	out := BuildKubeconfig(ca, clientCert, clientKey)
	data, err := yaml.Marshal(out)
	if err != nil {
		panic(fmt.Errorf("building Kubeconfig: %w", err))
	}

	os.Stdout.Write(data)
}

func BuildKubeconfig(ca, clientCert, clientKey string) *Kubeconfig {
	return &Kubeconfig{
		APIVersion:     "v1",
		Kind:           "Config",
		CurrentContext: "root",
		Preferences:    map[string]interface{}{},
		Clusters: []NamedCluster{
			{Name: "root", Cluster: Cluster{Server: "https://localhost:8443/clusters/root", CertificateAuthorityData: ca}},
			{Name: "root-orgs", Cluster: Cluster{Server: "https://localhost:8443/clusters/root:orgs", CertificateAuthorityData: ca}},
			{Name: "root-orgs-chainsaw", Cluster: Cluster{Server: "https://localhost:8443/clusters/root:orgs:chainsaw", CertificateAuthorityData: ca}},
		},
		Contexts: []NamedContext{
			{Name: "root", Context: Context{Cluster: "root", User: "kcp-admin"}},
			{Name: "root-orgs", Context: Context{Cluster: "root-orgs", User: "kcp-admin"}},
			{Name: "root-orgs-chainsaw", Context: Context{Cluster: "root-orgs-chainsaw", User: "kcp-admin"}},
		},
		Users: []NamedUser{
			{Name: "kcp-admin", User: User{ClientCertificateData: clientCert, ClientKeyData: clientKey}},
		},
	}
}

// Kubeconfig represents a standard Kubernetes config file (e.g. admin.kubeconfig).
type Kubeconfig struct {
	APIVersion     string         `json:"apiVersion"`
	Kind           string         `json:"kind"`
	Clusters       []NamedCluster `json:"clusters"`
	Contexts       []NamedContext `json:"contexts"`
	CurrentContext string         `json:"current-context"`
	Preferences    interface{}    `json:"preferences,omitempty"`
	Users          []NamedUser    `json:"users"`
}

// NamedCluster is a cluster entry with a name.
type NamedCluster struct {
	Name    string  `json:"name"`
	Cluster Cluster `json:"cluster"`
}

// Cluster holds cluster connection details.
type Cluster struct {
	Server                   string `json:"server"`
	CertificateAuthorityData string `json:"certificate-authority-data"`
}

// NamedContext is a context entry with a name.
type NamedContext struct {
	Name    string  `json:"name"`
	Context Context `json:"context"`
}

// Context ties a cluster to a user.
type Context struct {
	Cluster string `json:"cluster"`
	User    string `json:"user"`
}

// NamedUser is a user entry with a name.
type NamedUser struct {
	Name string `json:"name"`
	User User   `json:"user"`
}

// User holds user authentication data (client cert).
type User struct {
	ClientCertificateData string `json:"client-certificate-data"`
	ClientKeyData         string `json:"client-key-data"`
}

// LoadKubeconfig reads and parses a kubeconfig file from path.
func LoadKubeconfig(path string) (*Kubeconfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Kubeconfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ExtractContextAuth finds the context named kcpBaseContext in cfg, and returns
// the CA, client-certificate-data, and client-key-data (base64) for that context.
// Returns an error if cfg is nil, the context is missing, or the referenced
// cluster/user are not found.
func ExtractContextAuth(cfg *Kubeconfig) (ca, clientCertData, clientKeyData string, err error) {
	if cfg == nil {
		return "", "", "", errors.New("kubeconfig is nil")
	}
	var ctx *Context
	for i := range cfg.Contexts {
		if cfg.Contexts[i].Name == kcpBaseContext {
			ctx = &cfg.Contexts[i].Context
			break
		}
	}
	if ctx == nil {
		return "", "", "", fmt.Errorf("context %q not found in kubeconfig", kcpBaseContext)
	}

	var cluster *Cluster
	for i := range cfg.Clusters {
		if cfg.Clusters[i].Name == ctx.Cluster {
			cluster = &cfg.Clusters[i].Cluster
			break
		}
	}
	if cluster == nil {
		return "", "", "", fmt.Errorf("cluster %q not found in kubeconfig", ctx.Cluster)
	}

	var auth *User
	for i := range cfg.Users {
		if cfg.Users[i].Name == ctx.User {
			auth = &cfg.Users[i].User
			break
		}
	}
	if auth == nil {
		return "", "", "", fmt.Errorf("user %q not found in kubeconfig", ctx.User)
	}
	if auth.ClientCertificateData == "" || auth.ClientKeyData == "" {
		return "", "", "", errors.New("context user has no client-certificate-data or client-key-data")
	}
	return cluster.CertificateAuthorityData, auth.ClientCertificateData, auth.ClientKeyData, nil
}

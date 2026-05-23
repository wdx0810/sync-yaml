package k8s

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
	"k8s.io/client-go/discovery"
	k8sdynamic "k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type kubeConfig struct {
	CurrentContext string         `yaml:"current-context"`
	Clusters       []kubeCluster `yaml:"clusters"`
	Contexts       []kubeContext  `yaml:"contexts"`
	Users          []kubeUser    `yaml:"users"`
}

type kubeCluster struct {
	Name    string `yaml:"name"`
	Cluster struct {
		Server string `yaml:"server"`
	} `yaml:"cluster"`
}

type kubeContext struct {
	Name    string `yaml:"name"`
	Context struct {
		Cluster string `yaml:"cluster"`
		User    string `yaml:"user"`
	} `yaml:"context"`
}

type kubeUser struct {
	Name string `yaml:"name"`
	User struct {
		Token string `yaml:"token"`
	} `yaml:"user"`
}

func parseKubeconfigManual(content string) (kubernetes.Interface, error) {
	var kc kubeConfig
	if err := yaml.Unmarshal([]byte(content), &kc); err != nil {
		return nil, fmt.Errorf("failed to parse kubeconfig YAML: %w", err)
	}

	if kc.CurrentContext == "" && len(kc.Contexts) > 0 {
		kc.CurrentContext = kc.Contexts[0].Name
	}
	if kc.CurrentContext == "" {
		return nil, fmt.Errorf("no current-context in kubeconfig")
	}

	var clusterName, userName string
	for _, ctx := range kc.Contexts {
		if ctx.Name == kc.CurrentContext {
			clusterName = ctx.Context.Cluster
			userName = ctx.Context.User
			break
		}
	}
	if clusterName == "" {
		return nil, fmt.Errorf("context %q not found", kc.CurrentContext)
	}

	var server string
	for _, c := range kc.Clusters {
		if c.Name == clusterName {
			server = c.Cluster.Server
			break
		}
	}
	if server == "" {
		return nil, fmt.Errorf("cluster %q not found", clusterName)
	}

	var token string
	for _, u := range kc.Users {
		if u.Name == userName {
			token = u.User.Token
			break
		}
	}

	config := &rest.Config{
		Host: server,
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: true,
		},
	}

	if token != "" {
		config.BearerToken = token
	}

	// Extract base64 data fields directly from raw text to avoid YAML parser corruption.
	caBytes := extractAndDecodeBase64Field(content, "certificate-authority-data")
	certBytes := extractAndDecodeBase64Field(content, "client-certificate-data")
	keyBytes := extractAndDecodeBase64Field(content, "client-key-data")

	if caBytes != nil {
		config.TLSClientConfig.CAData = caBytes
		config.TLSClientConfig.Insecure = false
	}
	if certBytes != nil && keyBytes != nil {
		config.TLSClientConfig.CertData = certBytes
		config.TLSClientConfig.KeyData = keyBytes
	}

	return kubernetes.NewForConfig(config)
}

// extractAndDecodeBase64Field extracts a base64 value for a given YAML key
// directly from raw text, collecting multi-line continuations.
func extractAndDecodeBase64Field(content, fieldName string) []byte {
	// Find the field in the raw text.
	re := regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(fieldName) + `:\s*(.*)$`)
	match := re.FindStringSubmatchIndex(content)
	if match == nil {
		return nil
	}

	// Get the first line value.
	value := strings.TrimSpace(content[match[2]:match[3]])

	// Collect continuation lines (indented lines that look like base64).
	lines := strings.Split(content[match[3]:], "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			break
		}
		// If line starts with a YAML key (contains ":"), stop.
		if strings.Contains(trimmed, ":") && !isBase64Char(trimmed[0]) {
			break
		}
		// Check if it looks like base64 continuation.
		if isAllBase64(trimmed) {
			value += trimmed
		} else {
			break
		}
	}

	// Remove any remaining whitespace.
	value = strings.ReplaceAll(value, " ", "")
	value = strings.ReplaceAll(value, "\t", "")

	// Try decoding.
	if data, err := base64.StdEncoding.DecodeString(value); err == nil {
		return data
	}
	if data, err := base64.RawStdEncoding.DecodeString(value); err == nil {
		return data
	}
	return nil
}

func isBase64Char(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' || c == '='
}

func isAllBase64(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isBase64Char(s[i]) {
			return false
		}
	}
	return len(s) > 0
}

// ParseKubeconfigForDynamic parses kubeconfig and returns a dynamic.Interface.
// Exported for use by GenericSyncer.
func ParseKubeconfigForDynamic(content string) (k8sdynamic.Interface, error) {
	config, err := buildRestConfig(content)
	if err != nil {
		return nil, err
	}
	return k8sdynamic.NewForConfig(config)
}

// ParseKubeconfigForDiscovery parses kubeconfig and returns a discovery client
// suitable for resolving GVRs of cluster-installed CRDs.
func ParseKubeconfigForDiscovery(content string) (discovery.DiscoveryInterface, error) {
	config, err := buildRestConfig(content)
	if err != nil {
		return nil, err
	}
	return discovery.NewDiscoveryClientForConfig(config)
}

// buildRestConfig parses a kubeconfig YAML string into a *rest.Config.
func buildRestConfig(content string) (*rest.Config, error) {
	var kc kubeConfig
	if err := yaml.Unmarshal([]byte(content), &kc); err != nil {
		return nil, fmt.Errorf("failed to parse kubeconfig YAML: %w", err)
	}

	if kc.CurrentContext == "" && len(kc.Contexts) > 0 {
		kc.CurrentContext = kc.Contexts[0].Name
	}

	var clusterName, userName string
	for _, ctx := range kc.Contexts {
		if ctx.Name == kc.CurrentContext {
			clusterName = ctx.Context.Cluster
			userName = ctx.Context.User
			break
		}
	}

	var server string
	for _, c := range kc.Clusters {
		if c.Name == clusterName {
			server = c.Cluster.Server
			break
		}
	}
	if server == "" {
		return nil, fmt.Errorf("cluster not found in kubeconfig")
	}

	var token string
	for _, u := range kc.Users {
		if u.Name == userName {
			token = u.User.Token
			break
		}
	}

	config := &rest.Config{
		Host:        server,
		BearerToken: token,
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: true,
		},
	}

	// Try to decode client certs.
	certData := extractAndDecodeBase64Field(content, "client-certificate-data")
	keyData := extractAndDecodeBase64Field(content, "client-key-data")
	caData := extractAndDecodeBase64Field(content, "certificate-authority-data")
	if certData != nil && keyData != nil {
		config.TLSClientConfig.CertData = certData
		config.TLSClientConfig.KeyData = keyData
		if caData != nil {
			config.TLSClientConfig.CAData = caData
			config.TLSClientConfig.Insecure = false
		}
	}

	return config, nil
}

package k8s

import (
	"fmt"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Option customizes how New resolves the Kubernetes client config for a Backend.
// With no options New preserves the historical behavior: prefer in-cluster
// config, falling back to the default kubeconfig loading rules (KUBECONFIG /
// ~/.kube/config). The options let a programmatic caller target a specific
// cluster without relying on ambient environment.
type Option func(*newConfig)

// newConfig collects the resolved-config inputs from the options.
type newConfig struct {
	kubeconfigPath string
	contextName    string
	restConfig     *rest.Config
}

// WithKubeconfig sets an explicit kubeconfig file path (overriding $KUBECONFIG
// and ~/.kube/config). Supplying it signals out-of-cluster intent, so the
// in-cluster config probe is skipped.
func WithKubeconfig(path string) Option {
	return func(c *newConfig) { c.kubeconfigPath = path }
}

// WithContext selects a named context from the kubeconfig (overriding the
// current-context). Like WithKubeconfig it skips the in-cluster probe.
func WithContext(name string) Option {
	return func(c *newConfig) { c.contextName = name }
}

// WithRESTConfig injects an already-built *rest.Config (e.g. one the caller
// authenticated itself), bypassing kubeconfig loading entirely. Highest
// precedence.
func WithRESTConfig(rc *rest.Config) Option {
	return func(c *newConfig) { c.restConfig = rc }
}

// resolve builds the *rest.Config implied by the options.
func (c newConfig) resolve() (*rest.Config, error) {
	if c.restConfig != nil {
		return c.restConfig, nil
	}
	// No explicit kubeconfig/context: prefer in-cluster (historical default).
	if c.kubeconfigPath == "" && c.contextName == "" {
		if rc, err := rest.InClusterConfig(); err == nil {
			return rc, nil
		}
	}
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if c.kubeconfigPath != "" {
		loadingRules.ExplicitPath = c.kubeconfigPath
	}
	overrides := &clientcmd.ConfigOverrides{}
	if c.contextName != "" {
		overrides.CurrentContext = c.contextName
	}
	rc, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("k8s: load kubeconfig: %w", err)
	}
	return rc, nil
}

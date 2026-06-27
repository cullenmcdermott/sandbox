package k8s

import "context"

// Ping checks that the cluster API server is reachable, returning nil if a
// lightweight /healthz probe succeeds. It is used by `sandbox auth status` to
// render the cluster-connection light without creating any resources. The
// caller controls the timeout via ctx.
func (b *Backend) Ping(ctx context.Context) error {
	_, err := b.core.Discovery().RESTClient().Get().AbsPath("/healthz").DoRaw(ctx)
	return err
}

// Namespace returns the namespace this backend addresses.
func (b *Backend) Namespace() string { return b.namespace }

// Host returns the cluster API server URL from the loaded kubeconfig (empty when
// the backend was built from pre-supplied clients without a rest.Config).
func (b *Backend) Host() string {
	if b.config == nil {
		return ""
	}
	return b.config.Host
}

package k8s

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"

	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// forwardHandle implements session.ForwardHandle for a kubectl-equivalent
// port-forward managed by client-go.
type forwardHandle struct {
	localPort int
	done      chan struct{}
	err       error
	cancel    context.CancelFunc
}

func (h *forwardHandle) LocalPort() int      { return h.localPort }
func (h *forwardHandle) Close() error        { h.cancel(); return nil }
func (h *forwardHandle) Done() <-chan struct{} { return h.done }

// PortForward establishes port-forwards to the session's runner pod. Each
// PortSpec maps a local port to a remote pod port. If Local is 0, a random
// free local port is chosen.
func (b *Backend) PortForward(ctx context.Context, ref session.Ref, ports []session.PortSpec) ([]session.ForwardHandle, error) {
	sb, err := b.agents.AgentsV1alpha1().Sandboxes(b.namespace).Get(ctx, string(ref.ID), metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("k8s: get Sandbox %s for port-forward: %w", ref.ID, err)
	}
	pod, err := b.getPodForSandbox(ctx, sb)
	if err != nil || pod == nil {
		return nil, fmt.Errorf("k8s: no pod found for sandbox %s", ref.ID)
	}

	handles := make([]session.ForwardHandle, 0, len(ports))
	for _, ps := range ports {
		h, err := b.forwardPort(ctx, pod, ps)
		if err != nil {
			// Close already-established forwards before returning the error.
			for _, h := range handles {
				h.Close()
			}
			return nil, fmt.Errorf("k8s: port-forward %d->%d: %w", ps.Local, ps.Remote, err)
		}
		handles = append(handles, h)
	}
	return handles, nil
}

func (b *Backend) forwardPort(ctx context.Context, pod *corev1.Pod, ps session.PortSpec) (*forwardHandle, error) {
	localPort := ps.Local
	if localPort == 0 {
		p, err := freePort()
		if err != nil {
			return nil, err
		}
		localPort = p
	}

	fwdCtx, cancel := context.WithCancel(ctx)
	h := &forwardHandle{
		localPort: localPort,
		done:      make(chan struct{}),
		cancel:    cancel,
	}

	go b.runForward(fwdCtx, pod, localPort, ps.Remote, h)

	// Wait briefly for the forward to establish.
	select {
	case <-h.done:
		return nil, h.err
	case <-time.After(3 * time.Second):
		// Forward is likely up; return. If it fails later, done closes.
	}

	return h, nil
}

func (b *Backend) runForward(ctx context.Context, pod *corev1.Pod, localPort, remotePort int, h *forwardHandle) {
	defer close(h.done)

	// Build the SPDY transport from the rest config.
	transport, upgrader, err := spdy.RoundTripperFor(b.config)
	if err != nil {
		h.err = fmt.Errorf("spdy transport: %w", err)
		return
	}

	u, err := url.Parse(b.restURLForPodPortForward(pod))
	if err != nil {
		h.err = fmt.Errorf("parse port-forward URL: %w", err)
		return
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", u)

	ports := []string{fmt.Sprintf("%d:%d", localPort, remotePort)}
	ready := make(chan struct{}, 1)

	pf, err := portforward.New(dialer, ports, ctx.Done(), ready, nil, nil)
	if err != nil {
		h.err = fmt.Errorf("create port-forward: %w", err)
		return
	}

	err = pf.ForwardPorts()
	if err != nil {
		h.err = err
	}
}

// restURLForPodPortForward builds the port-forward URL for a pod.
func (b *Backend) restURLForPodPortForward(pod *corev1.Pod) string {
	return b.config.Host + "/api/v1/namespaces/" + pod.Namespace + "/pods/" + pod.Name + "/portforward"
}

// freePort returns a random free TCP port on localhost.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	addr := l.Addr().(*net.TCPAddr)
	return addr.Port, nil
}

// Ensure compile-time that forwardHandle satisfies the interface.
var _ session.ForwardHandle = (*forwardHandle)(nil)

// RunnerPort returns the runner HTTP port constant for callers that need it.
func RunnerPort() int { return portRunner }

// SSHPort returns the SSH port constant for callers that need it.
func SSHPort() int { return portSSH }

// ForwardSpecs is a helper that builds the standard port-forward specs for
// a session: runner HTTP and SSH.
func ForwardSpecs(httpLocal, sshLocal int) []session.PortSpec {
	return []session.PortSpec{
		{Local: httpLocal, Remote: portRunner},
		{Local: sshLocal, Remote: portSSH},
	}
}

// Ensure the Backend satisfies the session.Backend interface.
var _ session.Backend = (*Backend)(nil)

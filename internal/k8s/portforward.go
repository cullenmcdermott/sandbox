package k8s

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
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

func (h *forwardHandle) LocalPort() int        { return h.localPort }
func (h *forwardHandle) Close() error          { h.cancel(); return nil }
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

	// The forward's lifetime is governed by the handle's Close(), not by the
	// caller's ctx. Callers pass a bounded/timeout ctx for connect-time setup
	// (Status/Resume/health), but the established forward must outlive that
	// call: e.g. the dashboard connects under a 120s timeout context and then
	// cancels it, yet keeps using the forward for the whole attached session.
	// Rooting the goroutine at context.Background() and tearing it down only via
	// cancel()/Close() decouples the two. The caller's ctx is still honored
	// during establishment below.
	fwdCtx, cancel := context.WithCancel(context.Background())
	h := &forwardHandle{
		localPort: localPort,
		done:      make(chan struct{}),
		cancel:    cancel,
	}

	// client-go closes `ready` when the port-forward is actually listening.
	// Waiting on it (with a timeout) is faster and more correct than a fixed
	// 3s sleep (S4).
	ready := make(chan struct{}, 1)
	go b.runForwardFn(fwdCtx, pod, localPort, ps.Remote, h, ready)

	// Wait for the forward to establish, respecting caller cancellation.
	select {
	case <-ready:
		// Forward is confirmed listening.
	case <-h.done:
		return nil, h.err
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	case <-time.After(5 * time.Second):
		cancel()
		return nil, fmt.Errorf("port-forward %d→%d did not become ready within 5s", localPort, ps.Remote)
	}

	return h, nil
}

// forwardReconnectBackoff bounds the wait between reconnect attempts after a
// forward drops (pod restart, SPDY stream death, transient API-server blip).
var (
	forwardReconnectBackoffInitial = 500 * time.Millisecond
	forwardReconnectBackoffMax     = 10 * time.Second
)

// runForward establishes and maintains a port-forward for the lifetime of ctx.
// forwardOnce (client-go's pf.ForwardPorts()) blocks until the forward dies; if
// it exits while ctx is still live (pod rescheduled, SPDY stream dropped, a
// transient API-server blip) we re-resolve the pod and reconnect with capped
// exponential backoff so a transient drop doesn't permanently break an attached
// session. The loop reconnects indefinitely; h.done closes — and h.err carries
// the last error — only when ctx is cancelled, i.e. Close(). The local port is
// preserved across reconnects, so existing client connections re-dial the same
// address.
//
// The caller's `ready` is closed exactly once, on the FIRST successful
// establishment, regardless of which attempt achieves it. Each forwardOnce gets
// its own channel (client-go closes the one it is given, and a closed channel
// can't be reused), and a per-attempt watcher — bounded by iterDone so a failed
// attempt leaks no goroutine — propagates the first listen to `ready`. This is
// what lets a transient FIRST-attempt failure that recovers on retry still
// unblock forwardPort, instead of stranding it until the establishment timeout
// fires (and then tearing the recovered forward down). h.err is internal: it is
// read only by forwardPort via the `<-h.done` branch, not exposed on the
// session.ForwardHandle interface.
func (b *Backend) runForward(ctx context.Context, pod *corev1.Pod, localPort, remotePort int, h *forwardHandle, ready chan struct{}) {
	defer close(h.done)

	var readyOnce sync.Once
	signalReady := func() { readyOnce.Do(func() { close(ready) }) }

	backoff := forwardReconnectBackoffInitial
	first := true
	for {
		if !first {
			// Re-resolve the pod: after a restart the old pod name is gone, so a
			// reconnect must target whatever pod now backs the session.
			if rp, err := b.resolvePodForForward(ctx, pod); err == nil && rp != nil {
				pod = rp
			}
		}

		iterReady := make(chan struct{})
		iterDone := make(chan struct{})
		go func() {
			select {
			case <-iterReady:
				signalReady()
			case <-iterDone:
				// Attempt ended; honor a listen that raced the exit.
				select {
				case <-iterReady:
					signalReady()
				default:
				}
			case <-ctx.Done():
			}
		}()

		err := b.forwardOnceFn(ctx, pod, localPort, remotePort, iterReady)
		close(iterDone)

		// A cancelled context is an intentional teardown (Close()), not a failure.
		if ctx.Err() != nil {
			h.err = ctx.Err()
			return
		}
		if err != nil {
			h.err = err
		} else {
			// A clean exit with a live ctx still means the forward is no longer
			// carrying traffic; record why so the next attempt's reason is clear.
			h.err = fmt.Errorf("port-forward %d→%d exited unexpectedly", localPort, remotePort)
		}

		first = false
		// Wait out the backoff, but bail immediately if the caller cancels.
		select {
		case <-ctx.Done():
			h.err = ctx.Err()
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > forwardReconnectBackoffMax {
			backoff = forwardReconnectBackoffMax
		}
	}
}

// forwardOnce runs a single ForwardPorts() session and returns when it exits.
func (b *Backend) forwardOnce(ctx context.Context, pod *corev1.Pod, localPort, remotePort int, ready chan struct{}) error {
	// Build the SPDY transport from the rest config.
	transport, upgrader, err := spdy.RoundTripperFor(b.config)
	if err != nil {
		return fmt.Errorf("spdy transport: %w", err)
	}

	u := b.restURLForPodPortForward(pod)

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", u)

	ports := []string{fmt.Sprintf("%d:%d", localPort, remotePort)}

	pf, err := portforward.New(dialer, ports, ctx.Done(), ready, nil, nil)
	if err != nil {
		return fmt.Errorf("create port-forward: %w", err)
	}

	return pf.ForwardPorts()
}

// resolvePodForForward re-finds the pod backing the same session as the given
// pod, used to retarget a port-forward after the pod is rescheduled. It relies
// on the session-id label the Sandbox stamps onto its pod template.
func (b *Backend) resolvePodForForward(ctx context.Context, old *corev1.Pod) (*corev1.Pod, error) {
	sid := old.Labels[labelSessionID]
	if sid == "" {
		return old, nil // can't re-resolve without the label; reuse the old pod
	}
	selector := fmt.Sprintf("%s=%s", labelSessionID, sid)
	pods, err := b.core.CoreV1().Pods(old.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, err
	}
	for i := range pods.Items {
		if pods.Items[i].DeletionTimestamp == nil {
			return &pods.Items[i], nil
		}
	}
	return nil, fmt.Errorf("no live pod for session %s", sid)
}

// restURLForPodPortForward builds the port-forward URL for a pod. It uses the
// REST client's request builder rather than string concatenation so the host's
// base path is joined correctly — e.g. a kubeconfig server URL with a trailing
// slash (as some API-server proxies emit) would otherwise yield a double-slash
// path the API server 404s.
func (b *Backend) restURLForPodPortForward(pod *corev1.Pod) *url.URL {
	return b.core.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(pod.Namespace).
		Name(pod.Name).
		SubResource("portforward").
		URL()
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

// OpencodePort returns the `opencode serve` port constant for callers that
// need it (opencode-server sessions).
func OpencodePort() int { return portOpencode }

// ForwardSpecs is a helper that builds the standard port-forward specs for
// a session: runner HTTP and SSH.
func ForwardSpecs(httpLocal, sshLocal int) []session.PortSpec {
	return []session.PortSpec{
		{Local: httpLocal, Remote: portRunner},
		{Local: sshLocal, Remote: portSSH},
	}
}

// ForwardSpecsRunnerOnly forwards just the runner HTTP port. Used by the
// dashboard's background status observers (and any read-only attach that never
// runs mutagen sync), so the SSH forward — needed only for sync — is not opened.
// Halves the SPDY stream count per backgrounded session at launch.
func ForwardSpecsRunnerOnly(httpLocal int) []session.PortSpec {
	return []session.PortSpec{
		{Local: httpLocal, Remote: portRunner},
	}
}

// ForwardSpecsWithOpencode is ForwardSpecs plus the opencode serve port. Used
// for opencode-server sessions where the local `opencode attach` client needs a
// forward to port 4096. The returned handles are ordered HTTP, SSH, opencode.
func ForwardSpecsWithOpencode(httpLocal, sshLocal, opencodeLocal int) []session.PortSpec {
	return append(ForwardSpecs(httpLocal, sshLocal), session.PortSpec{Local: opencodeLocal, Remote: portOpencode})
}

// Ensure the Backend satisfies the session.Backend interface.
var _ session.Backend = (*Backend)(nil)

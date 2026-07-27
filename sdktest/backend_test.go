package sdktest

// backend_test.go — the compile-time + behavioral pin for client.Backend's new
// external-implementability contract (see the header note in surface_test.go).
// fakeBackend is a full, from-scratch implementation of client.Backend built in
// this SEPARATE module, using only types exported from client — proving the
// interface no longer requires naming an internal/... type. The behavioral test
// below (TestDialRunnerRoutesByName) is the actual point of the change: it
// proves a caller reaches the handle keyed PortRunner in the returned
// client.Forwards, never a same-call sibling at a different port, regardless of
// map iteration order.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/cullenmcdermott/sandbox/client"
)

// --- compile pins: the new named-forward surface --------------------------

var (
	_ func(...client.PortName) []client.PortSpec = client.Forward

	// Forwards' method set, pinned as method expressions so a retyped or
	// dropped method fails to compile here rather than downstream.
	_ func(client.Forwards, client.PortName) (client.ForwardHandle, bool) = client.Forwards.Get
	_ func(client.Forwards, client.PortName) (int, bool)                  = client.Forwards.LocalPort
	_ func(client.Forwards)                                               = client.Forwards.Close

	// Named endpoints + their standard in-pod ports.
	_ client.PortName = client.PortRunner
	_ client.PortName = client.PortSSH
	_ client.PortName = client.PortOpencode
	_ client.PortName = client.PortCodex

	_ int = client.RunnerPort
	_ int = client.SSHPort
	_ int = client.OpencodePort
	_ int = client.CodexPort
)

// TestForwardSpecs pins client.Forward's contract: one spec per requested name,
// each carrying its Name, the endpoint's standard Remote port, and an
// OS-assigned (0) Local port.
func TestForwardSpecs(t *testing.T) {
	got := client.Forward(client.PortRunner, client.PortSSH)
	want := []client.PortSpec{
		{Name: client.PortRunner, Local: 0, Remote: client.RunnerPort},
		{Name: client.PortSSH, Local: 0, Remote: client.SSHPort},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Forward(PortRunner, PortSSH) = %+v, want %+v", got, want)
	}
}

// --- fakeBackend: a from-scratch, external implementation of client.Backend ---

// fakeForwardHandle proves client.ForwardHandle is implementable outside the
// main module: nothing in the interface names an internal type.
type fakeForwardHandle struct {
	port int
	done chan struct{}
}

func (h *fakeForwardHandle) LocalPort() int        { return h.port }
func (h *fakeForwardHandle) Close() error          { return nil }
func (h *fakeForwardHandle) Done() <-chan struct{} { return h.done }

var _ client.ForwardHandle = (*fakeForwardHandle)(nil)

// fakeBackend is a minimal, entirely external client.Backend: every field it
// touches is a type exported from client (Ref, Spec, State, StateEvent,
// ResumeOptions, PortSpec, Forwards, ReaperOptions), so this compiles in a
// module that has never seen internal/k8s or internal/session.
type fakeBackend struct {
	forwards client.Forwards
	token    string

	gotPorts []client.PortSpec
}

func (f *fakeBackend) Namespace() string { return "fake-namespace" }

func (f *fakeBackend) CreateSession(context.Context, client.Spec) (client.Ref, error) {
	return client.Ref{}, nil
}

func (f *fakeBackend) Status(context.Context, client.Ref) (client.State, error) {
	return client.State{}, nil
}

func (f *fakeBackend) List(context.Context) ([]client.State, error) { return nil, nil }

func (f *fakeBackend) Watch(context.Context) (<-chan client.StateEvent, error) {
	return nil, nil
}

func (f *fakeBackend) Suspend(context.Context, client.Ref) error { return nil }

func (f *fakeBackend) Resume(context.Context, client.Ref, client.ResumeOptions) error {
	return nil
}

func (f *fakeBackend) Destroy(context.Context, client.Ref) error { return nil }

func (f *fakeBackend) StartWithProgress(context.Context, client.Ref, func(string)) error {
	return nil
}

func (f *fakeBackend) PortForward(_ context.Context, _ client.Ref, ports []client.PortSpec) (client.Forwards, error) {
	f.gotPorts = ports
	return f.forwards, nil
}

func (f *fakeBackend) RunnerToken(context.Context, client.Ref) (string, error) {
	return f.token, nil
}

func (f *fakeBackend) OpencodePassword(context.Context, client.Ref) (string, error) {
	return "", nil
}

func (f *fakeBackend) EnsureReaper(context.Context, client.Ref, client.ReaperOptions) error {
	return nil
}

// This compile-time pin IS the point of the change: client.Backend is fully
// implementable in an external module, using only exported client types.
var _ client.Backend = (*fakeBackend)(nil)

// TestDialRunnerRoutesByName is the behavioral proof that a caller reaches the
// handle keyed PortRunner in the Forwards a Backend.PortForward returns, never
// a same-call sibling published at a different local port — the whole reason
// PortForward returns a name-keyed map instead of a positionally-ordered slice.
//
// The fake's PortRunner handle points at a real httptest server that answers
// /healthz; a decoy PortSSH handle (a different name, on a different local
// port) points at a second server that always 500s. If DialRunner ever dialed
// the decoy instead of the named PortRunner entry, Health would report the
// decoy's error instead of succeeding.
func TestDialRunnerRoutesByName(t *testing.T) {
	real := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok","protocolVersion":1}`)
	}))
	defer real.Close()

	decoy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "wrong port reached", http.StatusInternalServerError)
	}))
	defer decoy.Close()

	realPort := serverPort(t, real)
	decoyPort := serverPort(t, decoy)
	if realPort == decoyPort {
		t.Fatalf("test setup: real and decoy servers landed on the same port %d", realPort)
	}

	be := &fakeBackend{
		token: "tok",
		forwards: client.Forwards{
			client.PortRunner: &fakeForwardHandle{port: realPort, done: make(chan struct{})},
			// Decoy: a different name, a different port. A position-indexed
			// ("first handle wins") caller would dial this one first in map
			// iteration order about half the time; a name-keyed lookup never does.
			client.PortSSH: &fakeForwardHandle{port: decoyPort, done: make(chan struct{})},
		},
	}

	c, err := client.New(client.WithBackend(be), client.WithStateDir(t.TempDir()))
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	rc, cleanup, err := c.DialRunner(context.Background(), client.Ref{ID: "sess-1"})
	if err != nil {
		t.Fatalf("DialRunner: %v", err)
	}
	defer cleanup()

	// DialRunner only ever needs the runner endpoint: PortForward must have been
	// called with a spec named PortRunner (never PortSSH — that would open an
	// unused SPDY stream for a one-shot runner call).
	foundRunner := false
	for _, ps := range be.gotPorts {
		if ps.Name == client.PortSSH {
			t.Errorf("DialRunner requested a PortSSH forward it never uses: %+v", be.gotPorts)
		}
		if ps.Name == client.PortRunner {
			foundRunner = true
		}
	}
	if !foundRunner {
		t.Fatalf("DialRunner never requested a PortRunner forward: %+v", be.gotPorts)
	}

	if err := rc.Health(context.Background()); err != nil {
		t.Fatalf("Health reached the decoy (or neither) instead of the PortRunner handle's real server: %v", err)
	}
}

// serverPort extracts the TCP port an httptest.Server is actually listening on.
func serverPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	addr, ok := srv.Listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("httptest server listener is not TCP: %v", srv.Listener.Addr())
	}
	return addr.Port
}

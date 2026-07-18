package client

import (
	"context"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// orchestration_test.go — fake-backed unit tests for the Client orchestration
// paths (Create / Status / List / Suspend / Resume / Destroy / DialRunner).
// They exercise the narrowed client.Backend seam: a fake backend + a fake
// Mutagen runner stand in for the cluster and mutagen CLI, so the sequencing
// and error handling of these methods can be pinned without a live cluster.
//
// The Destroy call-order spy (TestDestroyStopsSyncBeforeClusterDestroy) is the
// regression net for §8's Destroy reorder: sync stop must precede the cluster
// destroy, or mutagen sessions orphan against a dead pod.

// fakeBackend implements client.Backend. Each method optionally records itself
// into *order (shared with the fake sync runner so a test can pin cross-seam
// call ordering) and returns the pre-seeded result/error for that method.
type fakeBackend struct {
	order *[]string

	// results / errors, keyed by method
	createErr   error
	statusState State
	statusErr   error
	listStates  []State
	listErr     error
	suspendErr  error
	resumeErr   error
	destroyErr  error
	handles     []session.ForwardHandle
	portErr     error
	// portForwardHook, if set, runs at the start of PortForward — a test seam to
	// stall Connect mid-flight (e.g. to race Close against it).
	portForwardHook func()
	token           string
	tokenErr        error

	// captured inputs
	gotSpec  Spec
	gotSpecs []session.PortSpec
	gotRefs  map[string][]Ref
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{gotRefs: map[string][]Ref{}}
}

func (f *fakeBackend) record(method string, ref Ref) {
	if f.order != nil {
		*f.order = append(*f.order, method)
	}
	f.gotRefs[method] = append(f.gotRefs[method], ref)
}

func (f *fakeBackend) Namespace() string { return "agent-sessions" }

func (f *fakeBackend) CreateSession(_ context.Context, spec Spec) (Ref, error) {
	f.gotSpec = spec
	f.record("create", Ref{ID: spec.ID})
	// The real backend echoes the spec's id back in the ref.
	return Ref{ID: spec.ID}, f.createErr
}

func (f *fakeBackend) Status(_ context.Context, ref Ref) (State, error) {
	f.record("status", ref)
	return f.statusState, f.statusErr
}

func (f *fakeBackend) List(context.Context) ([]State, error) {
	if f.order != nil {
		*f.order = append(*f.order, "list")
	}
	return f.listStates, f.listErr
}

func (f *fakeBackend) Suspend(_ context.Context, ref Ref) error {
	f.record("suspend", ref)
	return f.suspendErr
}

func (f *fakeBackend) Resume(_ context.Context, ref Ref) error {
	f.record("resume", ref)
	return f.resumeErr
}

func (f *fakeBackend) Destroy(_ context.Context, ref Ref) error {
	f.record("destroy", ref)
	return f.destroyErr
}

func (f *fakeBackend) StartWithProgress(_ context.Context, ref Ref, _ func(string)) error {
	f.record("start", ref)
	return nil
}

func (f *fakeBackend) PortForward(_ context.Context, ref Ref, ports []session.PortSpec) ([]session.ForwardHandle, error) {
	if f.portForwardHook != nil {
		f.portForwardHook()
	}
	f.gotSpecs = ports
	f.record("portforward", ref)
	return f.handles, f.portErr
}

func (f *fakeBackend) RunnerToken(_ context.Context, ref Ref) (string, error) {
	f.record("token", ref)
	return f.token, f.tokenErr
}

func (f *fakeBackend) OpencodePassword(_ context.Context, ref Ref) (string, error) {
	f.record("opencodepw", ref)
	return "", nil
}

func (f *fakeBackend) EnsureReaper(_ context.Context, ref Ref, _ k8s.ReaperOptions) error {
	f.record("reaper", ref)
	return nil
}

var _ Backend = (*fakeBackend)(nil)

// fakeSyncRunner implements syncpkg.Runner. It never shells out; it records each
// `mutagen sync <verb>` invocation into *order (shared with the fake backend)
// and returns success, so the best-effort sync calls the orchestration paths
// make are observable and side-effect-free.
type fakeSyncRunner struct {
	order *[]string
	mu    sync.Mutex
	calls [][]string
}

func (r *fakeSyncRunner) Output(_ context.Context, _ io.Reader, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, args)
	if r.order != nil && len(args) >= 2 && args[0] == "sync" {
		*r.order = append(*r.order, "sync-"+args[1])
	}
	return nil, nil
}

func (r *fakeSyncRunner) sawSync(verb string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if len(c) >= 2 && c[0] == "sync" && c[1] == verb {
			return true
		}
	}
	return false
}

// fakeClient wires a Client onto the fake backend + fake sync runner with a temp
// state dir, sharing one order log across both seams.
func fakeClient(t *testing.T, be *fakeBackend) (*Client, *fakeSyncRunner, *[]string) {
	t.Helper()
	order := &[]string{}
	be.order = order
	spy := &fakeSyncRunner{order: order}
	c, err := New(WithBackend(be), WithStateDir(t.TempDir()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.syncRunner = spy
	return c, spy, order
}

func TestClientCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("success stamps fresh-path shortcuts and records the session", func(t *testing.T) {
		be := newFakeBackend()
		c, _, _ := fakeClient(t, be)

		sess, err := c.Create(ctx, CreateOptions{ProjectPath: "/work/repo", ID: "claude-sdk-abc123"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if sess.ID() != "claude-sdk-abc123" {
			t.Errorf("session id = %q, want claude-sdk-abc123", sess.ID())
		}
		if !sess.fresh || sess.freshBackend != session.BackendClaudeSDK || sess.sshPrivPath == "" {
			t.Errorf("fresh shortcuts not stamped: fresh=%v backend=%q priv=%q", sess.fresh, sess.freshBackend, sess.sshPrivPath)
		}
		// The spec handed to the backend carries the project path and the freshly
		// generated SSH public key.
		if be.gotSpec.ProjectPath != "/work/repo" || be.gotSpec.SSHPublicKey == "" {
			t.Errorf("CreateSession spec = %+v, want ProjectPath + SSHPublicKey set", be.gotSpec)
		}
		// Wave-1 worktree split: WorkspacePath is normalized to equal ProjectPath
		// (no worktree yet), and the same value is stamped onto the session.
		if be.gotSpec.WorkspacePath != "/work/repo" {
			t.Errorf("spec WorkspacePath = %q, want /work/repo (== ProjectPath)", be.gotSpec.WorkspacePath)
		}
		if sess.workspacePath != "/work/repo" {
			t.Errorf("session workspacePath = %q, want /work/repo", sess.workspacePath)
		}
		if be.gotSpec.Backend != session.BackendClaudeSDK {
			t.Errorf("spec backend = %q, want default claude-sdk", be.gotSpec.Backend)
		}
		// The session is recorded locally so status/reconnect can find it.
		if _, lerr := c.index.Load(string(sess.ID())); lerr != nil {
			t.Errorf("index entry not saved: %v", lerr)
		}
	})

	t.Run("missing project path is rejected before any cluster call", func(t *testing.T) {
		be := newFakeBackend()
		c, _, _ := fakeClient(t, be)
		if _, err := c.Create(ctx, CreateOptions{}); !errors.Is(err, ErrProjectPathRequired) {
			t.Fatalf("Create with no ProjectPath: got %v, want ErrProjectPathRequired", err)
		}
		if len(be.gotRefs["create"]) != 0 {
			t.Error("CreateSession must not be called when validation fails")
		}
	})

	t.Run("backend create error propagates", func(t *testing.T) {
		be := newFakeBackend()
		be.createErr = errors.New("apiserver down")
		c, _, _ := fakeClient(t, be)
		if _, err := c.Create(ctx, CreateOptions{ProjectPath: "/work/repo"}); err == nil {
			t.Fatal("Create: want error from CreateSession, got nil")
		}
	})
}

func TestClientStatus(t *testing.T) {
	ctx := context.Background()
	be := newFakeBackend()
	be.statusState = State{ID: "sess-1", Status: session.StatusRunning}
	c, _, _ := fakeClient(t, be)

	st, err := c.Status(ctx, "sess-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.ID != "sess-1" || st.Status != session.StatusRunning {
		t.Errorf("Status = %+v, want {sess-1 running}", st)
	}
	if got := be.gotRefs["status"]; len(got) != 1 || got[0].ID != "sess-1" {
		t.Errorf("backend.Status refs = %v, want one call for sess-1", got)
	}

	be.statusErr = errors.New("not found")
	if _, err := c.Status(ctx, "sess-1"); err == nil {
		t.Fatal("Status: want backend error, got nil")
	}
}

func TestClientList(t *testing.T) {
	ctx := context.Background()
	be := newFakeBackend()
	be.listStates = []State{{ID: "a"}, {ID: "b"}}
	c, _, _ := fakeClient(t, be)

	got, err := c.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("List = %v, want 2 states", got)
	}

	be.listErr = errors.New("boom")
	if _, err := c.List(ctx); err == nil {
		t.Fatal("List: want backend error, got nil")
	}
}

func TestClientSuspend(t *testing.T) {
	ctx := context.Background()

	t.Run("success suspends then pauses sync", func(t *testing.T) {
		be := newFakeBackend()
		c, spy, order := fakeClient(t, be)
		if err := c.Suspend(ctx, "sess-1"); err != nil {
			t.Fatalf("Suspend: %v", err)
		}
		if got := be.gotRefs["suspend"]; len(got) != 1 || got[0].ID != "sess-1" {
			t.Errorf("backend.Suspend refs = %v, want one call for sess-1", got)
		}
		if !spy.sawSync("pause") {
			t.Error("Suspend must pause sync after a successful backend suspend")
		}
		if want := []string{"suspend", "sync-pause"}; !reflect.DeepEqual(*order, want) {
			t.Errorf("order = %v, want %v", *order, want)
		}
	})

	t.Run("backend error short-circuits before pausing sync", func(t *testing.T) {
		be := newFakeBackend()
		be.suspendErr = errors.New("cannot scale down")
		c, spy, _ := fakeClient(t, be)
		if err := c.Suspend(ctx, "sess-1"); err == nil {
			t.Fatal("Suspend: want backend error, got nil")
		}
		if spy.sawSync("pause") {
			t.Error("Suspend must NOT pause sync when the backend suspend failed")
		}
	})
}

func TestClientResume(t *testing.T) {
	ctx := context.Background()

	t.Run("success resumes then resumes sync", func(t *testing.T) {
		be := newFakeBackend()
		c, spy, order := fakeClient(t, be)
		if err := c.Resume(ctx, "sess-1"); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if !spy.sawSync("resume") {
			t.Error("Resume must resume sync after a successful backend resume")
		}
		if want := []string{"resume", "sync-resume"}; !reflect.DeepEqual(*order, want) {
			t.Errorf("order = %v, want %v", *order, want)
		}
	})

	t.Run("backend error short-circuits before resuming sync", func(t *testing.T) {
		be := newFakeBackend()
		be.resumeErr = errors.New("no such sandbox")
		c, spy, _ := fakeClient(t, be)
		if err := c.Resume(ctx, "sess-1"); err == nil {
			t.Fatal("Resume: want backend error, got nil")
		}
		if spy.sawSync("resume") {
			t.Error("Resume must NOT resume sync when the backend resume failed")
		}
	})
}

// TestDestroyStopsSyncBeforeClusterDestroy is the regression net for §8's
// Destroy reorder: sync must be terminated BEFORE the cluster destroy (so the
// mutagen-over-SSH stream is torn down while the pod is still up), and local
// state removal must run only AFTER a successful destroy.
func TestDestroyStopsSyncBeforeClusterDestroy(t *testing.T) {
	ctx := context.Background()

	t.Run("success: sync-terminate, then destroy, then local-state removal", func(t *testing.T) {
		be := newFakeBackend()
		c, spy, order := fakeClient(t, be)
		// Seed a local index entry so we can prove RemoveLocalState ran (after destroy).
		if _, err := c.Create(ctx, CreateOptions{ProjectPath: "/work/repo", ID: "sess-1"}); err != nil {
			t.Fatalf("seed Create: %v", err)
		}
		*order = (*order)[:0] // discard the create bookkeeping; measure Destroy only

		if err := c.Destroy(ctx, "sess-1"); err != nil {
			t.Fatalf("Destroy: %v", err)
		}
		if want := []string{"sync-terminate", "destroy"}; !reflect.DeepEqual(*order, want) {
			t.Fatalf("call order = %v, want %v (sync stop must precede cluster destroy)", *order, want)
		}
		if !spy.sawSync("terminate") {
			t.Error("Destroy must terminate sync")
		}
		// RemoveLocalState ran last: the seeded index entry is gone.
		if _, err := c.index.Load("sess-1"); err == nil {
			t.Error("Destroy must remove the local index entry after a successful destroy")
		}
	})

	t.Run("backend destroy error: sync stopped, local state preserved", func(t *testing.T) {
		be := newFakeBackend()
		be.destroyErr = errors.New("finalizer stuck")
		c, _, order := fakeClient(t, be)
		if _, err := c.Create(ctx, CreateOptions{ProjectPath: "/work/repo", ID: "sess-1"}); err != nil {
			t.Fatalf("seed Create: %v", err)
		}
		*order = (*order)[:0]

		if err := c.Destroy(ctx, "sess-1"); err == nil {
			t.Fatal("Destroy: want backend error, got nil")
		}
		// Sync was still stopped first (best-effort teardown), and RemoveLocalState
		// did NOT run — local state survives so a retry can find the session.
		if want := []string{"sync-terminate", "destroy"}; !reflect.DeepEqual(*order, want) {
			t.Errorf("call order = %v, want %v", *order, want)
		}
		if _, err := c.index.Load("sess-1"); err != nil {
			t.Errorf("failed destroy must preserve the local index entry: %v", err)
		}
	})
}

// closeSpyHandle is a session.ForwardHandle that counts Close calls, so the
// DialRunner cleanup func can be verified to tear the forward down.
type closeSpyHandle struct {
	port   int
	closes int
	done   chan struct{}
}

func (h *closeSpyHandle) LocalPort() int        { return h.port }
func (h *closeSpyHandle) Close() error          { h.closes++; return nil }
func (h *closeSpyHandle) Done() <-chan struct{} { return h.done }

func TestDialRunner(t *testing.T) {
	ctx := context.Background()

	t.Run("forwards the runner port only and cleans up", func(t *testing.T) {
		be := newFakeBackend()
		h := &closeSpyHandle{port: 12345, done: make(chan struct{})}
		be.handles = []session.ForwardHandle{h}
		be.token = "tok"
		c, _, _ := fakeClient(t, be)

		rc, cleanup, err := c.DialRunner(ctx, Ref{ID: "sess-1"})
		if err != nil {
			t.Fatalf("DialRunner: %v", err)
		}
		if rc == nil {
			t.Fatal("DialRunner returned a nil runner client")
		}
		// #3: only the runner HTTP port is forwarded — the SSH port (used solely by
		// mutagen sync, which DialRunner never runs) is not.
		if want := k8s.ForwardSpecsRunnerOnly(0); !reflect.DeepEqual(be.gotSpecs, want) {
			t.Errorf("PortForward specs = %v, want runner-only %v", be.gotSpecs, want)
		}
		cleanup()
		if h.closes != 1 {
			t.Errorf("cleanup closed the forward %d times, want 1", h.closes)
		}
	})

	t.Run("token error tears down the forward and returns the error", func(t *testing.T) {
		be := newFakeBackend()
		h := &closeSpyHandle{port: 12345, done: make(chan struct{})}
		be.handles = []session.ForwardHandle{h}
		be.tokenErr = errors.New("no secret")
		c, _, _ := fakeClient(t, be)

		if _, _, err := c.DialRunner(ctx, Ref{ID: "sess-1"}); err == nil {
			t.Fatal("DialRunner: want token error, got nil")
		}
		if h.closes != 1 {
			t.Errorf("failed DialRunner must close the forward once, got %d", h.closes)
		}
	})
}

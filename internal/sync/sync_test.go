package sync

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls [][]string
}

func (f *fakeRunner) Output(_ context.Context, _ io.Reader, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	return nil, nil
}

type errorRunner struct {
	msg string
}

func (e *errorRunner) Output(_ context.Context, _ io.Reader, args ...string) ([]byte, error) {
	return nil, &fakeError{msg: e.msg}
}

type fakeError struct{ msg string }

func (e *fakeError) Error() string { return e.msg }

func TestStatusUsesLabelSelector(t *testing.T) {
	r := &fakeRunner{}
	m := New(r)
	if _, err := m.Status(context.Background(), "abc"); err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(r.calls))
	}
	got := strings.Join(r.calls[0], " ")
	if want := "sync list --label-selector=sandbox-session=abc"; got != want {
		t.Errorf("status args: got %q, want %q", got, want)
	}
}

// RV20/RV21: FlushAll forces a sync cycle (by label) so async transport failures
// surface and the initial tree settles.
func TestFlushAllUsesLabelSelector(t *testing.T) {
	r := &fakeRunner{}
	m := New(r)
	if err := m.FlushAll(context.Background(), "abc"); err != nil {
		t.Fatalf("flush: %v", err)
	}
	got := strings.Join(r.calls[0], " ")
	if want := "sync flush --label-selector=sandbox-session=abc"; got != want {
		t.Errorf("flush args: got %q, want %q", got, want)
	}
}

// FlushAll treats "no sessions found" as success (nothing to flush).
func TestFlushAllIgnoresNotFound(t *testing.T) {
	m := New(&errorRunner{msg: "no sessions found"})
	if err := m.FlushAll(context.Background(), "abc"); err != nil {
		t.Errorf("flush should ignore not-found, got %v", err)
	}
}

// FlushAll surfaces a real transport error (the RV20 case).
func TestFlushAllSurfacesRealError(t *testing.T) {
	m := New(&errorRunner{msg: "ssh: handshake failed"})
	if err := m.FlushAll(context.Background(), "abc"); err == nil {
		t.Error("flush should surface a real transport error")
	}
}

func TestCreateAll(t *testing.T) {
	r := &fakeRunner{}
	m := New(r)

	// SSHHost is an ssh config alias (resolved via the per-session Include
	// block), NOT a host:port — exercise the documented/real shape.
	// ProjectPath is a temp dir so the test never reads a real .gitignore.
	const sshHost = "sandbox-test-session"
	spec := Spec{
		SessionID:    "test-session",
		ProjectPath:  t.TempDir(),
		RemotePath:   "/session/workspace/Users/cullen/git/homelab",
		HomeDir:      "/Users/cullen",
		SSHHost:      sshHost,
		RemoteClaude: "/session/state/claude",
	}

	created, err := m.CreateAll(context.Background(), spec)
	if err != nil {
		t.Fatalf("createAll: %v", err)
	}
	if !created {
		t.Fatal("CreateAll should report created=true when the project session is freshly made")
	}

	// Should have created: 1 project + 5 config + 3 transcripts = 9 sessions
	if len(r.calls) != 9 {
		t.Fatalf("got %d calls, want 9", len(r.calls))
	}

	// First call should be the project sync with two-way-safe
	first := strings.Join(r.calls[0], " ")
	if !strings.Contains(first, "two-way-safe") {
		t.Errorf("project sync should be two-way-safe: %s", first)
	}
	if !strings.Contains(first, "test-session-project") {
		t.Errorf("project session name missing: %s", first)
	}

	// Verify config syncs use one-way-safe AND push host -> remote: the local
	// path must be the alpha (first positional) arg and SSHHost:remote the beta.
	// A swapped alpha/beta would silently invert push/pull, yet still contain
	// "one-way-safe" — so assert the endpoint ORDER, not just the mode.
	for i := 1; i <= 5; i++ {
		call := strings.Join(r.calls[i], " ")
		if !strings.Contains(call, "one-way-safe") {
			t.Errorf("config sync %d should be one-way-safe: %s", i, call)
		}
		alpha, beta := endpoints(t, r.calls[i])
		if !strings.HasPrefix(alpha, spec.HomeDir+"/.claude/") {
			t.Errorf("config sync %d alpha should be a local ~/.claude path (push host->remote), got alpha=%q beta=%q", i, alpha, beta)
		}
		if !strings.HasPrefix(beta, sshHost+":") {
			t.Errorf("config sync %d beta should be %s:<remote> (push host->remote), got alpha=%q beta=%q", i, sshHost, alpha, beta)
		}
	}

	// Verify transcript syncs use one-way-safe AND pull remote -> host: here
	// SSHHost:remote must be the alpha and the local path the beta — the
	// mirror image of the config direction.
	for i := 6; i <= 8; i++ {
		call := strings.Join(r.calls[i], " ")
		if !strings.Contains(call, "one-way-safe") {
			t.Errorf("transcript sync %d should be one-way-safe: %s", i, call)
		}
		alpha, beta := endpoints(t, r.calls[i])
		if !strings.HasPrefix(alpha, sshHost+":") {
			t.Errorf("transcript sync %d alpha should be %s:<remote> (pull remote->host), got alpha=%q beta=%q", i, sshHost, alpha, beta)
		}
		if !strings.HasPrefix(beta, spec.HomeDir+"/.claude/") {
			t.Errorf("transcript sync %d beta should be a local ~/.claude path (pull remote->host), got alpha=%q beta=%q", i, alpha, beta)
		}
	}
}

// endpoints returns the two trailing positional URL args (alpha, beta) of a
// `mutagen sync create` invocation. They are the last two args: every flag in
// CreateAll is a single token (--name=, --label <v> handled as two flag tokens,
// --mode=, --ignore=) and the URLs are appended last, so the final two
// elements are alpha then beta.
func endpoints(t *testing.T, call []string) (alpha, beta string) {
	t.Helper()
	if len(call) < 2 {
		t.Fatalf("call too short to have alpha/beta endpoints: %v", call)
	}
	return call[len(call)-2], call[len(call)-1]
}

// The project sync's ignore flags must layer in precedence order (mutagen:
// later wins): build-tree defaults, then the project .gitignore verbatim,
// then the security/auto-exec set LAST so no .gitignore negation can
// re-enable syncing a secret or host-auto-executing file.
func TestCreateProjectSyncIgnoreLayering(t *testing.T) {
	dir := t.TempDir()
	gitignore := "# comment\nsecrets/\n!vendor\n*.tfstate\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignore), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &fakeRunner{}
	m := New(r)
	spec := Spec{
		SessionID:    "test-session",
		ProjectPath:  dir,
		RemotePath:   "/session/workspace/proj",
		HomeDir:      "/Users/cullen",
		SSHHost:      "sandbox-test-session",
		RemoteClaude: "/session/state/claude",
	}
	if _, err := m.CreateAll(context.Background(), spec); err != nil {
		t.Fatalf("createAll: %v", err)
	}

	call := r.calls[0] // project sync is created first
	idx := func(flag string) int {
		for i, a := range call {
			if a == flag {
				return i
			}
		}
		t.Fatalf("flag %q missing from project sync args: %v", flag, call)
		return -1
	}

	// All three layers present, in order.
	buildTree := idx("--ignore=node_modules")
	fromGitignore := idx("--ignore=secrets/")
	negation := idx("--ignore=!vendor")
	security := idx("--ignore=.env")
	autoExec := idx("--ignore=.envrc")
	if buildTree >= fromGitignore || fromGitignore >= security {
		t.Errorf("ignore layers out of order: buildTree=%d gitignore=%d security=%d in %v",
			buildTree, fromGitignore, security, call)
	}
	if negation > security {
		t.Errorf("gitignore negation (%d) must precede security ignores (%d) so it cannot override them", negation, security)
	}
	if autoExec < security {
		t.Errorf("auto-exec ignores should sit in the security layer: autoExec=%d security=%d", autoExec, security)
	}
	// Credential filenames (S2) sit in the security layer too — present, and
	// positioned after the gitignore layer so no negation re-enables them.
	for _, flag := range []string{
		"--ignore=.netrc",
		"--ignore=_netrc",
		"--ignore=.npmrc",
		"--ignore=.git-credentials",
		"--ignore=.aws",
		"--ignore=service-account*.json",
		"--ignore=id_rsa",
		"--ignore=id_rsa.*",
		"--ignore=id_ed25519",
		"--ignore=id_ed25519.*",
		"--ignore=id_ecdsa",
		"--ignore=id_ecdsa.*",
	} {
		if pos := idx(flag); pos < security {
			t.Errorf("credential ignore %q should sit in the security layer: pos=%d security=%d", flag, pos, security)
		}
	}
	idx("--ignore=*.tfstate") // gitignore pattern passed through verbatim

	// Comment lines never become flags.
	for _, a := range call {
		if strings.Contains(a, "comment") {
			t.Errorf("comment leaked into args: %q", a)
		}
	}

	// Endpoints stay the trailing positional args.
	alpha, beta := endpoints(t, call)
	if alpha != dir || beta != spec.SSHHost+":"+spec.RemotePath {
		t.Errorf("endpoints displaced by ignore flags: alpha=%q beta=%q", alpha, beta)
	}
}

// CreateProject creates ONLY the load-bearing project sync — the one the connect
// path stages in the foreground before the first prompt (§5). Exactly one
// `mutagen sync create`, and it is the two-way-safe project session.
func TestCreateProjectCreatesOnlyProject(t *testing.T) {
	r := &fakeRunner{}
	m := New(r)
	spec := Spec{
		SessionID:    "test-session",
		ProjectPath:  t.TempDir(),
		RemotePath:   "/session/workspace/proj",
		HomeDir:      "/Users/cullen",
		SSHHost:      "sandbox-test-session",
		RemoteClaude: "/session/state/claude",
	}
	created, err := m.CreateProject(context.Background(), spec)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if !created {
		t.Fatal("CreateProject should report created=true for a fresh project sync")
	}
	if len(r.calls) != 1 {
		t.Fatalf("CreateProject should issue exactly 1 create, got %d: %v", len(r.calls), r.calls)
	}
	call := strings.Join(r.calls[0], " ")
	if !strings.Contains(call, "two-way-safe") || !strings.Contains(call, "test-session-project") {
		t.Errorf("CreateProject did not create the two-way-safe project sync: %s", call)
	}
}

// A blank ProjectPath is a no-op for CreateProject (nothing load-bearing to
// stage) — no create call, created=false (MF4).
func TestCreateProjectNoProjectPath(t *testing.T) {
	r := &fakeRunner{}
	m := New(r)
	created, err := m.CreateProject(context.Background(), Spec{SessionID: "s", HomeDir: "/Users/cullen", SSHHost: "sandbox-s", RemoteClaude: "/session/state/claude"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if created {
		t.Error("CreateProject with blank ProjectPath should report created=false")
	}
	if len(r.calls) != 0 {
		t.Errorf("CreateProject with blank ProjectPath should issue no creates, got %v", r.calls)
	}
}

// CreateInputs creates the 8 non-load-bearing config/transcript syncs (5+3) and
// nothing else — never the project sync — so the connect path can run it off the
// foreground (§5).
func TestCreateInputsCreatesEight(t *testing.T) {
	r := &fakeRunner{}
	m := New(r)
	spec := Spec{
		SessionID:    "test-session",
		ProjectPath:  t.TempDir(),
		RemotePath:   "/session/workspace/proj",
		HomeDir:      "/Users/cullen",
		SSHHost:      "sandbox-test-session",
		RemoteClaude: "/session/state/claude",
	}
	if err := m.CreateInputs(context.Background(), spec); err != nil {
		t.Fatalf("CreateInputs: %v", err)
	}
	if len(r.calls) != len(ConfigInputsSubs)+len(TranscriptSubs) {
		t.Fatalf("CreateInputs should issue %d creates, got %d", len(ConfigInputsSubs)+len(TranscriptSubs), len(r.calls))
	}
	for i, call := range r.calls {
		joined := strings.Join(call, " ")
		// two-way-safe uniquely identifies the load-bearing project sync; the
		// inputs are all one-way-safe.
		if strings.Contains(joined, "two-way-safe") || strings.Contains(joined, "--name=sandbox-test-session-project ") {
			t.Errorf("CreateInputs must not create the project sync (call %d): %s", i, joined)
		}
		if !strings.Contains(joined, "one-way-safe") {
			t.Errorf("input sync %d should be one-way-safe: %s", i, joined)
		}
	}
}

// The claude-pane user-statusline chain reads the host-synced script from
// <RemoteClaude>/statusline/user-statusline (runner/src/claude-pane-observer.ts,
// STATUSLINE_SCRIPT candidate list), so pin the ConfigInputsSubs entry: the
// host→remote endpoints and — critically — that it targets the statusline/
// SIBLING dir, never the runner-owned pane-observer/ dir holding the
// runner-minted observer token.
func TestConfigInputsSyncStatuslineEntry(t *testing.T) {
	r := &fakeRunner{}
	m := New(r)
	spec := Spec{
		SessionID:    "s",
		HomeDir:      "/Users/cullen",
		SSHHost:      "sandbox-s",
		RemoteClaude: "/session/state/claude",
	}
	if err := m.CreateInputs(context.Background(), spec); err != nil {
		t.Fatalf("CreateInputs: %v", err)
	}
	found := false
	for _, call := range r.calls {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, "pane-observer") {
			t.Errorf("no sync may ever target the runner-owned pane-observer/ dir: %s", joined)
		}
		if !strings.Contains(joined, "--name=sandbox-s-config-statusline") {
			continue
		}
		found = true
		if !strings.Contains(joined, "/Users/cullen/.claude/statusline sandbox-s:/session/state/claude/statusline") {
			t.Errorf("statusline sync endpoints drifted from the runner's candidate path: %s", joined)
		}
		if !strings.Contains(joined, "one-way-safe") {
			t.Errorf("statusline sync must be one-way-safe host→remote: %s", joined)
		}
	}
	if !found {
		t.Errorf("CreateInputs did not create the config-statusline sync: %v", r.calls)
	}
}

// CreateInputs surfaces a real create failure (it must not vanish when run in
// the background) but swallows an idempotent "already exists".
func TestCreateInputsErrorHandling(t *testing.T) {
	spec := Spec{SessionID: "s", ProjectPath: t.TempDir(), HomeDir: "/Users/cullen", SSHHost: "sandbox-s", RemoteClaude: "/session/state/claude"}
	if err := New(&errorRunner{msg: "connection refused"}).CreateInputs(context.Background(), spec); err == nil {
		t.Error("CreateInputs should surface a real create error")
	}
	if err := New(&errorRunner{msg: "session already exists"}).CreateInputs(context.Background(), spec); err != nil {
		t.Errorf("CreateInputs should swallow already-exists, got %v", err)
	}
}

func TestCreateAllIdempotent(t *testing.T) {
	r := &errorRunner{msg: "session already exists"}
	m := New(r)

	spec := Spec{
		SessionID:    "test-session",
		ProjectPath:  t.TempDir(),
		RemotePath:   "/session/workspace/tmp",
		HomeDir:      "/Users/cullen",
		SSHHost:      "127.0.0.1:22000",
		RemoteClaude: "/session/state/claude",
	}

	// "already exists" errors should be swallowed
	created, err := m.CreateAll(context.Background(), spec)
	if err != nil {
		t.Fatalf("createAll should swallow already-exists: %v", err)
	}
	// An already-existing project session is the reconnect signal: created=false
	// so the caller skips the blocking initial flush.
	if created {
		t.Fatal("CreateAll should report created=false when the project session already exists")
	}
}

func TestCreateAllRealError(t *testing.T) {
	r := &errorRunner{msg: "connection refused"}
	m := New(r)

	spec := Spec{
		SessionID:    "test-session",
		ProjectPath:  t.TempDir(),
		RemotePath:   "/session/workspace/tmp",
		HomeDir:      "/Users/cullen",
		SSHHost:      "127.0.0.1:22000",
		RemoteClaude: "/session/state/claude",
	}

	if _, err := m.CreateAll(context.Background(), spec); err == nil {
		t.Fatal("expected error for connection refused")
	}
}

func TestPauseResumeTerminate(t *testing.T) {
	r := &fakeRunner{}
	m := New(r)

	if err := m.PauseAll(context.Background(), "test"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("pause: got %d calls, want 1", len(r.calls))
	}
	if !strings.Contains(strings.Join(r.calls[0], " "), "pause") {
		t.Errorf("expected pause command: %s", r.calls[0])
	}

	r.calls = nil
	if err := m.ResumeAll(context.Background(), "test"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !strings.Contains(strings.Join(r.calls[0], " "), "resume") {
		t.Errorf("expected resume command: %s", r.calls[0])
	}

	r.calls = nil
	if err := m.TerminateAll(context.Background(), "test"); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if !strings.Contains(strings.Join(r.calls[0], " "), "terminate") {
		t.Errorf("expected terminate command: %s", r.calls[0])
	}
}

func TestTerminateAllNotFound(t *testing.T) {
	r := &errorRunner{msg: "session not found"}
	m := New(r)
	// "not found" should be swallowed
	if err := m.TerminateAll(context.Background(), "test"); err != nil {
		t.Fatalf("terminate should swallow not-found: %v", err)
	}
}

// PauseAll/ResumeAll must swallow a "not found" error exactly as
// FlushAll/TerminateAll do — pausing/resuming a session that was never synced
// is a no-op, not a failure.
func TestPauseResumeAllIgnoreNotFound(t *testing.T) {
	const notFound = "unable to locate requested sessions"
	if err := New(&errorRunner{msg: notFound}).PauseAll(context.Background(), "test"); err != nil {
		t.Errorf("PauseAll should swallow not-found, got %v", err)
	}
	if err := New(&errorRunner{msg: notFound}).ResumeAll(context.Background(), "test"); err != nil {
		t.Errorf("ResumeAll should swallow not-found, got %v", err)
	}
}

// PauseAll/ResumeAll must still surface a real (non-not-found) error.
func TestPauseResumeAllSurfaceRealError(t *testing.T) {
	if err := New(&errorRunner{msg: "ssh: handshake failed"}).PauseAll(context.Background(), "test"); err == nil {
		t.Error("PauseAll should surface a real error")
	}
	if err := New(&errorRunner{msg: "ssh: handshake failed"}).ResumeAll(context.Background(), "test"); err == nil {
		t.Error("ResumeAll should surface a real error")
	}
}

// isMutagenNotFound recognizes all three mutagen phrasings. The third branch
// ("unable to locate requested sessions") is what modern mutagen emits for a
// label selector that matches nothing; without it PauseAll/ResumeAll/Flush/
// Terminate would wrongly surface a harmless no-match as a failure.
func TestIsMutagenNotFound(t *testing.T) {
	for _, msg := range []string{
		"no sessions found",
		"session not found",
		"unable to locate requested sessions",
	} {
		if !isMutagenNotFound(&fakeError{msg: msg}) {
			t.Errorf("isMutagenNotFound should match %q", msg)
		}
	}
	if isMutagenNotFound(&fakeError{msg: "connection refused"}) {
		t.Error("isMutagenNotFound should not match an unrelated error")
	}
}

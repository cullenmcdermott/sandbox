// Package sync manages Mutagen file sync sessions for remote agent sessions.
//
// Unlike the existing lima-based mutagen.Manager (which syncs to Lima VMs via
// SSH), this module syncs to Kubernetes runner pods via SSH over
// kubectl port-forward. The sync sessions are:
//
//  1. Project: local workspace <-> remote <host workspace path>, two-way-safe
//     (the pod bind-mounts the workspace at the real host path, so both
//     endpoints match; the caller passes Spec.WorkspacePath into both)
//  2. Config inputs: ~/.claude/skills etc -> remote /session/state/claude/, one-way
//  3. Transcripts: remote /session/state/claude/projects -> local ~/.claude/projects, one-way
package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"strings"
	stdsync "sync"

	"k8s.io/client-go/tools/clientcmd"
)

// Runner is the interface for Mutagen CLI invocations. It matches the
// existing mutagen.Runner from system-config so the same implementation
// can be shared.
type Runner interface {
	Output(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error)
}

// Manager manages Mutagen sync sessions for a remote session.
type Manager struct {
	r Runner

	// kubeContext resolves the current kube context name for the sandbox-context
	// label (MF3). nil (production) uses the process-cached ambient kubeconfig
	// resolver; tests inject a stub to make the label deterministic.
	kubeContext func() string

	// namespace is the k8s namespace this Manager stamps onto new syncs via the
	// sandbox-namespace label ([V28] — the GC live set is namespace-scoped, so a
	// same-context sync in another namespace must be distinguishable). "" leaves
	// syncs unlabeled (legacy / no-namespace callers), matching pre-[V28] syncs.
	// Set by the client's syncManager() to the client's effective namespace.
	namespace string
}

// New creates a sync Manager.
func New(r Runner) *Manager {
	return &Manager{r: r}
}

// SetNamespace records the k8s namespace this Manager stamps onto new syncs (the
// sandbox-namespace label, [V28]). Sanitized into mutagen's label charset like
// the context label. "" leaves new syncs unlabeled.
func (m *Manager) SetNamespace(ns string) { m.namespace = ns }

// kubeContextCache resolves the ambient kubeconfig current-context ONCE per
// process: the value can't change within a run, and CreateAll issues up to 8
// creates that would otherwise each re-read ~/.kube/config. A Manager with an
// injected kubeContext (tests) bypasses this cache entirely.
var (
	kubeCtxOnce stdsync.Once
	kubeCtxName string
)

// currentKubeContext returns the ambient kubeconfig current-context, "" when it
// can't be resolved (in-cluster, no kubeconfig, or a load error). Cached.
func currentKubeContext() string {
	kubeCtxOnce.Do(func() { kubeCtxName = loadKubeContext() })
	return kubeCtxName
}

// loadKubeContext reads the current-context from the ambient kubeconfig
// (KUBECONFIG / ~/.kube/config). "" on any failure — the CLI runs against the
// same ambient kubeconfig the client uses, so this matches the cluster the
// session was created against.
func loadKubeContext() string {
	cfg, err := clientcmd.NewDefaultClientConfigLoadingRules().Load()
	if err != nil {
		return ""
	}
	return cfg.CurrentContext
}

// CurrentContext returns the kube context this Manager stamps onto new syncs and
// scopes GC to (MF3), SANITIZED into mutagen's label charset ([V3] — a raw
// context like `kubernetes-admin@kubernetes` or an EKS ARN fails label
// validation and would disable sync entirely). Sanitizing HERE — the single
// value both the create path and the GC comparison read — keeps the stamped
// label and the scope check identical. "" when the context can't be resolved
// (in-cluster / no kubeconfig), in which case no sandbox-context label is stamped
// and GC falls back to the legacy live-set check.
func (m *Manager) CurrentContext() string {
	return sanitizeLabelValue(m.rawContext())
}

// rawContext returns the unsanitized kube context name (the injected stub in
// tests, else the process-cached ambient kubeconfig current-context).
func (m *Manager) rawContext() string {
	if m.kubeContext != nil {
		return m.kubeContext()
	}
	return currentKubeContext()
}

// contextLabelArgs returns the `--label sandbox-context=<ctx>` create flag when
// a context is resolvable, or nil. A nil (empty-context) result deliberately
// leaves the sync unlabeled — matching pre-MF3 sessions — so an in-cluster or
// kubeconfig-less caller still creates working syncs (they're then GC-scoped by
// the legacy live-set path, not the context label).
func (m *Manager) contextLabelArgs() []string {
	ctx := m.CurrentContext()
	if ctx == "" {
		return nil
	}
	return []string{"--label", "sandbox-context=" + ctx}
}

// namespaceLabelArgs returns the `--label sandbox-namespace=<ns>` create flag
// when a namespace is set ([V28]), sanitized into mutagen's label charset with
// the same helper the context label uses. nil (unset namespace) leaves the sync
// unlabeled, matching pre-[V28] syncs — GC then treats a label-less sync as it
// always did (no cross-namespace skip).
func (m *Manager) namespaceLabelArgs() []string {
	ns := sanitizeLabelValue(m.namespace)
	if ns == "" {
		return nil
	}
	return []string{"--label", "sandbox-namespace=" + ns}
}

// labelRune reports whether r is allowed in a mutagen label value: an
// alphanumeric or one of '-', '_', '.'.
func labelRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.'
}

func alnumRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// sanitizeLabelValue coerces s into a valid mutagen label value ([V3]): only
// alphanumerics and '-', '_', '.', at most 63 chars, and an alphanumeric first
// and last char. Invalid runes become '-'. When anything had to change OR the
// input exceeded 63 chars, an 8-hex-char suffix of the original's sha256 is
// appended (inside the 63-char cap) so two distinct originals that sanitize to
// the same base still get distinct labels. A value that is already valid and
// within length passes through UNCHANGED, preserving backward compatibility with
// existing labels like "my-cluster". Empty in → empty out (callers read "" as "no
// label").
func sanitizeLabelValue(s string) string {
	if s == "" {
		return ""
	}
	changed := false
	mapped := make([]rune, 0, len(s))
	for _, r := range s {
		if labelRune(r) {
			mapped = append(mapped, r)
			continue
		}
		mapped = append(mapped, '-')
		changed = true
	}
	// Trim to an alphanumeric first/last char (mutagen requires it).
	start, end := 0, len(mapped)
	for start < end && !alnumRune(mapped[start]) {
		start++
		changed = true
	}
	for end > start && !alnumRune(mapped[end-1]) {
		end--
		changed = true
	}
	base := string(mapped[start:end])

	if !changed && len(base) <= 63 {
		return base // already valid and within length — pass through unchanged
	}

	sum := sha256.Sum256([]byte(s))
	hash := hex.EncodeToString(sum[:])[:8] // hex → alphanumeric, safe as the last char
	if base == "" {
		return hash
	}
	const maxBase = 63 - 9 // 9 = len("-") + len(8-char hash)
	if len(base) > maxBase {
		base = base[:maxBase]
		// Re-trim: the clamp may have cut mid-run, but the joining '-' + hash below
		// restores an alphanumeric end regardless; only guard against a now-empty base.
		for len(base) > 0 && !alnumRune(rune(base[len(base)-1])) {
			base = base[:len(base)-1]
		}
		if base == "" {
			return hash
		}
	}
	return base + "-" + hash
}

// Spec describes the sync sessions to create for a remote session.
type Spec struct {
	SessionID    string // sandbox session ID
	ProjectPath  string // local absolute path of the project
	RemotePath   string // remote workspace path; the real host project path bind-mounted into the pod (e.g. /Users/cullen/git/homelab)
	HomeDir      string // local home dir (e.g. /Users/cullen)
	SSHHost      string // mutagen SSH host alias resolved via the per-session ssh config block (e.g. sandbox-<id>), not a host:port
	RemoteClaude string // remote CLAUDE_CONFIG_DIR (e.g. /session/state/claude)
}

// ConfigInputsSubs is the set of ~/.claude subdirectories synced one-way
// from host to remote (config inputs, no credentials).
var ConfigInputsSubs = []struct {
	Local  string
	Remote string
}{
	{"skills", "skills"},
	{"agents", "agents"},
	{"commands", "commands"},
	{"hooks", "hooks"},
}

// TranscriptSubs is the set of ~/.claude subdirectories synced one-way
// from remote to host (transcripts of agent activity).
var TranscriptSubs = []string{"projects", "todos", "tasks"}

// CreateAll creates all three sync session groups for a remote session: the
// load-bearing project sync first, then the config-input and transcript groups.
// It is idempotent: existing sessions are not recreated. It reports whether the
// load-bearing project sync session was newly created (created=true) versus
// already existing (created=false). Callers use this to decide whether to block
// on an initial flush — needed only for a session's first-ever sync — or let an
// existing session reconcile in the background on reconnect.
//
// The connect path (client) does NOT call CreateAll: it splits it into
// CreateProject (foreground, load-bearing for the first prompt) and CreateInputs
// (backgrounded, §5) so the visible prompt is not gated on the 7 non-load-bearing
// config/transcript sync-create execs. CreateAll remains the single-shot form for
// callers that want the whole set synchronously.
func (m *Manager) CreateAll(ctx context.Context, spec Spec) (created bool, err error) {
	created, err = m.CreateProject(ctx, spec)
	if err != nil {
		return false, err
	}
	if err := m.CreateInputs(ctx, spec); err != nil {
		return false, err
	}
	return created, nil
}

// CreateProject creates ONLY the load-bearing two-way-safe project sync — the
// one the agent needs staged before it can work on the repo. Split out of
// CreateAll so the connect path can create it in the foreground and defer the
// config/transcript groups to the background (§5). Reports created=true only on
// a fresh create (created=false when the session already exists, the reconnect
// signal). A blank ProjectPath (e.g. attaching to a session whose State lacks
// the local repo path) is a no-op reporting created=false — there is nothing
// load-bearing to stage, and a blank alpha URL would fail with "unable to parse
// alpha URL: empty URL" (MF4).
func (m *Manager) CreateProject(ctx context.Context, spec Spec) (created bool, err error) {
	if spec.ProjectPath == "" {
		return false, nil
	}
	return m.createProjectSync(ctx, spec, sessionLabel(spec.SessionID))
}

// CreateInputs creates the config-input (one-way host -> remote) and transcript
// (one-way remote -> host) sync groups — the 7 non-load-bearing syncs. Split out
// of CreateAll so the connect path can run it off the foreground (§5). It is
// idempotent (an existing session is left as-is) and, like CreateAll, surfaces
// any real create failure so a backgrounding caller can observe it rather than
// have it vanish. The syncs carry the same session label as the project sync, so
// the GC and pause/resume/terminate-by-label continue to reach them unchanged.
func (m *Manager) CreateInputs(ctx context.Context, spec Spec) error {
	label := sessionLabel(spec.SessionID)

	// 1. Config inputs (one-way host -> remote)
	for _, sub := range ConfigInputsSubs {
		name := "sandbox-" + spec.SessionID + "-config-" + sub.Local
		localPath := path.Join(spec.HomeDir, ".claude", sub.Local)
		remotePath := path.Join(spec.RemoteClaude, sub.Remote)
		args := []string{
			"sync", "create",
			"--name=" + name,
			"--label", label,
		}
		args = append(args, m.contextLabelArgs()...)
		args = append(args, m.namespaceLabelArgs()...)
		args = append(args,
			"--mode=one-way-safe",
			localPath, spec.SSHHost+":"+remotePath,
		)
		if _, err := m.r.Output(ctx, nil, args...); err != nil {
			if !isMutagenAlreadyExists(err) {
				return fmt.Errorf("sync: create config-%s session: %w", sub.Local, err)
			}
		}
	}

	// 2. Transcripts (one-way remote -> host)
	for _, sub := range TranscriptSubs {
		name := "sandbox-" + spec.SessionID + "-transcripts-" + sub
		localPath := path.Join(spec.HomeDir, ".claude", sub)
		remotePath := path.Join(spec.RemoteClaude, sub)
		args := []string{
			"sync", "create",
			"--name=" + name,
			"--label", label,
		}
		args = append(args, m.contextLabelArgs()...)
		args = append(args, m.namespaceLabelArgs()...)
		args = append(args,
			"--mode=one-way-safe",
			spec.SSHHost+":"+remotePath, localPath,
		)
		if _, err := m.r.Output(ctx, nil, args...); err != nil {
			if !isMutagenAlreadyExists(err) {
				return fmt.Errorf("sync: create transcripts-%s session: %w", sub, err)
			}
		}
	}

	return nil
}

// buildTreeIgnores excludes large build/dependency trees that belong on each
// side independently. Listed FIRST among the ignore layers so a project's own
// .gitignore negation (e.g. `!vendor`) can override them — mutagen gives
// later patterns precedence.
var buildTreeIgnores = []string{
	"--ignore=node_modules",
	"--ignore=vendor",
	"--ignore=.venv",
	"--ignore=venv",
	"--ignore=__pycache__",
	"--ignore=.cache",
	"--ignore=dist",
	"--ignore=build",
	"--ignore=target",
}

// securityIgnores is the non-overridable boundary layer, appended AFTER the
// .gitignore-derived patterns so later-wins precedence means no project
// .gitignore negation can re-enable syncing them.
var securityIgnores = []string{
	// Secrets and credentials — never sync to the pod (C1).
	"--ignore=.env",
	"--ignore=.env.*",
	"--ignore=*.pem",
	"--ignore=*.key",
	"--ignore=*.p12",
	"--ignore=*.pfx",
	// Plaintext machine logins — host, registry, and git-remote credentials
	// looked up by exact filename (S2).
	"--ignore=.netrc",
	"--ignore=_netrc",
	"--ignore=.npmrc",
	"--ignore=.git-credentials",
	// Cloud credentials — the AWS CLI/SDK credential+config directory and
	// GCP-style service-account key files.
	"--ignore=.aws",
	"--ignore=service-account*.json",
	// SSH private keys under their default names (and derived files, e.g.
	// id_rsa.old backups).
	"--ignore=id_rsa",
	"--ignore=id_rsa.*",
	"--ignore=id_ed25519",
	"--ignore=id_ed25519.*",
	"--ignore=id_ecdsa",
	"--ignore=id_ecdsa.*",
	// Files that execute on the HOST without an explicit user action if the
	// pod agent writes them and two-way sync carries them back: direnv runs
	// .envrc/.direnv on cd (once allowed), VS Code tasks.json can auto-run on
	// folder open, .idea holds JetBrains run configurations. Makefile-class
	// files are deliberately NOT ignored — they run only on explicit user
	// action, and agents legitimately edit them.
	"--ignore=.envrc",
	"--ignore=.direnv",
	"--ignore=.vscode",
	"--ignore=.idea",
}

// createProjectSync creates the two-way-safe project sync for a session.
// Idempotent: an already-existing session ("session already exists") is left as-is
// and reported as created=false; only a fresh create reports created=true.
//
// Ignore layering (mutagen resolves later patterns with higher precedence):
// build trees, then the project root's .gitignore (see gitignoreIgnoreFlags),
// then the non-overridable security/auto-exec set.
func (m *Manager) createProjectSync(ctx context.Context, spec Spec, label string) (created bool, err error) {
	projectName := "sandbox-" + spec.SessionID + "-project"
	gitignoreFlags, err := gitignoreIgnoreFlags(spec.ProjectPath)
	if err != nil {
		return false, err
	}
	args := []string{
		"sync", "create",
		"--name=" + projectName,
		"--label", label,
	}
	args = append(args, m.contextLabelArgs()...)
	args = append(args, m.namespaceLabelArgs()...)
	args = append(args,
		// two-way-safe is data-preserving (M27): when only one side changed a
		// path, the change propagates; when BOTH sides changed the same path
		// (a conflict — e.g. the operator and the agent edit one file at once),
		// Mutagen does NOT pick a winner or clobber either copy. It halts
		// propagation for that path and surfaces it as a conflict, visible via
		// `mutagen sync list <name>` (and `mutagen sync monitor`). The operator
		// resolves it by removing the unwanted side's version, after which sync
		// resumes. (Contrast two-way-resolved, which would auto-pick the local
		// side and silently overwrite the pod's edit.)
		"--mode=two-way-safe",
		"--ignore-vcs",
	)
	args = append(args, buildTreeIgnores...)
	args = append(args, gitignoreFlags...)
	args = append(args, securityIgnores...)
	args = append(args, spec.ProjectPath, spec.SSHHost+":"+spec.RemotePath)
	if _, err := m.r.Output(ctx, nil, args...); err != nil {
		// C8: use a more specific substring to avoid swallowing unrelated failures.
		// Mutagen emits "session already exists" for idempotent re-creates → not an
		// error (a reconnect; the session reconciles on its own, created=false).
		if !isMutagenAlreadyExists(err) {
			return false, fmt.Errorf("sync: create project session: %w", err)
		}
		return false, nil
	}
	return true, nil
}

// Status returns mutagen's listing output for a session's sync sessions.
func (m *Manager) Status(ctx context.Context, sessionID string) ([]byte, error) {
	return m.r.Output(ctx, nil, "sync", "list", "--label-selector="+sessionLabel(sessionID))
}

// FlushAll forces a synchronization cycle for the session's sync sessions and
// blocks until it completes. `mutagen sync create` returns as soon as the
// session is registered — before the SSH transport is proven and before any
// files have staged — so the real failure modes (auth rejected, agent install
// failed, pod unreachable) and the initial project upload happen asynchronously
// and are invisible to the caller. Flushing surfaces those failures (RV20) and
// lets the first sync settle before the user starts working (RV21). Callers
// should bound this with a timeout: a broken transport errors fast, while a
// large-but-healthy first sync may legitimately take a while.
func (m *Manager) FlushAll(ctx context.Context, sessionID string) error {
	_, err := m.r.Output(ctx, nil, "sync", "flush", "--label-selector="+sessionLabel(sessionID))
	if err != nil && isMutagenNotFound(err) {
		return nil
	}
	return err
}

// PauseAll pauses all sync sessions for a session. A "not found" error (no
// sessions matched the selector, e.g. a session that was never synced) is
// treated as success, matching FlushAll/TerminateAll.
func (m *Manager) PauseAll(ctx context.Context, sessionID string) error {
	label := sessionLabel(sessionID)
	_, err := m.r.Output(ctx, nil, "sync", "pause", "--label-selector="+label)
	if err != nil && isMutagenNotFound(err) {
		return nil
	}
	return err
}

// ResumeAll resumes all sync sessions for a session. A "not found" error (no
// sessions matched the selector) is treated as success, matching
// FlushAll/TerminateAll.
func (m *Manager) ResumeAll(ctx context.Context, sessionID string) error {
	label := sessionLabel(sessionID)
	_, err := m.r.Output(ctx, nil, "sync", "resume", "--label-selector="+label)
	if err != nil && isMutagenNotFound(err) {
		return nil
	}
	return err
}

// Reconcile self-heals a session's sync sessions after a mid-session stall
// (MF5): it resumes any that a transient fault left paused, then forces a flush
// cycle to re-establish a wedged transport and surface real errors. Both steps
// are label-scoped and idempotent, so no Spec is needed and it is safe to call
// repeatedly — the prober-owning layer fires it (debounced) when it observes a
// SyncStalled reading while the SSE stream is still healthy, the case where
// nothing else re-runs sync setup.
//
// It heals a sync that still EXISTS but wedged; it does not recreate a fully
// terminated sync (that needs the connect path's Spec and happens on reconnect).
// A create re-run is a separate, already-idempotent path: `mutagen sync create`
// with an existing name reports "session already exists", which CreateAll/
// CreateProject/CreateInputs swallow as a no-op — so re-running create never
// errors, and Reconcile adds the resume+flush that actually un-sticks it.
func (m *Manager) Reconcile(ctx context.Context, sessionID string) error {
	if err := m.ResumeAll(ctx, sessionID); err != nil {
		return err
	}
	return m.FlushAll(ctx, sessionID)
}

// TerminateAll terminates all sync sessions for a session.
func (m *Manager) TerminateAll(ctx context.Context, sessionID string) error {
	label := sessionLabel(sessionID)
	_, err := m.r.Output(ctx, nil, "sync", "terminate", "--label-selector="+label)
	// C8: use specific mutagen "not found" message rather than bare "not found"
	// which could match an unrelated error.
	if err != nil && isMutagenNotFound(err) {
		return nil
	}
	return err
}

// SyncSession is a sandbox-owned mutagen sync session (one of the ~8 created per
// remote session), as reported by `mutagen sync list`. Used by the GC to find
// orphans whose pod endpoint is gone.
type SyncSession struct {
	SessionID  string // sandbox session id (from the sandbox-session label)
	Context    string // kube context that created it (sandbox-context label; "" for pre-MF3 syncs)
	Namespace  string // k8s namespace that created it (sandbox-namespace label; "" for pre-[V28] syncs)
	Identifier string // mutagen session identifier (sync_...)
	Name       string // mutagen session name (sandbox-<id>-<kind>)
	Status     string // mutagen status enum string (Watching, ConnectingBeta, Paused, …)
}

// syncListTemplate emits one line per session:
// sessionID|context|identifier|name|status|namespace. It reads the
// sandbox-session label so List can scope to THIS tool's k8s syncs (the
// lima-based system-config manager shares the same host mutagen daemon but labels
// its syncs sandbox-vm-id, which we must never touch), the sandbox-context label
// so GC can scope to the current cluster's syncs (MF3 — never reap a sync a
// different context created), and the sandbox-namespace label so GC can also
// scope to the current namespace ([V28] — never reap a sync a different namespace
// in the same context created). The namespace is LAST so a pre-[V28] daemon
// listing (5 fields) still parses, with an empty Namespace.
const syncListTemplate = `{{range .}}{{index .Labels "sandbox-session"}}|{{index .Labels "sandbox-context"}}|{{.Identifier}}|{{.Name}}|{{.Status}}|{{index .Labels "sandbox-namespace"}}{{"\n"}}{{end}}`

// List returns every mutagen sync session owned by THIS tool — those carrying a
// non-empty sandbox-session label. Sessions from other users of the same host
// daemon (notably the lima sandbox-vm-id syncs) are excluded. A daemon with none
// returns an empty slice (not an error).
func (m *Manager) List(ctx context.Context) ([]SyncSession, error) {
	out, err := m.r.Output(ctx, nil, "sync", "list", "--template", syncListTemplate)
	if err != nil {
		if isMutagenNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("sync: list sessions: %w", err)
	}
	return parseSyncList(out), nil
}

// parseSyncList parses syncListTemplate output, keeping only rows that carry a
// sandbox-session label (ours). Lima syncs (empty first field) and blank lines
// are dropped. A row with the wrong field count (output from a stale/other
// template shape) is skipped, never a crash — every consumer degrades safely on
// a skipped row: the GC selectors simply don't see it (conservative — an unseen
// sync is never reaped) and the client's same-dir collision scan omits its
// warning (its documented degrade-to-silence). A REAL daemon driven by
// syncListTemplate always emits 6 fields since [V28]: a label-less sync renders
// `{{index .Labels "sandbox-context"}}` / `sandbox-namespace` as an EMPTY field,
// not a missing one. A 5-field row (a pre-[V28] daemon template shape) is still
// accepted for backward compatibility, with an empty Namespace.
func parseSyncList(out []byte) []SyncSession {
	var sessions []SyncSession
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 6)
		if len(parts) < 5 || parts[0] == "" {
			continue // not ours (no sandbox-session label) or malformed (stale shape)
		}
		ns := ""
		if len(parts) == 6 {
			ns = parts[5]
		}
		sessions = append(sessions, SyncSession{
			SessionID:  parts[0],
			Context:    parts[1],
			Identifier: parts[2],
			Name:       parts[3],
			Status:     parts[4],
			Namespace:  ns,
		})
	}
	return sessions
}

// IsOrphanStatus reports whether a mutagen status means the remote (pod) endpoint
// is unreachable — the transport is down and mutagen is looping to reconnect a pod
// that is (most likely) gone. It is cluster-agnostic: a sync that cannot reach its
// pod is dead regardless of which cluster owned it. Paused (intentional, set by
// `suspend`) and the connected working states (Watching/Scanning/Reconciling/
// Staging/…) are treated as healthy.
func IsOrphanStatus(status string) bool {
	s := strings.ToLower(strings.TrimSpace(status))
	if strings.Contains(s, "paused") {
		return false
	}
	return strings.Contains(s, "connecting") || strings.Contains(s, "disconnected")
}

// IsPausedStatus reports whether a mutagen status is Paused — a suspend, an
// explicit `sandbox sync --pause`, or an out-of-band pause. [V35] A paused sync
// is healthy ONLY while its session still exists (it resumes on reattach); once
// the session is gone (e.g. `kubectl delete sandbox`), the paused sync is
// immortal to the IsOrphanStatus-only GC, so the GC treats a paused sync whose
// session is absent from the live set as reapable. Split from IsOrphanStatus so
// the transport-down signal stays orthogonal to the intentional-pause signal.
func IsPausedStatus(status string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(status)), "paused")
}

// TerminateByIdentifier terminates specific mutagen sync sessions by identifier —
// the GC's surgical teardown of orphaned syncs, distinct from TerminateAll (which
// targets every sync for one sandbox session by label). A "not found" (already
// gone) is treated as success.
func (m *Manager) TerminateByIdentifier(ctx context.Context, identifiers ...string) error {
	if len(identifiers) == 0 {
		return nil
	}
	args := append([]string{"sync", "terminate"}, identifiers...)
	_, err := m.r.Output(ctx, nil, args...)
	if err != nil && isMutagenNotFound(err) {
		return nil
	}
	return err
}

// isMutagenAlreadyExists reports whether the mutagen error indicates that a
// sync session with the given name already exists (idempotent create). Mutagen
// writes "session already exists" to stderr on duplicate create.
func isMutagenAlreadyExists(err error) bool {
	return strings.Contains(err.Error(), "session already exists")
}

// isMutagenNotFound reports whether a mutagen error means no sessions matched
// the label selector — normal when terminating a session that was never synced.
func isMutagenNotFound(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "no sessions found") ||
		strings.Contains(msg, "session not found") ||
		strings.Contains(msg, "unable to locate requested sessions")
}

func sessionLabel(sessionID string) string {
	return "sandbox-session=" + sessionID
}

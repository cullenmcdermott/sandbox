package cli

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"

	tea "charm.land/bubbletea/v2"

	"github.com/cullenmcdermott/sandbox/client/cred"
	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard"
)

// account_store.go — the CLI-side concrete dashboard.AccountStore. It is the
// single seam through which the TUI account picker touches the credential store,
// keeping Keychain access and secret bytes entirely in the CLI layer: the
// dashboard only ever receives dashboard.AccountInfo (metadata). It reuses the
// Stage 3 login logic (cred.ParseSetupToken / cred.ValidateConsoleKey) so the
// CLI `auth login` and the TUI add-account flow store credentials identically.

// dashboardAccountStore adapts a cred.Store to dashboard.AccountStore.
type dashboardAccountStore struct {
	store cred.Store
}

// newDashboardAccountStore builds the metadata-only account store the dashboard
// account picker uses. It returns nil (no account step — legacy shared-Secret
// behavior) when the underlying store can't be opened, e.g. no home dir: with no
// store there are no accounts, so the legacy path is the correct fallback. Once
// built, List() failures are surfaced by the picker (fail closed).
func newDashboardAccountStore() dashboard.AccountStore {
	store, err := newCredStore()
	if err != nil {
		return nil
	}
	return dashboardAccountStore{store: store}
}

// info projects a cred.Account (+ default flag) to the dashboard's metadata type.
func (s dashboardAccountStore) info(a cred.Account, def string) dashboard.AccountInfo {
	return dashboard.AccountInfo{
		ID:      a.ID,
		Label:   a.Label,
		Type:    string(a.Type),
		Default: a.ID == def,
	}
}

// ListAccounts implements dashboard.AccountStore. It reads only metadata.
func (s dashboardAccountStore) ListAccounts() ([]dashboard.AccountInfo, error) {
	accounts, err := s.store.List()
	if err != nil {
		return nil, err
	}
	def, _ := s.store.Default()
	out := make([]dashboard.AccountInfo, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, s.info(a, def))
	}
	return out, nil
}

// autoDefault sets id as the default when it is the only stored account, so a
// first TUI login is immediately usable without an extra step (mirrors
// runAuthLogin's auto-default).
func (s dashboardAccountStore) autoDefault(id string) {
	if accounts, err := s.store.List(); err == nil && len(accounts) == 1 {
		_ = s.store.SetDefault(id)
	}
}

// AddConsoleKey implements dashboard.AccountStore. It validates + normalizes the
// key via cred.ValidateConsoleKey (storing the returned normalized value, not the
// raw input) and stores it as a console account. Errors never echo the key.
func (s dashboardAccountStore) AddConsoleKey(label, key string) (dashboard.AccountInfo, error) {
	if label == "" {
		label = "console"
	}
	normalized, err := cred.ValidateConsoleKey(key)
	if err != nil {
		return dashboard.AccountInfo{}, err
	}
	acct := cred.NewAccount(label, cred.AccountConsole)
	if err := s.store.Add(acct, []byte(normalized)); err != nil {
		return dashboard.AccountInfo{}, fmt.Errorf("store account: %w", err)
	}
	s.autoDefault(acct.ID)
	def, _ := s.store.Default()
	return s.info(acct, def), nil
}

// SubscriptionLogin implements dashboard.AccountStore. It returns a
// tea.ExecCommand that runs `claude setup-token` with stdin/stderr on the real
// terminal (for the interactive browser auth) and stdout captured internally
// (which carries the final token), plus a finalize func that parses the captured
// token, stores it as a subscription account, and returns its metadata. The
// captured buffer never leaves this package — only metadata is returned.
func (s dashboardAccountStore) SubscriptionLogin(label string) (tea.ExecCommand, func() (dashboard.AccountInfo, error)) {
	if label == "" {
		label = "claude.ai"
	}
	// exec.Command defers PATH lookup to Start, so a missing `claude` surfaces as
	// a Run error routed to the dashboard's login-error surface, not a panic.
	ec := &captureStdoutExec{cmd: exec.Command("claude", "setup-token")}
	ec.cmd.Stdout = &ec.buf // captured; SetStdout below keeps it captured
	finalize := func() (dashboard.AccountInfo, error) {
		token, err := cred.ParseSetupToken(ec.buf.String())
		if err != nil {
			// cred.ParseSetupToken never embeds the raw output; keep it that way and
			// steer the user to the `sandbox auth login --subscription --paste`
			// fallback rather than surfacing the captured buffer.
			return dashboard.AccountInfo{}, fmt.Errorf("%w — re-run `sandbox auth login --subscription --paste` to paste the token `claude` printed", err)
		}
		acct := cred.NewAccount(label, cred.AccountSubscription)
		if err := s.store.Add(acct, []byte(token)); err != nil {
			return dashboard.AccountInfo{}, fmt.Errorf("store account: %w", err)
		}
		s.autoDefault(acct.ID)
		def, _ := s.store.Default()
		return s.info(acct, def), nil
	}
	return ec, finalize
}

// captureStdoutExec is a tea.ExecCommand that runs a subprocess with its stdout
// captured to an internal buffer while stdin/stderr are handed to the real
// terminal by Bubble Tea. This is the plan's "custom ExecCommand that keeps
// stdin/stderr on the terminal and swaps stdout for the buffer": SetStdout is a
// deliberate no-op so Bubble Tea's terminal writer never overwrites the capture.
// Deliberately NOT a pty (a pty wraps lines at its width and mirrors UI escapes
// into the capture, corrupting a token printed near the right edge).
type captureStdoutExec struct {
	cmd *exec.Cmd
	buf bytes.Buffer
}

func (c *captureStdoutExec) Run() error { return c.cmd.Run() }

// SetStdin wires the interactive browser-auth + paste-code input to the terminal.
func (c *captureStdoutExec) SetStdin(r io.Reader) { c.cmd.Stdin = r }

// SetStdout is intentionally ignored: stdout stays captured to the internal
// buffer (the token carrier). The passed terminal writer would otherwise splice
// the token into the visible UI.
func (c *captureStdoutExec) SetStdout(io.Writer) {}

// SetStderr wires the tool's interactive UI to the terminal (where it belongs).
func (c *captureStdoutExec) SetStderr(w io.Writer) { c.cmd.Stderr = w }

// compile-time: the store satisfies the dashboard interface.
var _ dashboard.AccountStore = dashboardAccountStore{}

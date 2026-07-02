package cli

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/cullenmcdermott/sandbox/internal/cred"
)

// setupTokenFunc runs the host `claude setup-token` subprocess and returns its
// captured stdout. It is a seam so login tests can fake the subprocess without a
// real `claude` on PATH. The captured stdout is treated as opaque token
// material: it is passed straight to cred.ParseSetupToken and never echoed.
type setupTokenFunc func(ctx context.Context) (string, error)

// secretPromptFunc reads one secret value from the terminal without echoing it.
// A seam so tests feed input without a real TTY. Implementations must never
// echo the value back.
type secretPromptFunc func(prompt string) (string, error)

// realSetupToken runs `claude setup-token` in the README-validated shape:
// stdin+stderr wired to the real terminal (for the interactive browser auth +
// paste-code), stdout captured to a buffer (which carries the final token).
// Deliberately NOT a pty — a pty wraps lines at its width and mirrors UI escape
// sequences into the capture, corrupting a token printed near the right edge.
// The captured stdout is never printed, even on error.
func realSetupToken(ctx context.Context) (string, error) {
	path, err := exec.LookPath("claude")
	if err != nil {
		return "", fmt.Errorf("`claude` CLI not found on PATH — install it, or run `sandbox auth login --console` to paste an API key (or `--subscription --paste` to paste a token): %w", err)
	}
	var stdout bytes.Buffer
	c := exec.CommandContext(ctx, path, "setup-token")
	c.Stdin = os.Stdin
	c.Stderr = os.Stderr
	c.Stdout = &stdout // captured; never echoed
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("`claude setup-token` failed: %w", err)
	}
	return stdout.String(), nil
}

// realSecretPrompt reads a secret from the controlling terminal without echoing
// it. When stdin is not a terminal (piped input, CI) it falls back to a plain
// line read — the value is still never echoed by this process. The prompt goes
// to stderr so it never pollutes captured stdout.
func realSecretPrompt(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr) // ReadPassword consumes the user's Enter without a newline
		if err != nil {
			return "", fmt.Errorf("read secret: %w", err)
		}
		return string(b), nil
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read secret: %w", err)
	}
	return line, nil
}

// authLoginOpts collects `auth login` flags.
type authLoginOpts struct {
	subscription bool
	console      bool
	paste        bool
	label        string
}

// runAuthLogin acquires and stores one Anthropic account. Exactly one of
// --subscription / --console must be set. On success it auto-sets the account as
// the default when it is the only one stored, then prints the account id, label,
// and type — never the secret. runSetupToken and prompt are injected seams.
func runAuthLogin(ctx context.Context, out io.Writer, store cred.Store, o authLoginOpts, runSetupToken setupTokenFunc, prompt secretPromptFunc) error {
	if o.subscription == o.console {
		return fmt.Errorf("exactly one of --subscription or --console is required")
	}
	var (
		acct cred.Account
		err  error
	)
	if o.subscription {
		acct, err = loginSubscription(ctx, store, o.label, o.paste, runSetupToken, prompt)
	} else {
		acct, err = loginConsole(store, o.label, prompt)
	}
	if err != nil {
		return err
	}
	// Auto-default when this is the only account, so a first login is
	// immediately usable by `sandbox claude` with no extra step.
	if accounts, lerr := store.List(); lerr == nil && len(accounts) == 1 {
		_ = store.SetDefault(acct.ID)
	}
	marker := ""
	if def, derr := store.Default(); derr == nil && def == acct.ID {
		marker = "  (default)"
	}
	fmt.Fprintf(out, "Added anthropic account %s  label=%q  type=%s%s\n", acct.ID, acct.Label, acct.Type, marker)
	return nil
}

// loginSubscription acquires a claude.ai subscription setup-token and stores it.
// With paste, it reads the token from the terminal (no echo) instead of running
// the subprocess. A parse failure never echoes the captured buffer and points at
// the --paste fallback.
func loginSubscription(ctx context.Context, store cred.Store, label string, paste bool, runSetupToken setupTokenFunc, prompt secretPromptFunc) (cred.Account, error) {
	if label == "" {
		label = "claude.ai"
	}
	var raw string
	var err error
	if paste {
		raw, err = prompt("Paste the claude setup token (sk-ant-oat…): ")
	} else {
		raw, err = runSetupToken(ctx)
	}
	if err != nil {
		return cred.Account{}, err
	}
	token, err := cred.ParseSetupToken(raw)
	if err != nil {
		// cred.ParseSetupToken never embeds the raw output; keep it that way and
		// steer the user to the paste fallback rather than dumping the buffer.
		return cred.Account{}, fmt.Errorf("%w — re-run `sandbox auth login --subscription --paste` to paste the token `claude` printed", err)
	}
	acct := cred.NewAccount(label, cred.AccountSubscription)
	if err := store.Add(acct, []byte(token)); err != nil {
		return cred.Account{}, fmt.Errorf("store account: %w", err)
	}
	return acct, nil
}

// loginConsole reads an Anthropic Console API key (no echo), validates its
// format, and stores the NORMALIZED key cred.ValidateConsoleKey returns (a
// trailing pasted newline would otherwise persist into the credential).
func loginConsole(store cred.Store, label string, prompt secretPromptFunc) (cred.Account, error) {
	if label == "" {
		label = "console"
	}
	raw, err := prompt("Paste the Anthropic Console API key (sk-ant-…): ")
	if err != nil {
		return cred.Account{}, err
	}
	key, err := cred.ValidateConsoleKey(raw)
	if err != nil {
		return cred.Account{}, err
	}
	acct := cred.NewAccount(label, cred.AccountConsole)
	if err := store.Add(acct, []byte(key)); err != nil {
		return cred.Account{}, fmt.Errorf("store account: %w", err)
	}
	return acct, nil
}

// runAuthList prints the stored accounts as a table (id, label, type, age,
// default marker). It reads only metadata — never secret bytes — and prints a
// friendly message when none are stored.
func runAuthList(out io.Writer, store cred.Store) error {
	accounts, err := store.List()
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		fmt.Fprintln(out, "No anthropic accounts stored. Add one with `sandbox auth login --subscription` or `--console`.")
		return nil
	}
	def, _ := store.Default()
	fmt.Fprintf(out, "%-18s %-18s %-14s %-10s %s\n", "ID", "LABEL", "TYPE", "AGE", "DEFAULT")
	for _, a := range accounts {
		marker := ""
		if a.ID == def {
			marker = "*"
		}
		fmt.Fprintf(out, "%-18s %-18s %-14s %-10s %s\n", a.ID, a.Label, a.Type, humanAge(a.CreatedAt), marker)
	}
	return nil
}

// sessionLister enumerates live sessions still holding a copy of an account's
// credential. A seam so logout is testable and degrades when the cluster is
// unreachable.
type sessionLister func(ctx context.Context, accountID string) ([]string, error)

// runAuthLogout removes an account locally and reports which live sessions still
// hold a copy of its credential (via the account label on per-session Secrets).
// The local removal always succeeds even if the cluster is unreachable — the
// session lookup degrades to a warning. It prints the honest message that local
// removal neither scrubs per-session copies nor revokes the credential at
// Anthropic.
func runAuthLogout(ctx context.Context, out io.Writer, store cred.Store, listSessions sessionLister, selector string) error {
	accounts, err := store.List()
	if err != nil {
		return err
	}
	acct, err := resolveAccount(accounts, selector)
	if err != nil {
		return err
	}
	if err := store.Remove(acct.ID); err != nil {
		return fmt.Errorf("remove account: %w", err)
	}
	fmt.Fprintf(out, "Removed local anthropic account %s (label %q).\n", acct.ID, acct.Label)

	if listSessions != nil {
		if ids, lerr := listSessions(ctx, acct.ID); lerr != nil {
			fmt.Fprintf(out, "warning: could not check live sessions for copies of this credential: %v\n", lerr)
		} else if len(ids) > 0 {
			fmt.Fprintf(out, "\n%d live session(s) still hold a copy of this credential:\n", len(ids))
			for _, id := range ids {
				fmt.Fprintf(out, "  %s\n", id)
			}
		}
	}
	fmt.Fprintln(out, "\nNote: local removal does not scrub per-session Secret copies, and running pods keep the\n"+
		"env var regardless. To cut access, revoke the token/key at Anthropic; to rotate, re-login\n"+
		"and update + suspend/resume the affected sessions.")
	return nil
}

// runAuthDefault resolves selector to an account and records it as the default.
func runAuthDefault(out io.Writer, store cred.Store, selector string) error {
	accounts, err := store.List()
	if err != nil {
		return err
	}
	acct, err := resolveAccount(accounts, selector)
	if err != nil {
		return err
	}
	if err := store.SetDefault(acct.ID); err != nil {
		return fmt.Errorf("set default: %w", err)
	}
	fmt.Fprintf(out, "Default anthropic account set to %s (label %q).\n", acct.ID, acct.Label)
	return nil
}

// humanAge renders an account's age compactly for the list table. The
// setup-token expiry is opaque (not a JWT), so age from CreatedAt is the signal
// shown instead.
func humanAge(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func newAuthLoginCmd() *cobra.Command {
	var o authLoginOpts
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Add an Anthropic account (claude.ai subscription or Console API key)",
		Long: "Add a stored Anthropic account, usable by `sandbox claude --account` and the\n" +
			"TUI account picker. --subscription runs the host `claude setup-token` (browser\n" +
			"login) and captures the token; --console prompts for a pasted API key. Exactly\n" +
			"one is required. The secret is stored locally (macOS Keychain, else a 0600\n" +
			"file) and never printed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := newCredStore()
			if err != nil {
				return err
			}
			return runAuthLogin(cmd.Context(), cmd.OutOrStdout(), store, o, realSetupToken, realSecretPrompt)
		},
	}
	cmd.Flags().BoolVar(&o.subscription, "subscription", false, "log in with a claude.ai subscription via `claude setup-token`")
	cmd.Flags().BoolVar(&o.console, "console", false, "log in with an Anthropic Console API key (pasted)")
	cmd.Flags().BoolVar(&o.paste, "paste", false, "with --subscription, paste the setup token instead of running `claude setup-token`")
	cmd.Flags().StringVar(&o.label, "label", "", "display label (default \"claude.ai\" for --subscription, \"console\" for --console)")
	cmd.MarkFlagsMutuallyExclusive("subscription", "console")
	return cmd
}

func newAuthListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List stored Anthropic accounts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := newCredStore()
			if err != nil {
				return err
			}
			return runAuthList(cmd.OutOrStdout(), store)
		},
	}
}

func newAuthLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout <id|label>",
		Short: "Remove a stored Anthropic account (local only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := newCredStore()
			if err != nil {
				return err
			}
			// Best-effort cluster lookup: a per-session copy report is informative,
			// not required — logout must still succeed if the cluster is unreachable.
			listSessions := func(ctx context.Context, accountID string) ([]string, error) {
				backend, berr := newBackend()
				if berr != nil {
					return nil, berr
				}
				return backend.SessionsForAccount(ctx, accountID)
			}
			return runAuthLogout(cmd.Context(), cmd.OutOrStdout(), store, listSessions, args[0])
		},
	}
}

func newAuthDefaultCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "default <id|label>",
		Short: "Set the default Anthropic account for new sessions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := newCredStore()
			if err != nil {
				return err
			}
			return runAuthDefault(cmd.OutOrStdout(), store, args[0])
		},
	}
}

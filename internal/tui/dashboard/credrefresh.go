package dashboard

// credrefresh.go — mid-session credential-plugin handover (TODO §8 P1-0a).
//
// Interactive kubeconfig `exec:` credential plugins (e.g. Teleport's `tsh kube
// credentials`) work fine at startup: client-go runs them lazily, before
// tea.NewProgram claims the terminal, so they get a real TTY. The gap is
// expiry WHILE THE DASHBOARD IS RUNNING — the dashboard makes live cluster
// calls for its whole lifetime, and a plugin's cert TTL is easily shorter than
// a left-open session. The plugin then tries to prompt underneath the
// dashboard's alt-screen raw-mode program: auth silently fails, or the display
// corrupts.
//
// The fix mirrors the existing subscription-login handover exactly
// (account_picker.go's startSubscriptionLogin / accountLoginDoneMsg): hand the
// terminal to the credential helper via tea.Exec, then retry the cluster call
// by re-driving the same seed+watch the `r` retry key uses.

import tea "charm.land/bubbletea/v2"

// CredentialRefresher hands the terminal back to an interactive credential
// helper — a kubeconfig `exec:` plugin such as `tsh kube credentials` — when a
// live cluster call fails because the credential expired mid-session. Injected
// through RunOptions; nil disables the handover entirely (the failure just
// surfaces as it does today).
type CredentialRefresher interface {
	// NeedsRefresh reports whether err is an expired-credential failure this
	// refresher can actually fix. It must be conservative: a false positive
	// suspends the UI to run a subprocess for an unrelated error.
	NeedsRefresh(err error) bool

	// Refresh returns the command to run with the terminal handed over (the
	// dashboard is suspended for its duration) plus a finalize func run after it
	// exits successfully.
	Refresh() (tea.ExecCommand, func() error)
}

// credRefreshDoneMsg reports the outcome of the credential-plugin terminal
// handover (tea.Exec). A nil err means the plugin ran and finalize succeeded;
// the cluster error that triggered the handover is not retried directly —
// App.Update re-seeds and restarts the watch instead (handleCredRefreshDone),
// exactly like the dashboard's own `r` retry key.
type credRefreshDoneMsg struct{ err error }

// maxCredRefreshAttempts caps consecutive terminal handovers, so a credential
// that keeps failing cannot thrash the terminal indefinitely.
//
// The loop it prevents is real and only needs the plugin to exit ZERO while
// leaving the credential still bad (logged into the wrong cluster, an SSO
// session for the wrong role): the handover "succeeds", handleCredRefreshDone
// re-seeds, the seed fails the same way, and — with nothing but the in-flight
// latch guarding it — we hand the terminal over again, forever. A cancelled
// login is already safe (non-zero exit ⇒ we surface the error and do not
// re-seed); this covers the other half.
//
// The counter resets only on an OBSERVED CLUSTER SUCCESS (see credErrorFrom's
// second return), never on a successful handover — "the plugin ran" is not
// evidence that the credential now works, and treating it as such is exactly
// what would re-open the loop.
const maxCredRefreshAttempts = 2

// credErrorFrom extracts the underlying error from the cluster-outcome messages
// the App/Model already carry, so the credential-handover check in App.Update
// can run generically ahead of each message's normal dispatch.
//
// The second return says whether msg REPORTS A CLUSTER OUTCOME at all, which is
// what separates "this call succeeded" from "this message says nothing about
// the cluster". Only the former re-arms the handover budget: a connect stage
// tick or a keypress is not evidence the credential works.
func credErrorFrom(msg tea.Msg) (err error, outcome bool) {
	switch m := msg.(type) {
	case seedFailedMsg:
		return m.err, true
	case actionResultMsg:
		return m.err, true
	case attachFailedMsg:
		return m.err, true
	case connectUpdateMsg:
		switch {
		case m.failed != nil:
			return m.failed.err, true
		case m.ready != nil:
			return nil, true // the connect completed — the credential works
		default:
			return nil, false // a stage/detail progress tick, not an outcome
		}
	}
	return nil, false
}

// startCredRefresh hands the terminal to the injected CredentialRefresher via
// tea.Exec (the dashboard is suspended for its duration), mirroring
// startSubscriptionLogin's tea.Exec / accountLoginDoneMsg handover exactly.
func (a *App) startCredRefresh() tea.Cmd {
	execCmd, finalize := a.credRefresher.Refresh()
	return tea.Exec(execCmd, func(runErr error) tea.Msg {
		if runErr != nil {
			return credRefreshDoneMsg{err: runErr}
		}
		return credRefreshDoneMsg{err: finalize()}
	})
}

// handleCredRefreshDone clears the credRefreshing latch and either re-drives
// the cluster (re-seed + restart the watch, the same pair the `r` retry key
// uses — see dashListTable in model_input.go) on success, or records the
// handover's own failure so it surfaces in the detail pane alongside — via the
// same connectErr field — a connector failure.
func (a *App) handleCredRefreshDone(msg credRefreshDoneMsg) (tea.Model, tea.Cmd) {
	a.credRefreshing = false
	if msg.err != nil {
		a.credRefreshErr = msg.err
		a.connectErr = msg.err
		a.dashboard.connectErr = msg.err
		return a, nil
	}
	a.credRefreshErr = nil
	a.dashboard.seedErr = nil
	return a, tea.Batch(a.dashboard.seedCmd(), a.dashboard.startWatchCmd())
}

package dashboard

// leader.go — the pure leader-chord decision for the external (PTY) pane.
//
// ctrl+] was already the reserved detach key for external panes, so extending it
// into a leader chord gives those panes attention-nav (jump next/prev) without
// stealing any key the embedded client (e.g. opencode) might itself bind: nothing
// changes until the user first presses ctrl+], and a lone ctrl+] still means
// detach. Keeping the decision a pure function (repo pattern: classifiers like
// classifyForwardReconnect in the port-forward code) makes every branch testable
// without a live PTY. See TODO §2d.

import "time"

// leaderAction is the decision for a key press given the external pane's
// leader-chord state (ctrl+] arms a leader; see TODO §2d).
type leaderAction int

const (
	leaderIgnore   leaderAction = iota // not armed, not a leader key: normal forwarding path
	leaderArm                          // arm the leader; swallow the keypress
	leaderDetach                       // detach to the dashboard now
	leaderJumpNext                     // jump to next session needing attention
	leaderJumpPrev                     // jump to previous session needing attention
	leaderForward                      // disarm; forward THIS key to the child (the arming ctrl+] is never forwarded)
)

// leaderTimeout is how long an armed leader waits before a lone ctrl+]
// resolves to detach.
const leaderTimeout = 500 * time.Millisecond

// leaderStep maps a key press (tea.KeyPressMsg.String()) to a leaderAction given
// whether the leader is currently armed.
//
// When not armed, only ctrl+] (a.k.a. ctrl+4 — the same terminal byte) arms the
// leader; every other key takes the normal forwarding path. When armed, a second
// ctrl+] detaches, "g"/"k" jump to the next/previous session needing attention,
// and anything else disarms and forwards to the child (the original ctrl+] is
// never itself forwarded).
func leaderStep(armed bool, key string) leaderAction {
	if !armed {
		switch key {
		case "ctrl+]", "ctrl+4":
			return leaderArm
		default:
			return leaderIgnore
		}
	}
	switch key {
	case "ctrl+]", "ctrl+4":
		return leaderDetach
	case "g":
		return leaderJumpNext
	case "k":
		return leaderJumpPrev
	default:
		return leaderForward
	}
}

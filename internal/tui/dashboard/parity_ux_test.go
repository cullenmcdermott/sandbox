package dashboard

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// parity_ux_test.go — Phase F of docs/archive/testing-parity-plan.md: the cross-backend
// UX-PARITY BAR. Every backend emits the SAME normalized events and the dashboard
// renders them through ONE backend-agnostic transcript, so "no second-class
// backends" is testable: the SAME events/keys must produce the SAME affordances for
// every backend. These tests fail if someone adds backend-specific branching that
// breaks metrics/interrupt/keybinding parity. A new backend (Codex) is covered by
// appending to backendTranscriptCases (shared with golden_multiturn_test.go).
//
// Scope note: the status line carries NO backend identifier, so metrics parity is a
// byte-equality assertion. The transcript BODY intentionally shows "via <backend>"
// in the turn footer (a correct, wanted distinction), so body rendering is guarded
// per-backend by golden_multiturn_test.go rather than cross-backend equality.
// Startup-connecting and detach are App-level and currently route opencode to the
// external PTY pane — their parity is gated on the Phase E routing decision.

// newBackendTranscript builds a sized, laid-out transcript labeled for `backend`.
func newBackendTranscript(t *testing.T, backend string, fc RunnerClient) *TranscriptModel {
	t.Helper()
	m := NewTranscript(fc, Session{
		State: session.State{ID: "alpha", ProjectPath: "/work/alpha", Backend: backend},
		Title: "alpha",
	}, nil)
	m.width, m.height = 100, 30
	m.layout()
	return m
}

// metricsEventStream is one fixed metrics sequence (model · workspace · usage/cost ·
// rate-limit) fed identically to every backend. The runner observer feeds these for
// EVERY backend, so the status line must render them identically.
func metricsEventStream() []session.Event {
	return []session.Event{
		mkEvent(session.EventSessionStarted, session.SessionStartedPayload{Model: "claude-opus-4-8", Cwd: "/work/alpha"}),
		mkEvent(session.EventWorkspaceStatus, session.WorkspaceStatusPayload{Branch: "main", Dirty: true, Ahead: 1}),
		mkEvent(session.EventUsageUpdated, session.UsagePayload{InputTokens: 5000, OutputTokens: 2000, CacheReadTokens: 1000, CacheWriteTokens: 500, TotalCostUSD: 0.0123}),
		mkEvent(session.EventRateLimitUpdated, session.RateLimitPayload{
			Available: true, SubscriptionType: "max",
			FiveHourUtil: 42, SevenDayUtil: 30,
			FiveHourResetsAt: "2030-06-21T15:00:00Z", SevenDayResetsAt: "2030-06-25T00:00:00Z",
		}),
	}
}

// assertBackendsIdentical drives `scenario` against a fresh transcript for EVERY
// backend, renders `view`, and fails if any backend's output differs.
func assertBackendsIdentical(t *testing.T, what string, scenario func(m *TranscriptModel), view func(m *TranscriptModel) string) {
	t.Helper()
	var first, firstName string
	for i, bc := range backendTranscriptCases {
		m := newBackendTranscript(t, bc.backend, &fakeRunnerClient{})
		scenario(m)
		got := stripANSI(view(m))
		if i == 0 {
			first, firstName = got, bc.name
			continue
		}
		if got != first {
			t.Errorf("%s differs between %s and %s (no second-class backends):\n--- %s ---\n%s\n--- %s ---\n%s",
				what, firstName, bc.name, firstName, first, bc.name, got)
		}
	}
}

// Status-line / metrics parity: ctx% · usage · rate_limit · model · workspace render
// identically for every backend (the status line carries no backend label).
func TestUXParityStatusLineMetrics(t *testing.T) {
	withDeterministicRender(t, func() {
		assertBackendsIdentical(t, "status line",
			func(m *TranscriptModel) {
				for _, ev := range metricsEventStream() {
					m.handleEvent(ev)
				}
				m.layout()
			},
			func(m *TranscriptModel) string { return m.renderStatusLine() },
		)
	})
}

// Interrupt parity: esc during an active turn interrupts THAT exact turn for every
// backend (the parity bar's interrupt key; targets the runner turn id from
// turn.started so the /interrupt route matches).
func TestUXParityInterrupt(t *testing.T) {
	for _, bc := range backendTranscriptCases {
		t.Run(bc.name, func(t *testing.T) {
			fc := &fakeRunnerClient{}
			m := newBackendTranscript(t, bc.backend, fc)
			m.handleEvent(session.Event{Type: session.EventTurnStarted, TurnID: "turn-7"})
			if !m.turnActive {
				t.Fatal("turn.started did not mark the turn active")
			}
			_, cmd := m.handleKey(keyMsg("esc"))
			if cmd == nil {
				t.Fatal("esc during an active turn produced no interrupt command")
			}
			cmd()
			if fc.interrupts != 1 {
				t.Fatalf("interrupts = %d, want 1", fc.interrupts)
			}
			if len(fc.interruptRefs) != 1 || fc.interruptRefs[0].Turn != "turn-7" {
				t.Fatalf("interrupt targeted %+v, want turn-7", fc.interruptRefs)
			}
		})
	}
}

// Keybinding parity: the vim NORMAL/INSERT mode switches behave identically for
// every backend (the transcript keymap is backend-independent — this guards it).
func TestUXParityVimModeSwitch(t *testing.T) {
	for _, bc := range backendTranscriptCases {
		t.Run(bc.name, func(t *testing.T) {
			m := newBackendTranscript(t, bc.backend, &fakeRunnerClient{})
			m.setVim(true) // NORMAL
			m.handleKey(keyMsg("i"))
			if m.imode != modeInsert {
				t.Fatalf("'i' in NORMAL: imode = %v, want modeInsert", m.imode)
			}
			m.handleKey(keyMsg("esc"))
			if m.imode != modeNormal {
				t.Fatalf("esc in INSERT: imode = %v, want modeNormal", m.imode)
			}
		})
	}
}

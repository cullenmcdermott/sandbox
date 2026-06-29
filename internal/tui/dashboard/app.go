package dashboard

import (
	"context"
	"fmt"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/charmbracelet/x/ansi"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/terminal"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// Screen identifies the active screen in the root App model.
type Screen int

const (
	// ScreenDashboard is the default command-center list+detail view.
	ScreenDashboard Screen = iota
	// ScreenTranscript is the per-session transcript view (Phase B).
	ScreenTranscript
	// ScreenConnecting is a transient screen shown while the connector runs.
	ScreenConnecting
	// ScreenExternal hands the terminal to an external full-screen client
	// (opencode attach) for opencode-server sessions.
	ScreenExternal
)

// --------------------------------------------------------------------------
// Screen-switch messages
// --------------------------------------------------------------------------

// attachMsg is sent when the user requests to attach to a session. It carries
// the session to attach to; the App kicks off a Connector call and transitions
// to ScreenConnecting until the result arrives.
type attachMsg struct {
	sess Session
}

// attachReadyMsg is returned by the connector Cmd on success. The App
// transitions to the correct pane (transcript for claude-sdk, external PTY for
// opencode-server) and initialises it.
type attachReadyMsg struct {
	sess          Session
	client        RunnerClient
	reconnect     ReconnectFunc
	endpoint      string
	opencodeCreds *OpencodeCreds
	// warning is a non-fatal advisory (e.g. sync failure) to surface in the
	// transcript as an info block so it is visible in the alt-screen TUI (C9).
	warning string
}

// attachFailedMsg is returned when the connector fails. The App stays on (or
// returns to) ScreenDashboard and shows the error in the detail pane.
type attachFailedMsg struct {
	sess session.Ref
	err  error
}

// detachMsg is sent by the transcript model when the user presses esc. The
// App transitions back to ScreenDashboard with state intact.
type detachMsg struct{}

// externalPaneFinishedMsg is returned when the opencode attach subprocess
// exits, so the App can return to the dashboard.
type externalPaneFinishedMsg struct {
	err error
}

// connectUpdateMsg is one item from the connect goroutine's progress channel.
// Exactly one of stage/ready/failed is non-nil.
type connectUpdateMsg struct {
	stage  *ConnectStage    // progress tick — connector entered a new stage
	detail string           // optional live sub-status for the current stage ("" = none)
	ready  *attachReadyMsg  // success (terminal)
	failed *attachFailedMsg // failure (terminal)
}

// connectTickMsg drives the connecting-screen spinner animation.
type connectTickMsg struct{}

// --------------------------------------------------------------------------
// App
// --------------------------------------------------------------------------

// App is the root Bubble Tea v2 model for the sandbox command center. It owns
// the screen enum, both child models, and the Connector used for attaching to
// sessions. All Update and View calls are delegated to the active screen.
type App struct {
	screen     Screen
	dashboard  *Model
	transcript *TranscriptModel // nil until first attach
	external   *ExternalPane    // nil unless attached to an opencode-server session

	// lastProgress is the OSC 9;4 tab-progress state last emitted to the terminal.
	// App.Update compares the live session aggregate against it and emits a tea.Raw
	// only on a transition, so each state change writes exactly once (and idle goes
	// quiet) instead of re-emitting every frame (Stage 2). It MUST ride tea.Raw,
	// not View content: Bubble Tea v2's cell renderer drops control strings spliced
	// into a frame (same reason the desktop notification + Kitty graphics use Raw).
	// ProgressNone (the zero value) off-Ghostty / under ReduceMotion, so this is
	// only ever meaningfully non-None on a Ghostty terminal.
	lastProgress terminal.Progress

	// picker is the new-session backend chooser overlay (`n`). When open it is
	// rendered over the dashboard and intercepts key input.
	picker backendPicker

	// connector is called in a Cmd to establish a live runner connection.
	// It is set by Run/NewApp; nil means attach is disabled (unit-test mode).
	connector Connector

	// creator provisions a brand-new session for the `n` key. nil disables
	// new-session (unit-test mode); the dashboard surfaces an error inline.
	creator Creator

	// connectingFor is the session being connected to (shown in the
	// ScreenConnecting placeholder).
	connectingFor *Session

	// connectingPreview is a read-only transcript built from warm history or the
	// host-side cache at attach time, rendered behind the connect banner so the
	// user sees their conversation immediately during a (possibly slow cold-pod)
	// resume instead of a blank splash (Fix A). nil when there's nothing cached to
	// show, or for opencode sessions (no Go transcript). On a successful attach it
	// is promoted to the live foreground transcript.
	connectingPreview *TranscriptModel

	// modalBackdrop caches the dimmed dashboard backdrop composited behind the
	// transcript modal. A transcript keystroke is never delegated to the dashboard
	// (see the delegation guard in Update), so the dashboard's render is unchanged
	// between keystrokes — caching the dimmed backdrop skips a full dashboard
	// re-render + per-line dim pass on every keystroke (Fix E). Invalidated
	// whenever the dashboard is actually delegated a message, or the size changes.
	modalBackdrop      string
	modalBackdropW     int
	modalBackdropH     int
	modalBackdropValid bool
	// bdBuilds counts backdrop (re)builds — a behavioral counter the Fix E test
	// asserts on to prove the backdrop is reused across keystrokes.
	bdBuilds int

	// connectStage is the latest ConnectStage reported by the connector (U1).
	connectStage ConnectStage

	// connectStartedAt is when the current connect/create began; the connecting
	// splash shows the elapsed time so a slow cold-pod resume reads as progress
	// rather than a freeze. Zero when not connecting. (Mirrors the transcript
	// reconnect header's elapsed timer.)
	connectStartedAt time.Time

	// connectDetail is the latest live sub-status for the current stage (e.g.
	// "uploading" during the initial file sync); "" when there is none.
	connectDetail string

	// connectingOpencode records whether the in-flight connect is for an
	// opencode-server session, so the connecting stepper shows the extra
	// StageOpencode ("Starting opencode") step. Set when a connect/create begins.
	connectingOpencode bool

	// connectFrame is the spinner frame index for the connecting screen (U1).
	connectFrame int

	// connectCh is the update stream for the in-flight connect goroutine (U1).
	// Nil when not connecting.
	connectCh chan connectUpdateMsg

	// connectCancel cancels the in-flight connect goroutine (U1). Called on key
	// press in ScreenConnecting.
	connectCancel context.CancelFunc

	// connectErr holds the last connector error, shown in the detail pane.
	connectErr error

	// autoAttach, when non-nil, makes the App open straight into a session's
	// transcript on launch (used by `sandbox claude` / `sandbox attach`), with
	// the dashboard list loading underneath so esc still returns to it.
	autoAttach *Session

	// initialPrompt is submitted automatically once the auto-attached transcript
	// is live (the prompt passed to `sandbox claude "…"`). Consumed once.
	initialPrompt string

	// Terminal size is propagated to child models via WindowSizeMsg.
	width  int
	height int

	// parkedTranscripts holds the lightweight view/input state saved when the user
	// detaches from a session (B3). On re-attach to the same session, the state is
	// restored into the new TranscriptModel so compose buffers, queued prompts, and
	// search queries survive a detach→reattach cycle.
	parkedTranscripts map[session.ID]ParkedTranscriptState
}

// NewApp constructs the root App with a dashboard backed by the given k8s
// Backend. connector may be nil (attach will be a no-op / for unit tests).
func NewApp(backend *k8s.Backend, connector Connector, creator Creator) *App {
	dash := New(backend)
	if connector != nil {
		dash.WithConnector(connector)
	}
	return &App{
		screen:            ScreenDashboard,
		dashboard:         dash,
		connector:         connector,
		creator:           creator,
		parkedTranscripts: make(map[session.ID]ParkedTranscriptState),
	}
}

// RunOptions configures optional behavior for Run/RunAttached.
type RunOptions struct {
	// DestroyHook is called after a successful session destroy so the caller
	// can perform irreversible local cleanup (SSH alias removal, key deletion,
	// index removal). Corresponds to C2 fix.
	DestroyHook func(id session.ID)

	// PreDestroyHook is called before backend.Destroy so the caller can stop
	// file sync ahead of pod teardown, avoiding mutagen-over-SSH EOF errors as
	// the pod disappears.
	PreDestroyHook func(id session.ID)

	// TitleStore persists user-chosen session titles across restarts (T5).
	TitleStore TitleStore

	// SnapshotStore persists the per-session live read-model so the dashboard
	// hydrates instantly on launch and resumes the SSE stream from the cached seq
	// instead of replaying the full event history.
	SnapshotStore SnapshotStore

	// EventCache persists each foreground session's transcript events host-side so
	// a cold re-attach rebuilds the conversation instantly and streams only the
	// delta (Workstream C).
	EventCache EventCache

	// ObserverConnector is the lightweight connect path for background passive
	// status streams (port-forward + runner health, no file-sync setup). When
	// nil, background streams use Connector.
	ObserverConnector Connector

	// SyncProber reports per-session sync health for the dashboard indicator.
	SyncProber SyncProber

	// IdleTimeout is the reaper idle-timeout, used to render the "suspends in"
	// hint for warm sessions. Zero hides the hint.
	IdleTimeout time.Duration
}

// applyOpts threads RunOptions into the dashboard model.
func (a *App) applyOpts(opts []RunOptions) {
	if len(opts) == 0 {
		return
	}
	if opts[0].DestroyHook != nil {
		a.dashboard = a.dashboard.WithDestroyHook(opts[0].DestroyHook)
	}
	if opts[0].PreDestroyHook != nil {
		a.dashboard = a.dashboard.WithPreDestroyHook(opts[0].PreDestroyHook)
	}
	if opts[0].TitleStore != nil {
		a.dashboard = a.dashboard.WithTitleStore(opts[0].TitleStore)
	}
	if opts[0].SnapshotStore != nil {
		a.dashboard = a.dashboard.WithSnapshotStore(opts[0].SnapshotStore)
	}
	if opts[0].EventCache != nil {
		a.dashboard = a.dashboard.WithEventCache(opts[0].EventCache)
	}
	if opts[0].ObserverConnector != nil {
		a.dashboard = a.dashboard.WithObserverConnector(opts[0].ObserverConnector)
	}
	if opts[0].SyncProber != nil {
		a.dashboard = a.dashboard.WithSyncProber(opts[0].SyncProber)
	}
	if opts[0].IdleTimeout > 0 {
		a.dashboard = a.dashboard.WithIdleTimeout(opts[0].IdleTimeout)
	}
}

// Run starts the Bubble Tea program with the root App model and returns when
// the user quits. connector provides live runner connections for attach.
func Run(backend *k8s.Backend, connector Connector, creator Creator, opts ...RunOptions) error {
	app := NewApp(backend, connector, creator)
	app.applyOpts(opts)
	p := tea.NewProgram(app)
	_, err := p.Run()
	return err
}

// RunAttached starts the command center already attached to one session's
// transcript — the entry point for `sandbox claude` and `sandbox attach`. The
// dashboard list still loads underneath, so pressing esc detaches to the full
// session list rather than quitting. initialPrompt, if non-empty, is submitted
// as the first turn once the transcript is live.
func RunAttached(backend *k8s.Backend, connector Connector, creator Creator, sess Session, initialPrompt string, opts ...RunOptions) error {
	app := NewApp(backend, connector, creator)
	app.applyOpts(opts)
	app.autoAttach = &sess
	app.initialPrompt = initialPrompt
	p := tea.NewProgram(app)
	_, err := p.Run()
	return err
}

// --------------------------------------------------------------------------
// tea.Model interface
// --------------------------------------------------------------------------

// Init initialises the active screen and delegates init commands. When the App
// was built with an auto-attach target, it also fires an attachMsg so the
// program opens straight into that session's transcript.
func (a *App) Init() tea.Cmd {
	// Ask the terminal for its background color so the palette can adapt to a
	// light or dark theme (handled in Update via theme.ApplyTheme).
	cmds := []tea.Cmd{a.dashboard.Init(), tea.RequestBackgroundColor}
	if a.autoAttach != nil {
		sess := *a.autoAttach
		cmds = append(cmds, func() tea.Msg { return attachMsg{sess: sess} })
	}
	return tea.Batch(cmds...)
}

// Update routes messages to the active screen. Global keys (quit) are
// intercepted before delegation.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		if a.external != nil {
			// Resize the emulator + child PTY (sends SIGWINCH) so the embedded
			// opencode TUI repaints at the new size, even while minimized.
			a.external.resize(msg.Width, msg.Height)
		}
		// Fall through so child models receive the size too.

	case ptyOutputMsg:
		// PTY output from an embedded external pane. Handled at the top level —
		// not gated on the active screen — so the emulator stays current and the
		// reader keeps draining even while the pane is minimized (which is what
		// keeps the child from blocking and makes re-open instant).
		if a.external == nil || msg.pane != a.external {
			return a, nil // stale pane (replaced/closed); drop its trailing reads
		}
		cmd, finished := a.external.apply(msg.chunk)
		if finished {
			return a, func() tea.Msg { return externalPaneFinishedMsg{err: a.external.err} }
		}
		return a, cmd

	case tea.BackgroundColorMsg:
		// Adapt the whole palette to the detected terminal background. theme.ApplyTheme
		// rebuilds the shared styles, so the next render of every screen adapts.
		theme.ApplyForBackground(msg.IsDark())
		return a, nil

	case tea.KeyPressMsg:
		// Ctrl+C is always fatal regardless of screen.
		if msg.String() == "ctrl+c" {
			a.dashboard.Cancel()
			return a, tea.Quit
		}
		// Any key press in ScreenConnecting cancels the in-flight connection (U1).
		if a.screen == ScreenConnecting {
			if a.connectCancel != nil {
				a.connectCancel()
				a.connectCancel = nil
				a.connectCh = nil
			}
			a.connectingFor = nil
			a.connectingPreview = nil
			a.connectStartedAt = time.Time{}
			a.screen = ScreenDashboard
			return a, nil
		}
		// The backend picker, when open, owns key input (over the dashboard).
		if a.picker.open {
			if cmd, consumed := a.pickerKey(msg); consumed {
				return a, cmd
			}
			return a, nil
		}

	// ---- Screen-switch messages ----

	case attachMsg:
		// A live external pane for this same session was only minimized, not torn
		// down — restore it instantly (no reconnect) so toggling is immediate.
		if a.external != nil && !a.external.exited && a.external.sess.ID() == msg.sess.ID() {
			a.screen = ScreenExternal
			return a, nil
		}
		// Start the connector in a Cmd; transition to the "connecting" placeholder.
		a.connectingFor = &msg.sess
		a.connectingOpencode = msg.sess.State.Backend == session.BackendOpenCode
		a.connectErr = nil
		a.screen = ScreenConnecting
		// Fix A: build a read-only preview of the conversation from warm history or
		// the host-side cache so it paints immediately during the resume wait,
		// instead of a blank splash. opencode sessions have no Go transcript.
		a.connectingPreview = nil
		if !a.connectingOpencode {
			a.connectingPreview = a.buildConnectingPreview(msg.sess)
		}
		return a, a.connectCmd(msg.sess)

	case createSessionMsg:
		// `n` opens the backend picker; provisioning happens when the user
		// confirms a choice (pickerKey → createCmd). The picker is an overlay
		// over the live dashboard, so the screen stays ScreenDashboard.
		a.connectErr = nil
		a.openBackendPicker()
		return a, nil

	case attachReadyMsg:
		// Connection established: build the transcript screen. msg.client and
		// msg.reconnect are already dashboard.RunnerClient-typed, so they pass
		// straight through — no adapter needed.
		a.connectingFor = nil
		a.connectErr = nil
		a.connectStartedAt = time.Time{}
		// opencode-server sessions don't have a Go transcript; the local
		// `opencode attach` client owns the UI, embedded as a Tier-2 PTY pane.
		if msg.opencodeCreds != nil {
			// O1: if a different opencode session's pane is already live, close
			// it first to prevent goroutine/process leaks.
			if a.external != nil && a.external.sess.ID() != msg.sess.ID() {
				a.external.close()
				a.external = nil
			}
			// Phase 4: do NOT cancel the background passive SSE here (unlike the
			// transcript path below). The external pane is not a runner SSE client —
			// `opencode attach` is a separate connection to `opencode serve` — so the
			// passive stream is the ONLY thing feeding the runner observer's live
			// status/title/ctx%/cost into the read-model the pane's status row reads.
			// It's a passive stream, so it doesn't hold the idle reaper open.
			id := msg.sess.ID()
			pane := NewExternalPane(msg.sess, *msg.opencodeCreds, func() Session { return a.dashboard.sessionByID(id) })
			pane.w, pane.h = a.width, a.height
			a.external = pane
			a.screen = ScreenExternal
			return a, pane.Init()
		}
		// Cancel the dashboard's background SSE for this (claude) session so we
		// don't run two concurrent SSE clients to the same runner: the transcript
		// installs its own active client below (B2).
		a.dashboard.cancelLiveSSE(msg.sess.ID())
		// Reuse, in priority order: the preview already built (and cache-loaded)
		// for this connect (Fix A); the warm model retained from a background
		// stream; otherwise a fresh cold model. In every case we install the live
		// client + reconnect and register it as warm so future hide/show are O(1).
		var m *TranscriptModel
		preview := a.connectingPreview
		a.connectingPreview = nil
		if existing, ok := a.dashboard.retainedTranscript(msg.sess.ID()); ok {
			m = existing
		} else if preview != nil && preview.ref.ID == msg.sess.ID() {
			m = preview
		}
		if m != nil {
			m.client = msg.client         // install the live (active) client
			m.reconnect = msg.reconnect   // and its reconnect callback
			m.seedSize(a.width, a.height) // a background/preview model never got a WindowSizeMsg
		} else {
			m = NewTranscript(msg.client, msg.sess, msg.reconnect)
		}
		a.dashboard.putRetained(msg.sess.ID(), m)
		// Thread detected terminal capabilities into the transcript so its
		// status-line effects (ctx-gauge sweep, etc.) light up only on a capable
		// terminal; the dashboard Model detected them once at startup.
		m.caps = a.dashboard.caps
		// Workstream C: give the transcript the host-side event cache so it loads
		// history instantly on a cold open and mirrors streamed events for next time.
		m.cache = a.dashboard.eventCache
		// Hand off a one-shot initial prompt (from `sandbox claude "…"`) so the
		// transcript submits it as the first turn once its stream is live.
		m.initialPrompt = a.initialPrompt
		a.initialPrompt = ""
		// Restore parked view/input state (compose buffer, queued prompt, search,
		// permMode) if the user previously detached from this same session (B3).
		if ps, ok := a.parkedTranscripts[msg.sess.ID()]; ok {
			m.RestoreParkedState(ps)
			delete(a.parkedTranscripts, msg.sess.ID())
		}
		// C9: surface non-fatal connector warnings (e.g. sync failure) in the
		// transcript so they are visible in the alt-screen TUI rather than
		// discarded to a hidden stderr.
		if msg.warning != "" {
			m.appendBlock(blockInfo, "⚠ "+msg.warning)
		}
		// Mark everything seen for the session we're now viewing so its unread
		// badge clears the moment it comes to the foreground.
		for i := range a.dashboard.sessions {
			if a.dashboard.sessions[i].ID() == msg.sess.ID() {
				a.dashboard.sessions[i].seenSeq = a.dashboard.sessions[i].lastSeq
				break
			}
		}
		a.transcript = m
		a.screen = ScreenTranscript
		// Bubble Tea only emits WindowSizeMsg at startup and on resize, so a
		// child built mid-run never learns the size on its own. Seed it with
		// the size the App already knows so the transcript paints immediately
		// instead of rendering blank until the next resize.
		return a, tea.Batch(m.Init(), func() tea.Msg {
			return tea.WindowSizeMsg{Width: a.width, Height: a.height}
		})

	case attachFailedMsg:
		// Connector failed: stay on the dashboard and show the error inline.
		a.connectingFor = nil
		a.connectingPreview = nil
		a.connectStartedAt = time.Time{}
		a.connectErr = msg.err
		a.dashboard.connectErr = msg.err
		a.screen = ScreenDashboard
		return a, nil
	case detachMsg:
		// User detached from the transcript. Return to the dashboard, restart
		// the background SSE for the session we were attached to (B2), and
		// save the transcript's view/input state for the next re-attach (B3).
		a.screen = ScreenDashboard
		var restoreCmd tea.Cmd
		if a.transcript != nil {
			a.parkTranscript(a.transcript)
			restoreCmd = a.dashboard.startLiveSSECmd(a.dashboard.sessionByID(a.transcript.ref.ID))
		}
		a.transcript = nil // release the model to free port-forwards held by Cmds
		return a, restoreCmd

	case externalPaneFinishedMsg:
		// The external client (opencode attach) exited. Tear the pane down for
		// real and return to the dashboard; surface a non-nil error inline.
		// Restart the background SSE for the external session (B2).
		var restoreCmd tea.Cmd
		if a.external != nil {
			restoreCmd = a.dashboard.startLiveSSECmd(a.dashboard.sessionByID(a.external.sess.ID()))
			a.external.close()
		}
		a.external = nil
		a.screen = ScreenDashboard
		if msg.err != nil {
			a.connectErr = msg.err
			a.dashboard.connectErr = msg.err
		}
		return a, restoreCmd

	case connectUpdateMsg:
		// Progress from the in-flight connect goroutine (U1).
		switch {
		case msg.stage != nil:
			a.connectStage = *msg.stage
			a.connectDetail = msg.detail
			return a, connectNextCmd(a.connectCh) // keep draining
		case msg.ready != nil:
			a.connectCancel = nil
			a.connectCh = nil
			_, cmd := a.Update(*msg.ready) // reuse attachReadyMsg path
			return a, cmd
		case msg.failed != nil:
			a.connectCancel = nil
			a.connectCh = nil
			_, cmd := a.Update(*msg.failed) // reuse attachFailedMsg path
			return a, cmd
		}
		return a, nil

	case connectTickMsg:
		// Advance the connecting-screen spinner (U1.4).
		if a.screen != ScreenConnecting {
			return a, nil // self-stop when leaving
		}
		a.connectFrame++
		return a, connectTickCmd()
	}
	// Keep the dashboard's notion of the attached session current so background
	// attention toasts never fire for the session the user is already viewing.
	a.dashboard.attachedID = a.attachedSessionID()

	// Delegate to the dashboard EXACTLY ONCE per message (B17). On the dashboard
	// screen it owns all input, so every message goes to it here. Behind a modal
	// (transcript / external / connecting) it still processes background (non-key)
	// messages so its live state and toast notifications stay current — but key
	// presses there belong to the active screen, not the dashboard. The per-screen
	// switch below must NOT call a.dashboard.Update again; it only reuses dashCmd.
	var dashCmd tea.Cmd
	if _, isKey := msg.(tea.KeyPressMsg); a.screen == ScreenDashboard || !isKey {
		next, cmd := a.dashboard.Update(msg)
		if dm, ok := next.(*Model); ok {
			a.dashboard = dm
		}
		dashCmd = cmd
		// The dashboard may have changed, so the cached modal backdrop (Fix E) is
		// stale. A pure transcript keystroke never reaches here, so it keeps the
		// cache warm.
		a.modalBackdropValid = false
	}

	// Emit the OSC 9;4 tab-progress signal out-of-band on an aggregate-state
	// transition. It MUST go via tea.Raw: Bubble Tea v2's cell renderer drops
	// control strings spliced into View content (the same reason the desktop
	// notification and Kitty graphics ride tea.Raw, not View). Edge-triggered
	// against a.lastProgress so each transition writes exactly once — not every
	// frame — and idle goes quiet. progressState already returns ProgressNone
	// off-Ghostty / under ReduceMotion, so this is a no-op there.
	if a.screen == ScreenExternal {
		// The opencode PTY owns the terminal and may write its own OSC 9;4, so we
		// don't paint over it. Forget our last state while it's attached so the
		// next non-external frame re-asserts the real progress instead of assuming
		// the terminal still reflects what we last emitted.
		a.lastProgress = terminal.ProgressNone
	} else if p := a.dashboard.progressState(); p != a.lastProgress {
		a.lastProgress = p
		dashCmd = tea.Batch(dashCmd, tea.Raw(terminal.OSCProgress(p)))
	}

	// Mirror the dashboard's freshly-polled mutagen sync health into the attached
	// transcript so its status line can surface a stalled sync. The dashboard
	// already probes warm sessions (the foreground session is one of them), so
	// this is a read of shared Session state after delegation — no new probe.
	if a.screen == ScreenTranscript && a.transcript != nil {
		a.transcript.syncStatus = a.dashboard.sessionByID(a.transcript.ref.ID).SyncStatus
	}

	switch a.screen {
	case ScreenTranscript:
		if a.transcript == nil {
			// Transcript went away unexpectedly; revert to the dashboard. The
			// dashboard already saw this message iff it was non-key (delegated
			// above), so just return dashCmd — do not re-delegate (B17).
			a.screen = ScreenDashboard
			return a, dashCmd
		}
		// Intercept detach keys → back to the dashboard; everything else goes
		// to the transcript model. ctrl+] / ctrl+4 quit the *standalone* TUI
		// (sandbox claude), but under the command center they must detach to
		// the dashboard, not tear down the whole program.
		if kp, ok := msg.(tea.KeyPressMsg); ok {
			ks := kp.String()
			// esc detaches only when the transcript has no local use for it (not in
			// INSERT mode, no overlay open) — otherwise it returns to NORMAL or
			// closes the overlay inside the transcript. ctrl+] / ctrl+4 always
			// detach (they quit the standalone TUI; under the command center they
			// detach to the dashboard rather than tearing down the program).
			detach := ks == "ctrl+]" || ks == "ctrl+4" || (ks == "esc" && !a.transcript.escapeConsumes())
			if detach {
				// With a queued prompt, the escape steers (interrupt + inject)
				// instead of detaching.
				if a.transcript.queuedPrompt != "" {
					next, cmd := a.transcript.Update(msg)
					if tm, ok := next.(*TranscriptModel); ok {
						a.transcript = tm
					}
					return a, tea.Batch(cmd, dashCmd)
				}
				// Park the transcript view/input state so it survives the
				// detach→reattach cycle (B3).
				a.parkTranscript(a.transcript)
				a.screen = ScreenDashboard
				restoreCmd := a.dashboard.startLiveSSECmd(a.dashboard.sessionByID(a.transcript.ref.ID))
				a.transcript = nil
				return a, tea.Batch(dashCmd, restoreCmd)
			}
			switch ks {
			case "ctrl+g":
				// Jump to the next session needing attention and close the modal.
				if s := a.dashboard.jumpToNextNeedingAttention(); s != nil {
					a.parkTranscript(a.transcript)
					restoreCmd := a.dashboard.startLiveSSECmd(a.dashboard.sessionByID(a.transcript.ref.ID))
					a.transcript = nil
					return a, tea.Batch(dashCmd, restoreCmd, func() tea.Msg { return attachMsg{sess: *s} })
				}
				return a, dashCmd
			case "ctrl+k":
				// Open the dashboard's quick-switcher from inside the chat modal.
				a.dashboard.openSwitcher()
				return a, dashCmd
			}
		}
		// Left-button press/drag on the modal's scrollbar column drives the scroll
		// position; wheel and everything else fall through to the transcript's own
		// Update (which handles the wheel).
		if a.handleScrollbarMouse(msg) {
			return a, dashCmd
		}
		next, cmd := a.transcript.Update(msg)
		if tm, ok := next.(*TranscriptModel); ok {
			a.transcript = tm
		}
		return a, tea.Batch(cmd, dashCmd)
	case ScreenExternal:
		if a.external == nil {
			a.screen = ScreenDashboard
			return a, dashCmd
		}
		if kp, ok := msg.(tea.KeyPressMsg); ok {
			switch kp.String() {
			case "ctrl+]", "ctrl+4":
				// Detach (back to the dashboard) without tearing down the pane —
				// the child keeps running so re-open is instant. esc is NOT in
				// this set: the embedded opencode TUI uses esc to dismiss its own
				// overlays / escape input mode, so intercepting it here would
				// trap the user inside opencode's screen with no way out for the
				// pane's own UI. Use ctrl+] (or ctrl+4) to detach.
				a.screen = ScreenDashboard
				return a, dashCmd
			default:
				// Everything else (incl. esc) is forwarded to the embedded opencode client.
				a.external.handleKey(kp)
				return a, dashCmd
			}
		}
		if paste, ok := msg.(tea.PasteMsg); ok {
			a.external.handlePaste(paste)
			return a, dashCmd
		}
		if mouse, ok := msg.(tea.MouseMsg); ok {
			a.external.handleMouse(mouse)
			return a, dashCmd
		}
		return a, dashCmd

	case ScreenConnecting:
		// While connecting, any key press (other than ctrl+c handled above)
		// cancels the connection attempt and returns to the dashboard.
		if _, ok := msg.(tea.KeyPressMsg); ok {
			a.connectingFor = nil
			a.connectingPreview = nil
			a.screen = ScreenDashboard
			return a, dashCmd
		}
		return a, dashCmd

	default: // ScreenDashboard
		// The dashboard already handled this message in the single delegation
		// above (it is the active screen, so both key and non-key were sent).
		// Returning dashCmd here — instead of calling Update again — is the B17
		// fix: a background RunnerEventMsg used to be delegated twice, spawning a
		// duplicate self-perpetuating liveSSENextCmd reader per event.
		return a, dashCmd
	}
}

// attachedSessionID returns the id of the session the user is currently attached
// to (transcript or external pane), or "" when on the dashboard. It is the
// exclusion key for background-attention toasts.
func (a *App) attachedSessionID() session.ID {
	switch {
	case a.screen == ScreenTranscript && a.transcript != nil:
		return a.transcript.ref.ID
	case a.screen == ScreenExternal && a.external != nil:
		return a.external.sess.ID()
	}
	return ""
}

// parkTranscript saves the transcript's view/input state keyed by session ID
// so it can be restored on the next re-attach to the same session (B3).
func (a *App) parkTranscript(m *TranscriptModel) {
	if m == nil {
		return
	}
	if a.parkedTranscripts == nil {
		a.parkedTranscripts = make(map[session.ID]ParkedTranscriptState)
	}
	a.parkedTranscripts[m.ref.ID] = m.ParkState()
	// Tear down the transcript's own live SSE stream so we don't leave a second
	// SSE client open after detach (NEW-5). Every detach path parks the
	// transcript immediately before releasing it, so this is the single hook.
	m.cancelStream()
}

// View renders the active screen. When a transcript is open it is composited
// as a near-fullscreen modal over the live dashboard (slice 5b) so the frame
// badge/toasts stay visible around the edges.
func (a *App) View() tea.View {
	v := a.withTerminalSignals(a.withToast(a.screenView()))
	// Cell-motion mouse capture on EVERY screen, the external opencode PTY
	// included. The embedded opencode TUI enables mouse tracking itself (verified
	// live: it sets DECSET 1000/1002/1003 + SGR 1006), but those requests reach
	// only the emulator — the HOST terminal's mouse mode is owned by this outer
	// program. With MouseMode left off on ScreenExternal, the host (e.g. Ghostty)
	// instead translated the wheel into arrow keys, which fell through to opencode
	// as Up/Down and hijacked its prompt history. Capturing here routes
	// wheel/click/drag to the app, where handleMouse re-encodes them as SGR mouse
	// and writes them to opencode's PTY — so opencode's own wheel-scroll and
	// clickable spots work (Phase 3 item 3). On the transcript it drives the
	// scrollbar wheel/click-drag (T1). Trade-off: app mouse capture replaces native
	// click-drag selection (shift+drag still selects).
	v.MouseMode = tea.MouseModeCellMotion
	// Opaque page background everywhere EXCEPT the external pane, which paints its
	// own — otherwise unpainted cells (splash whitespace, overlay margins) bleed the
	// terminal's possibly-transparent background through (T9).
	if a.screen != ScreenExternal {
		v.BackgroundColor = theme.Page
	}
	return v
}

// withToast composites the active cross-session "needs you" notification over the
// composed frame so it floats at the top-right of *every* screen (T3) — the chat
// modal and connecting splash included, which is exactly when a background
// session needing attention matters most. Previously only renderZoned composited
// it, so it was invisible behind the modal. ScreenExternal owns the terminal and
// is left untouched. A nil toast is a no-op, so non-toast frames are unchanged.
func (a *App) withToast(v tea.View) tea.View {
	if a.dashboard == nil || a.screen == ScreenExternal || a.dashboard.toast == nil {
		return v
	}
	// The toast earns its keep only when a modal/splash hides the session list. On
	// the bare dashboard the row glyphs already show every session's attention
	// state, so floating a toast over the list is the redundant noise the user
	// saw. The backend picker counts as "still on the list", so suppress there too.
	if a.screen == ScreenDashboard && !a.picker.open {
		return v
	}
	w, h := a.width, a.height
	if w == 0 || h == 0 {
		return v
	}
	// Position the whole box as one layer at the computed column so every row is
	// indented together (see renderToast — the old per-string space padding only
	// shifted line 0 and sheared the box).
	box, x := a.dashboard.renderToast(w)
	if box == "" {
		return v
	}
	canvas := lipgloss.NewCanvas(w, h)
	canvas.Compose(lipgloss.NewCompositor(
		lipgloss.NewLayer(v.Content).X(0).Y(0).Z(0),
		lipgloss.NewLayer(box).X(x).Y(2).Z(10),
	))
	v.Content = canvas.Render()
	return v
}

// screenView renders the active screen's view without terminal-signal
// decoration. View wraps it with the Stage 2 OSC signals.
func (a *App) screenView() tea.View {
	// The backend picker overlays the dashboard while choosing a new session's
	// backend.
	if a.picker.open {
		return a.pickerView()
	}
	switch a.screen {
	case ScreenConnecting:
		return a.connectingView()
	case ScreenExternal:
		if a.external == nil {
			return a.dashboard.View()
		}
		return a.external.View()
	case ScreenTranscript:
		if a.transcript == nil {
			return a.dashboard.View()
		}
		return a.modalView()
	default:
		return a.dashboard.View()
	}
}

// withTerminalSignals prepends the one-shot Kitty image transmission (queued by
// the transcript's ctx gauge on the frame its value changed) to the composed
// frame. Tab-progress (OSC 9;4) and the desktop notification do NOT ride here:
// Bubble Tea v2's cell renderer drops control strings spliced into View content,
// so both go out-of-band via tea.Raw — the progress signal from App.Update on the
// session-aggregate transition, the notification from the toastMsg handler. The
// Kitty image is the lone sanctioned in-View write because its placeholder cells
// must reference an image that already exists in the same frame. The external
// (opencode) PTY screen is left untouched — it owns the terminal.
func (a *App) withTerminalSignals(v tea.View) tea.View {
	if a.dashboard == nil || a.screen == ScreenExternal || a.transcript == nil {
		return v
	}
	// Stage 3: one-shot Kitty image transmission queued by the transcript's ctx
	// gauge when its value changed this frame (rides the changing frame, not every
	// frame). Prepended so the image exists before the placeholder cells reference it.
	kitty := a.transcript.takePendingKitty()
	if kitty == "" {
		return v
	}
	v.Content = kitty + v.Content
	return v
}

// modalRect returns the chat modal's outer rectangle (top-left mx,my and size
// mw,mh) on the current screen. It is the single source of truth shared by
// modalView (compositing) and the scrollbar mouse hit-test, so the two can never
// drift apart.
func (a *App) modalRect() (mx, my, mw, mh int) {
	w, h := a.width, a.height
	mw = w - 6
	mh = h - 4
	if mw < 20 {
		mw = 20
	}
	if mh < 6 {
		mh = 6
	}
	mx = (w - mw) / 2
	my = (h - mh) / 2
	return mx, my, mw, mh
}

// handleScrollbarMouse routes a left-button press/drag onto the chat modal's
// scrollbar column, translating absolute screen coordinates into the
// transcript's content space via modalRect (inner origin = modal top-left + the
// rounded border). It returns true only when the event is a left press/drag on
// the scrollbar; wheel events and clicks elsewhere return false and fall through
// to the transcript's own handler.
func (a *App) handleScrollbarMouse(msg tea.Msg) bool {
	if a.transcript == nil {
		return false
	}
	var mouse tea.Mouse
	switch m := msg.(type) {
	case tea.MouseClickMsg:
		mouse = m.Mouse()
	case tea.MouseMotionMsg:
		mouse = m.Mouse()
	default:
		return false
	}
	if mouse.Button != tea.MouseLeft {
		return false
	}
	mx, my, _, _ := a.modalRect()
	return a.transcript.scrollbarDragTo(mouse.X-(mx+1), mouse.Y-(my+1))
}

// modalView composites the dashboard background with the transcript as a
// floating modal. z-order: dashboard < shadow < modal.
func (a *App) modalView() tea.View {
	w, h := a.width, a.height
	if w == 0 || h == 0 {
		v := tea.NewView(a.dashboard.View().Content)
		v.AltScreen = true
		return v
	}

	mx, my, mw, mh := a.modalRect()

	// Frame the transcript as a bordered popup. The content is sized two cells
	// smaller in each axis to leave room for the rounded border, so the framed
	// card is exactly mw×mh and lines up with the drop shadow.
	inner := a.transcript.modalContent(mw-2, mh-2)
	modal := kit.Card(kit.CardOpts{
		Content:     inner,
		Width:       mw,
		BorderColor: theme.Charple,
		Background:  theme.Surface,
	})
	shadow := solidBlock(mw, mh, theme.Shadow)

	layers := []*lipgloss.Layer{
		// A fully opaque page-colored fill behind the popup: the dashboard is
		// hidden entirely (no dimmed ghost bleeding through) so the modal reads as
		// a focused sheet. Reused across keystrokes via the backdrop cache (Fix E).
		lipgloss.NewLayer(a.opaqueBackdrop(w, h)).X(0).Y(0).Z(0),
		lipgloss.NewLayer(shadow).X(mx + 2).Y(my + 1).Z(1),
		lipgloss.NewLayer(modal).X(mx).Y(my).Z(2),
	}

	canvas := lipgloss.NewCanvas(w, h)
	canvas.Compose(lipgloss.NewCompositor(layers...))
	v := tea.NewView(canvas.Render())
	v.AltScreen = true
	return v
}

// opaqueBackdrop returns a fully opaque page-colored fill drawn behind the
// transcript modal, served from a size-keyed cache so every keystroke's View
// reuses it instead of re-filling (Fix E). It is a SOLID fill, not a dimmed
// ghost of the dashboard: nothing behind the modal shows through, so the modal
// reads as a focused sheet rather than a translucent overlay. The cache is
// invalidated on a dashboard delegation or a size change; for a solid fill the
// dashboard-change rebuild is a harmless no-op (the block is size-only).
func (a *App) opaqueBackdrop(w, h int) string {
	if a.modalBackdropValid && a.modalBackdropW == w && a.modalBackdropH == h {
		return a.modalBackdrop
	}
	a.bdBuilds++
	d := solidBlock(w, h, theme.Page)
	a.modalBackdrop, a.modalBackdropW, a.modalBackdropH = d, w, h
	a.modalBackdropValid = true
	return d
}

// dimBackdrop ghosts content behind a modal: it strips each line's colors and
// re-renders them as dim text on the flat page background, normalized to a solid
// w×h block — recognizable but recessed. Used by the reconnect splash to keep
// the cached conversation visible (dimmed) behind the stepper. The transcript
// modal does NOT use this; it uses opaqueBackdrop (a solid fill) so nothing
// shows through.
func dimBackdrop(bg string, w, h int) string {
	dim := lipgloss.NewStyle().Foreground(theme.TextDim).Background(theme.Page)
	lines := strings.Split(bg, "\n")
	out := make([]string, h)
	for i := range out {
		var raw string
		if i < len(lines) {
			raw = ansi.Strip(lines[i])
		}
		if lipgloss.Width(raw) > w {
			raw = ansi.Truncate(raw, w, "")
		}
		if pad := w - lipgloss.Width(raw); pad > 0 {
			raw += strings.Repeat(" ", pad)
		}
		out[i] = dim.Render(raw)
	}
	return strings.Join(out, "\n")
}

// solidBlock returns a w×h block of spaces with the given background color.
func solidBlock(w, h int, c color.Color) string {
	row := lipgloss.NewStyle().Background(c).Render(strings.Repeat(" ", w))
	lines := make([]string, h)
	for i := range lines {
		lines[i] = row
	}
	return strings.Join(lines, "\n")
}

// --------------------------------------------------------------------------
// Commands
// --------------------------------------------------------------------------

// connectCmd runs the Connector for the given session in a background goroutine
// that streams ConnectStage updates via a channel (U1). The UI stays responsive
// and the connecting screen animates while the port-forward + health check runs.
func (a *App) connectCmd(sess Session) tea.Cmd {
	connector := a.connector
	if connector == nil {
		// No connector configured (unit-test / no-backend mode): fail gracefully.
		return func() tea.Msg {
			return attachFailedMsg{
				sess: session.Ref{ID: sess.ID()},
				err:  fmt.Errorf("no connector configured"),
			}
		}
	}
	// 300s: this connect path now owns the cold-start pod wait (schedule + image
	// pull) that used to run in a pre-TUI backend.Start, so an attach/resume onto a
	// cold node must not be cut off mid-pull. Matches createCmd's budget for the
	// equivalent provision+wait (Phase 2).
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	a.connectCancel = cancel
	ch := make(chan connectUpdateMsg, 8)
	a.connectCh = ch
	a.connectStage = StageCheck
	a.connectDetail = ""
	a.connectStartedAt = nowFunc()
	go func() {
		defer cancel()
		onStage := func(s ConnectStage, detail string) {
			select {
			case ch <- connectUpdateMsg{stage: &s, detail: detail}:
			case <-ctx.Done():
			}
		}
		res, err := connector(ctx, session.Ref{ID: sess.ID()}, sess.State.ProjectPath, onStage)
		if err != nil {
			ch <- connectUpdateMsg{failed: &attachFailedMsg{sess: session.Ref{ID: sess.ID()}, err: err}}
		} else {
			ready := attachReadyMsg{
				sess:          sess,
				client:        res.Client,
				reconnect:     res.Reconnect,
				endpoint:      res.Endpoint,
				opencodeCreds: res.OpencodeCreds,
				warning:       res.Warning,
			}
			ch <- connectUpdateMsg{ready: &ready}
		}
		close(ch)
	}()
	return tea.Batch(connectNextCmd(ch), connectTickCmd())
}

// createCmd provisions a brand-new session via the Creator in a background
// goroutine that streams ConnectStage updates (U1).
func (a *App) createCmd(backend string) tea.Cmd {
	creator := a.creator
	if creator == nil {
		return func() tea.Msg {
			return attachFailedMsg{err: fmt.Errorf("new session is not available")}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	a.connectCancel = cancel
	ch := make(chan connectUpdateMsg, 8)
	a.connectCh = ch
	// The Creator's connect path (establish) emits StageCheck first, then advances
	// to StageResume while the freshly-provisioned pod schedules + pulls its image.
	// Initialize at StageCheck so the stepper doesn't briefly regress Resume→Check
	// on the first emitted update (the pod wait moved out of a pre-connect
	// backend.Start into establish — Phase 2).
	a.connectStage = StageCheck
	a.connectDetail = ""
	a.connectStartedAt = nowFunc()
	a.connectingOpencode = backend == session.BackendOpenCode
	go func() {
		defer cancel()
		onStage := func(s ConnectStage, detail string) {
			select {
			case ch <- connectUpdateMsg{stage: &s, detail: detail}:
			case <-ctx.Done():
			}
		}
		res, err := creator(ctx, backend, onStage)
		if err != nil {
			ch <- connectUpdateMsg{failed: &attachFailedMsg{err: err}}
		} else {
			ready := attachReadyMsg{
				sess:          SessionFromState(res.State),
				client:        res.Client,
				reconnect:     res.Reconnect,
				endpoint:      res.Endpoint,
				opencodeCreds: res.OpencodeCreds,
				warning:       res.Warning, // RV23: surface new-session sync warnings
			}
			ch <- connectUpdateMsg{ready: &ready}
		}
		close(ch)
	}()
	return tea.Batch(connectNextCmd(ch), connectTickCmd())
}

// connectNextCmd reads one item from the connect goroutine's channel.
func connectNextCmd(ch chan connectUpdateMsg) tea.Cmd {
	return func() tea.Msg {
		u, ok := <-ch
		if !ok {
			return nil
		}
		return u
	}
}

// connectTickCmd drives the connecting-screen spinner at animFPS.
func connectTickCmd() tea.Cmd {
	return tea.Tick(animFPS, func(time.Time) tea.Msg { return connectTickMsg{} })
}

// --------------------------------------------------------------------------
// Rendering helpers
// --------------------------------------------------------------------------

// buildConnectingPreview returns a read-only transcript for the session being
// connected to, populated from warm history or the host-side cache, so the
// conversation can paint during the connect/resume wait (Fix A). It returns nil
// when there is nothing to show (no warm model and an empty/absent cache), so a
// brand-new or uncached session keeps the centered "connecting…" splash.
func (a *App) buildConnectingPreview(sess Session) *TranscriptModel {
	// A warm background model already holds history — reuse it directly.
	if m, ok := a.dashboard.retainedTranscript(sess.ID()); ok {
		m.seedSize(a.width, a.height)
		if len(m.blocks) == 0 {
			return nil
		}
		return m
	}
	if a.dashboard.eventCache == nil {
		return nil
	}
	m := NewTranscript(nil, sess, nil)
	m.caps = a.dashboard.caps
	m.cache = a.dashboard.eventCache
	m.loadCachedTranscript() // O(N) bulk replay; needs no live client
	if len(m.blocks) == 0 {
		return nil // nothing cached → keep the centered splash
	}
	m.seedSize(a.width, a.height)
	return m
}

// connectingView renders the animated connect/reconnect screen: a block-pixel
// mascot, the title (text ramp, not bold brand color), an animated stepper, and
// a cancel hint, centered on screen. When a warm/cached preview exists (Fix A)
// the conversation is dimmed as a backdrop and the stepper floats over it as a
// "Reconnecting…" modal from frame one — so a resume reads as progress over your
// real chat instead of a blank splash, and the stepper is visible immediately
// (T4) rather than buried in a thin one-line banner.
func (a *App) connectingView() tea.View {
	reconnecting := a.connectingPreview != nil && len(a.connectingPreview.blocks) > 0

	verb := "Connecting"
	if reconnecting {
		verb = "Reconnecting"
	}
	title := verb + "…"
	if a.connectingFor != nil {
		title = fmt.Sprintf("%s to %s…", verb, a.connectingFor.Title)
	}
	// Append a live elapsed timer (≥1s) so a slow cold-pod resume reads as
	// progress, not a freeze — mirrors the transcript reconnect header.
	if !a.connectStartedAt.IsZero() {
		if el := nowFunc().Sub(a.connectStartedAt); el >= time.Second {
			title += fmt.Sprintf(" (%s)", roundDur(el))
		}
	}

	titleLine := lipgloss.NewStyle().
		Foreground(theme.TextBright).
		Bold(true).
		Render(title)

	// opencode sessions have an extra "Starting opencode" stage; show it so the
	// current stage is always in the displayed set and renders a live spinner.
	var applicable []ConnectStage
	if a.connectingOpencode {
		applicable = opencodeConnectStages
	}
	stepper := connectingStepper(a.connectStage, a.connectFrame, a.connectDetail, applicable)

	// Block-pixel mascot above the title — the Claude Code guy for Claude, the
	// pixel "OC" monogram for opencode — so the splash announces which agent is
	// coming up, in that agent's own brand register.
	logo := theme.ClaudeMascot()
	if a.connectingOpencode {
		logo = theme.OpenCodeMascot()
	}

	hint := lipgloss.NewStyle().
		Foreground(theme.TextMuted).
		Render("(press any key to cancel)")

	// Keep the title/stepper/hint left-aligned relative to each other: the inner
	// JoinVertical(Left, …) pads every line to the panel's widest, so the stepper
	// rows stay column-aligned (T2) and re-centering the finished block can't
	// disturb that inner alignment. The same uniform-width invariant is why the
	// logo no longer shears — gradientBlock pads its lines to a common width
	// (T7); JoinVertical(Center, …) then centers the two equal-width blocks
	// against each other cleanly. (A ragged block would be centered line-by-line
	// and come out sheared.)
	panel := lipgloss.JoinVertical(lipgloss.Left, titleLine, "", stepper, "", hint)
	body := lipgloss.JoinVertical(lipgloss.Center, logo, "", panel)

	if reconnecting {
		// Dim the cached conversation as a backdrop and float the stepper over it
		// as a modal from frame one. Unlike the transcript modal (whose backdrop is
		// a solid opaque fill), the reconnect splash intentionally keeps the dimmed
		// conversation visible so the user sees *which* session is reconnecting
		// (TestConnectingPreviewShowsCachedHistory). The card is fully opaqued
		// (withBackground) so the dimmed chat can't bleed through the stepper's
		// transparent gaps; it mirrors the chat modal's framing (border + shadow).
		backdrop := dimBackdrop(a.connectingPreview.previewView(a.width, a.height, ""), a.width, a.height)
		cardW := lipgloss.Width(body) + 2 // + rounded border
		card := withBackground(kit.Card(kit.CardOpts{
			Content:     body,
			Width:       cardW,
			BorderColor: theme.Charple,
			Background:  theme.Surface,
		}), theme.Surface)
		cardH := lipgloss.Height(card)
		mx := (a.width - cardW) / 2
		my := (a.height - cardH) / 2
		if mx < 0 {
			mx = 0
		}
		if my < 0 {
			my = 0
		}
		shadow := solidBlock(cardW, cardH, theme.Shadow)
		canvas := lipgloss.NewCanvas(a.width, a.height)
		canvas.Compose(lipgloss.NewCompositor(
			lipgloss.NewLayer(backdrop).X(0).Y(0).Z(0),
			lipgloss.NewLayer(shadow).X(mx+2).Y(my+1).Z(1),
			lipgloss.NewLayer(card).X(mx).Y(my).Z(2),
		))
		v := tea.NewView(canvas.Render())
		v.AltScreen = true
		return v
	}

	centered := lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center, body, pageWhitespace())
	v := tea.NewView(centered)
	v.AltScreen = true
	return v
}

package dashboard

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/charmbracelet/x/ansi"

	"github.com/cullenmcdermott/sandbox/internal/session"
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
	// (opencode attach / the claude-pane PTY) for external-pane sessions.
	ScreenExternal
	// ScreenFeed is the read-only activity feed for an external-pane session
	// (claude-pane-first): a detached monitor built from normalized events,
	// from which the user can attach the pane. No input, no terminal handover.
	ScreenFeed
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

// viewFeedMsg is sent when the user opens the read-only activity feed for an
// external-pane session (the `v` key). Unlike attachMsg it establishes no
// connection and takes no terminal: the feed renders from the events already
// flowing on the background stream (claude-pane-first).
type viewFeedMsg struct {
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
	// paneDial, when non-nil, establishes the remote pane transport for a
	// backend whose interactive TUI runs in the pod (claude-pane): the attach
	// routes to an ExternalPane over it instead of a Go transcript.
	paneDial PaneDial
	// warning is a non-fatal advisory (e.g. sync failure) to surface in the
	// transcript as an info block so it is visible in the alt-screen TUI (C9).
	warning string
	// awaitWarning, when non-nil, blocks until the connect's background
	// sync/reaper work settles and returns its late advisory (§5). The App
	// polls it once via a Cmd and surfaces the result as a syncAdvisoryMsg.
	awaitWarning func(context.Context) (string, error)
	// close tears down the connection's transport (ConnectResult.Close — the
	// SPDY forwards, §1d C1). The handler hands it to the owning pane
	// (TranscriptModel.transportClose / ExternalPane.transportClose); a dropped
	// stale-generation ready must invoke it instead.
	close func()
}

// syncAdvisoryMsg carries a late background-sync/reaper advisory (see
// attachReadyMsg.awaitWarning) to the session's transcript.
type syncAdvisoryMsg struct {
	id      session.ID
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
	gen    int              // connect-attempt generation; stale msgs are dropped
	stage  *ConnectStage    // progress tick — connector entered a new stage
	detail string           // optional live sub-status for the current stage ("" = none)
	ready  *attachReadyMsg  // success (terminal)
	failed *attachFailedMsg // failure (terminal)
}

// connectTickMsg drives the connecting-screen spinner animation.
type connectTickMsg struct{}

// leaderTimeoutMsg fires leaderTimeout after the external pane's ctrl+] leader is
// armed. gen pins it to the arming that scheduled it (App.leaderGen): a tick whose
// gen no longer matches was superseded by a re-arm or an already-resolved chord
// and is ignored. Mirrors the toastTickMsg / toastTickCmd pattern in notify.go.
type leaderTimeoutMsg struct{ gen int }

// leaderTimeoutCmd schedules the lone-ctrl+] → detach resolution for an armed
// leader, tagged with the arming generation so a stale tick can be discarded.
func leaderTimeoutCmd(gen int) tea.Cmd {
	return tea.Tick(leaderTimeout, func(time.Time) tea.Msg {
		return leaderTimeoutMsg{gen: gen}
	})
}

// --------------------------------------------------------------------------
// App
// --------------------------------------------------------------------------

// App is the root Bubble Tea v2 model for the sandbox command center. It owns
// the screen enum, both child models, and the Connector used for attaching to
// sessions. All Update and View calls are delegated to the active screen.
type App struct {
	screen    Screen
	dashboard *Model
	external  *ExternalPane // nil unless attached to an external-pane session
	feed      *feedModel    // nil unless viewing an external-pane session's activity feed

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

	// accountStore is the injected metadata-only view of the Anthropic credential
	// store, driving the account picker's list + add-account sub-flow. nil means
	// no account step: claude selection creates on the shared Secret (legacy UX).
	// Secret bytes never cross this interface.
	accountStore AccountStore

	// connector is called in a Cmd to establish a live runner connection.
	// It is set by Run/NewApp; nil means attach is disabled (unit-test mode).
	connector Connector

	// creator provisions a brand-new session for the `n` key. nil disables
	// new-session (unit-test mode); the dashboard surfaces an error inline.
	creator Creator

	// connectingFor is the session being connected to (shown in the
	// ScreenConnecting placeholder).
	connectingFor *Session

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

	// connectGen identifies the current connect attempt. It is bumped when an
	// attempt starts and when one is cancelled, so trailing connectUpdateMsg
	// values from a cancelled/replaced goroutine (a "context canceled" failure,
	// a late ready that would attach a dead session, or a stage tick that would
	// re-arm the drain on the wrong channel) are recognized as stale and dropped.
	connectGen int

	// connectErr holds the last connector error, shown in the detail pane.
	connectErr error

	// autoAttach, when non-nil, makes the App open straight into a session's pane
	// on launch (used by `sandbox claude` / `sandbox attach`), with the dashboard
	// list loading underneath so esc still returns to it.
	autoAttach *Session

	// Terminal size is propagated to child models via WindowSizeMsg.
	width  int
	height int

	// leaderArmed marks that the external (PTY) pane's ctrl+] leader chord is
	// waiting for its next key. ctrl+] was already the reserved detach key there;
	// arming it into a leader lets the pane reach attention-nav (jump next/prev,
	// TODO §2d, decided 2026-07-07) WITHOUT stealing any key the embedded opencode
	// client itself binds — nothing changes until the user first presses ctrl+].
	// A lone ctrl+] still detaches, but now only once the leader lapses (double-tap
	// or the leaderTimeout), a deliberate trade for making ctrl+] a prefix.
	leaderArmed bool

	// leaderGen invalidates in-flight leaderTimeout ticks. It is bumped on every
	// arm and every resolve (detach / jump / forward), so a timeout scheduled for
	// an earlier arming is recognized as stale and dropped rather than detaching
	// out from under a re-armed or already-resolved chord.
	leaderGen int
}

// NewApp constructs the root App with a dashboard backed by the given Backend.
// connector may be nil (attach will be a no-op / for unit tests).
func NewApp(backend Backend, connector Connector, creator Creator) *App {
	dash := New(backend)
	if connector != nil {
		dash.WithConnector(connector)
	}
	return &App{
		screen:    ScreenDashboard,
		dashboard: dash,
		connector: connector,
		creator:   creator,
	}
}

// RunOptions configures optional behavior for Run/RunAttached.
type RunOptions struct {
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

	// MaxObserverStreams caps the number of concurrently-established background
	// observer forwards (§1d). Zero uses the built-in default
	// (defaultMaxObserverStreams). Beyond the cap the coldest streams are evicted
	// and their rows fall back to watch-driven lifecycle status.
	MaxObserverStreams int

	// SyncProber reports per-session sync health for the dashboard indicator.
	SyncProber SyncProber

	// SyncReaper enumerates + terminates orphaned mutagen syncs (those whose pod
	// is gone). When set, the dashboard runs a periodic GC on the reconcile tick so
	// the host mutagen daemon doesn't accumulate dead syncs from reaped/destroyed/
	// out-of-band-deleted sessions. nil disables it.
	SyncReaper SyncReaper

	// IdleTimeout is the reaper idle-timeout, used to render the "suspends in"
	// hint for warm sessions. Zero hides the hint.
	IdleTimeout time.Duration

	// AccountStore backs the new-session Anthropic account picker (list + add-
	// account/login). nil disables the account step entirely: claude sessions are
	// created on the shared cluster Secret, exactly as before this feature. The
	// concrete impl (internal/cli) holds the Keychain-backed store; only metadata
	// crosses this seam.
	AccountStore AccountStore

	// WorktreeOps backs the dashboard's convert-to-branch flow (`b` keymap). nil
	// disables the flow. The concrete impl (internal/cli) wraps the client SDK's
	// per-session worktree git surface; only branch/message strings cross this
	// seam (no LLM, no git internals).
	WorktreeOps WorktreeOps
}

// applyOpts threads RunOptions into the dashboard model.
func (a *App) applyOpts(opts []RunOptions) {
	if len(opts) == 0 {
		return
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
	if opts[0].MaxObserverStreams > 0 {
		a.dashboard = a.dashboard.WithMaxObserverStreams(opts[0].MaxObserverStreams)
	}
	if opts[0].SyncProber != nil {
		a.dashboard = a.dashboard.WithSyncProber(opts[0].SyncProber)
	}
	if opts[0].SyncReaper != nil {
		a.dashboard = a.dashboard.WithSyncReaper(opts[0].SyncReaper)
	}
	if opts[0].IdleTimeout > 0 {
		a.dashboard = a.dashboard.WithIdleTimeout(opts[0].IdleTimeout)
	}
	if opts[0].AccountStore != nil {
		a.accountStore = opts[0].AccountStore
	}
	if opts[0].WorktreeOps != nil {
		a.dashboard = a.dashboard.WithWorktreeOps(opts[0].WorktreeOps)
	}
}

// Run starts the Bubble Tea program with the root App model and returns when
// the user quits. connector provides live runner connections for attach.
func Run(backend Backend, connector Connector, creator Creator, opts ...RunOptions) error {
	app := NewApp(backend, connector, creator)
	app.applyOpts(opts)
	p := tea.NewProgram(app)
	_, err := p.Run()
	return err
}

// RunAttached starts the command center already attached to one session — the
// entry point for `sandbox claude` and `sandbox attach`. The dashboard list
// still loads underneath, so pressing esc/detach returns to the full session
// list rather than quitting. initialPrompt is accepted for signature
// compatibility but ignored: external-pane sessions own their own input loop
// (there is no headless first-turn path), so a seed prompt has nowhere to go.
func RunAttached(backend Backend, connector Connector, creator Creator, sess Session, initialPrompt string, opts ...RunOptions) error {
	_ = initialPrompt
	app := NewApp(backend, connector, creator)
	app.applyOpts(opts)
	app.autoAttach = &sess
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
		return a.handlePtyOutput(msg)

	case tea.BackgroundColorMsg:
		// Adapt the whole palette to the detected terminal background. theme.ApplyTheme
		// rebuilds the shared styles, so the next render of every screen adapts.
		theme.ApplyForBackground(msg.IsDark())
		return a, nil

	case tea.PasteMsg:
		// The picker, when open, owns input over the dashboard (mirrors the
		// KeyPressMsg route below). Bracketed paste arrives as its own message type,
		// so without this the console-key / label fields never receive a paste.
		if a.picker.open {
			if cmd, consumed := a.pickerPaste(msg); consumed {
				return a, cmd
			}
			return a, nil
		}

	case tea.KeyPressMsg:
		if cmd, done := a.handleGlobalKey(msg); done {
			return a, cmd
		}

	// ---- Screen-switch messages ----

	case attachMsg:
		return a.handleAttach(msg)

	case viewFeedMsg:
		return a.handleViewFeed(msg)

	case createSessionMsg:
		// `n` opens the backend picker; provisioning happens when the user
		// confirms a choice (pickerKey → createCmd). The picker is an overlay
		// over the live dashboard, so the screen stays ScreenDashboard.
		a.connectErr = nil
		a.openBackendPicker()
		return a, nil

	case accountLoginDoneMsg:
		// The subscription login (tea.Exec terminal handover) finished; reflect
		// the new account (or its error) back in the still-open account picker.
		return a, a.handleAccountLoginDone(msg)

	case attachReadyMsg:
		return a.handleAttachReady(msg)

	case syncAdvisoryMsg:
		// Late background-sync/reaper advisory. The interactive pane owns the
		// screen and the read-only feed takes no notices from this path, so the
		// advisory is dropped rather than shown — the sync indicator on the list
		// row already surfaces a stalled sync.
		return a, nil

	case attachFailedMsg:
		return a.handleAttachFailed(msg)

	case externalPaneFinishedMsg:
		return a.handleExternalPaneFinished(msg)

	case connectUpdateMsg:
		return a.handleConnectUpdate(msg)

	case connectTickMsg:
		// Advance the connecting-screen spinner (U1.4).
		if a.screen != ScreenConnecting {
			return a, nil // self-stop when leaving
		}
		a.connectFrame++
		return a, connectTickCmd()

	case leaderTimeoutMsg:
		return a.handleLeaderTimeout(msg)
	}
	// Keep the dashboard's notion of the attached session current so background
	// attention toasts never fire for the session the user is already viewing.
	a.dashboard.attachedID = a.attachedSessionID()

	// Delegate to the dashboard EXACTLY ONCE per message (B17). The per-screen
	// switch below must NOT call a.dashboard.Update again; it only reuses dashCmd.
	dashCmd := a.delegateDashboard(msg)

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

	// Tap the same runner events the dashboard just reduced into the open
	// activity feed (claude-pane-first): the feed is a read-only monitor with no
	// stream of its own — it rides the background passive stream's events for
	// the session it is watching.
	if a.screen == ScreenFeed && a.feed != nil {
		a.tapFeed(msg)
	}

	switch a.screen {
	case ScreenExternal:
		return a.updateExternalScreen(msg, dashCmd)
	case ScreenFeed:
		return a.updateFeedScreen(msg, dashCmd)
	case ScreenConnecting:
		// Key presses never reach here: the top-level KeyPressMsg case cancels
		// the in-flight connect (and returns) while this screen is active.
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

// handlePtyOutput drains PTY output from an embedded external pane. Handled at
// the top level — not gated on the active screen — so the emulator stays current
// and the reader keeps draining even while the pane is minimized (which is what
// keeps the child from blocking and makes re-open instant).
func (a *App) handlePtyOutput(msg ptyOutputMsg) (tea.Model, tea.Cmd) {
	if a.external == nil || msg.pane != a.external {
		return a, nil // stale pane (replaced/closed); drop its trailing reads
	}
	cmd, finished := a.external.apply(msg.chunk)
	if finished {
		return a, func() tea.Msg { return externalPaneFinishedMsg{err: a.external.err} }
	}
	return a, cmd
}

// handleGlobalKey handles the screen-independent key intercepts (quit, connect
// cancel, backend-picker input). It returns (cmd, true) when the key was fully
// handled and Update should return, or (nil, false) to fall through to the
// per-screen delegation below.
func (a *App) handleGlobalKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	// Ctrl+C is always fatal regardless of screen.
	if msg.String() == "ctrl+c" {
		a.dashboard.Cancel()
		return tea.Quit, true
	}
	// Any key press in ScreenConnecting cancels the in-flight connection (U1).
	if a.screen == ScreenConnecting {
		if a.connectCancel != nil {
			a.connectCancel()
			a.connectCancel = nil
			a.connectCh = nil
		}
		// Anything still in flight from the cancelled goroutine is stale now.
		a.connectGen++
		a.connectingFor = nil
		a.connectStartedAt = time.Time{}
		a.screen = ScreenDashboard
		return nil, true
	}
	// The backend picker, when open, owns key input (over the dashboard).
	if a.picker.open {
		if cmd, consumed := a.pickerKey(msg); consumed {
			return cmd, true
		}
		return nil, true
	}
	return nil, false
}

// handleAttach services an attachMsg: restore a still-live external pane
// instantly, or kick off the connector and transition to the connecting splash.
func (a *App) handleAttach(msg attachMsg) (tea.Model, tea.Cmd) {
	// A live external pane for this same session was only minimized, not torn
	// down — restore it instantly (no reconnect) so toggling is immediate.
	if a.external != nil && !a.external.exited && a.external.sess.ID() == msg.sess.ID() {
		a.screen = ScreenExternal
		return a, nil
	}
	// Start the connector in a Cmd; transition to the "connecting" placeholder.
	// Every session is an external pane now — the real agent TUI repaints from
	// the pane stream itself, so there is no Go transcript preview to build.
	a.connectingFor = &msg.sess
	a.connectingOpencode = msg.sess.State.Backend == session.BackendOpenCode
	a.connectErr = nil
	a.screen = ScreenConnecting
	return a, a.connectCmd(msg.sess)
}

// handleViewFeed opens the read-only activity feed for an external-pane session.
// It builds a fresh feed model, seeds it from the host event cache (history where
// available), and ensures the background passive stream is live so activity
// keeps arriving via the event tap. No connection is established and the terminal
// is not handed over — attaching the pane (enter/a on the feed) does that.
func (a *App) handleViewFeed(msg viewFeedMsg) (tea.Model, tea.Cmd) {
	id := msg.sess.ID()
	f := newFeedModel(session.Ref{ID: id}, msg.sess.DisplayTitle(), ClientLabel(msg.sess.State.Backend))
	if a.dashboard.eventCache != nil {
		if events, err := a.dashboard.eventCache.LoadEvents(id); err == nil {
			f.seed(events)
		}
	}
	f.SetSize(a.width, a.height)
	a.feed = f
	a.screen = ScreenFeed
	// Ensure the background observer stream is live for this session so the feed
	// receives activity as it happens (idempotent — reconnects if it was cold).
	return a, a.dashboard.startLiveSSECmd(a.dashboard.sessionByID(id))
}

// tapFeed forwards the runner events the dashboard just reduced into the open
// feed (matching session only). It handles both the batched and single-event
// stream messages and narrates stream drops/reconnects as calm feed notices.
func (a *App) tapFeed(msg tea.Msg) {
	feedID := a.feed.ref.ID
	switch m := msg.(type) {
	case RunnerEventBatchMsg:
		if m.ID != feedID {
			return
		}
		for _, ev := range m.Events {
			a.feed.ingest(ev)
		}
		if m.StreamEnded {
			a.feed.notice("Connection lost — reconnecting…")
			a.feed.setConnection(true, false)
		} else if a.feed.reconnecting {
			a.feed.notice("Reconnected")
			a.feed.setConnection(false, false)
		}
	case RunnerEventMsg:
		if m.ID != feedID {
			return
		}
		if m.StreamEnded {
			a.feed.notice("Connection lost — reconnecting…")
			a.feed.setConnection(true, false)
			return
		}
		a.feed.ingest(m.Event)
		if a.feed.reconnecting {
			a.feed.notice("Reconnected")
			a.feed.setConnection(false, false)
		}
	}
}

// updateFeedScreen handles keys on the read-only activity feed: navigation,
// attach (enter/a) → hand off to the pane, and esc/back → dashboard. Every
// other key is swallowed (the feed accepts no text input). The window resize is
// applied so the feed reflows.
func (a *App) updateFeedScreen(msg tea.Msg, dashCmd tea.Cmd) (tea.Model, tea.Cmd) {
	if a.feed == nil {
		a.screen = ScreenDashboard
		return a, dashCmd
	}
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		a.feed.SetSize(ws.Width, ws.Height)
		return a, dashCmd
	}
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return a, dashCmd
	}
	switch kp.String() {
	case "esc", "ctrl+]", "q":
		// Back to the dashboard (the feed holds no resources to release; the row
		// cursor is still on the session the feed was opened from).
		a.feed = nil
		a.screen = ScreenDashboard
		return a, dashCmd
	case "enter", "a":
		// Attach the pane for full-fidelity interaction (the feed's one
		// session-directed action). Reuse the standard attach path.
		sess := a.dashboard.sessionByID(a.feed.ref.ID)
		a.feed = nil
		return a, tea.Batch(dashCmd, func() tea.Msg { return attachMsg{sess: sess} })
	case "up", "k":
		a.feed.scroll(-1)
	case "down", "j":
		a.feed.scroll(1)
	case "pgup", "ctrl+u":
		a.feed.scroll(-(a.feed.bodyHeight() / 2))
	case "pgdown", "ctrl+d":
		a.feed.scroll(a.feed.bodyHeight() / 2)
	case "g", "home":
		a.feed.top()
	case "G", "end":
		a.feed.bottom()
	case "ctrl+g":
		// Attention nav still works from the feed (parity with the pane leader).
		a.dashboard.jumpToNextNeedingAttention()
	}
	return a, dashCmd
}

// handleAttachReady embeds the external agent pane once the connector reports a
// live connection. Every session is an external pane now (claude-pane / opencode)
// — a local `opencode attach` child (opencodeCreds) or the in-pod claude child
// over the pane WebSocket (paneDial). It is called from the attachReadyMsg case
// arm and from handleConnectUpdate's ready branch.
func (a *App) handleAttachReady(msg attachReadyMsg) (tea.Model, tea.Cmd) {
	a.connectingFor = nil
	a.connectErr = nil
	a.connectStartedAt = time.Time{}
	// A connection with neither pane transport is a provisioning bug (an unknown
	// backend reached the attach path). Surface it inline rather than opening a
	// blank pane; release the connection's forwards so they don't leak.
	if msg.opencodeCreds == nil && msg.paneDial == nil {
		if msg.close != nil {
			msg.close()
		}
		a.connectErr = fmt.Errorf("session %s: no interactive pane transport for backend %q", msg.sess.ID(), msg.sess.State.Backend)
		a.dashboard.connectErr = a.connectErr
		a.screen = ScreenDashboard
		return a, nil
	}
	// O1: if a different session's pane is already live, close it first to
	// prevent goroutine/process leaks.
	if a.external != nil && a.external.sess.ID() != msg.sess.ID() {
		a.external.close()
		a.external = nil
	}
	// Do NOT cancel the background passive SSE here: the external pane is not a
	// runner SSE client — its byte stream is a separate connection — so the
	// passive stream is the ONLY thing feeding the runner observer's live
	// status/title/ctx%/cost into the read-model the pane's status row reads.
	// It's a passive stream, so it doesn't hold the idle reaper open.
	id := msg.sess.ID()
	live := func() Session { return a.dashboard.sessionByID(id) }
	var pane *ExternalPane
	if msg.paneDial != nil {
		pane = NewExternalPaneTransport(msg.sess, ClientLabel(msg.sess.State.Backend), msg.paneDial, live)
	} else {
		pane = NewExternalPane(msg.sess, *msg.opencodeCreds, live)
	}
	pane.w, pane.h = a.width, a.height
	// The pane owns the attach connection's forwards from here; its close()
	// releases them once the stream is down (§1d C1).
	pane.transportClose = msg.close
	a.external = pane
	a.screen = ScreenExternal
	// Mark everything seen for the session we're now viewing so its unread
	// badge clears the moment it comes to the foreground.
	for i := range a.dashboard.sessions {
		if a.dashboard.sessions[i].ID() == id {
			a.dashboard.sessions[i].seenSeq = a.dashboard.sessions[i].lastSeq
			break
		}
	}
	return a, pane.Init()
}

// handleAttachFailed stays on the dashboard and shows the connector error inline.
func (a *App) handleAttachFailed(msg attachFailedMsg) (tea.Model, tea.Cmd) {
	a.connectingFor = nil
	a.connectStartedAt = time.Time{}
	a.connectErr = msg.err
	a.dashboard.connectErr = msg.err
	a.screen = ScreenDashboard
	return a, nil
}

// handleExternalPaneFinished tears down the exited opencode-attach pane for real
// and returns to the dashboard, restarting the background SSE for the external
// session (B2) and surfacing a non-nil exit error inline.
func (a *App) handleExternalPaneFinished(msg externalPaneFinishedMsg) (tea.Model, tea.Cmd) {
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
}

// handleConnectUpdate consumes one progress item from the in-flight connect
// goroutine (U1). A stale generation means the attempt was cancelled or replaced:
// drop it silently so it can't surface "context canceled" as an error, flip
// screens mid-new-connect, attach a cancelled session, or re-arm the drain on the
// new attempt's channel (double reader).
func (a *App) handleConnectUpdate(msg connectUpdateMsg) (tea.Model, tea.Cmd) {
	if msg.gen != a.connectGen {
		// A stale READY carries a live connection nobody will ever own —
		// release its forwards instead of leaking them (§1d C1).
		if msg.ready != nil && msg.ready.close != nil {
			msg.ready.close()
		}
		return a, nil
	}
	switch {
	case msg.stage != nil:
		a.connectStage = *msg.stage
		a.connectDetail = msg.detail
		return a, connectNextCmd(a.connectCh) // keep draining
	case msg.ready != nil:
		a.connectCancel = nil
		a.connectCh = nil
		// VERIFIED: both the attachReadyMsg and attachFailedMsg case arms return
		// before Update's post-switch preamble (attachedID mirror + dashboard
		// delegation), so calling the handler directly here is exactly equivalent
		// to the old a.Update(*msg.ready) re-entry — nothing else in the router
		// ever ran for these messages.
		return a.handleAttachReady(*msg.ready)
	case msg.failed != nil:
		a.connectCancel = nil
		a.connectCh = nil
		if errors.Is(msg.failed.err, context.Canceled) {
			// A cancellation is user intent, not a failure — stay quiet.
			a.connectingFor = nil
			a.connectStartedAt = time.Time{}
			if a.screen == ScreenConnecting {
				a.screen = ScreenDashboard
			}
			return a, nil
		}
		// Same equivalence as the ready branch above (attachFailedMsg returns
		// before the post-switch preamble).
		return a.handleAttachFailed(*msg.failed)
	}
	return a, nil
}

// handleLeaderTimeout resolves a lone ctrl+] on the external pane to detach when
// the leader lapses. Only act on the tick from the CURRENT arming while still
// armed and on the external screen: the gen guard drops a tick superseded by a
// re-arm, and the screen guard keeps a stale tick from leaking into another
// screen. This is exactly the leaderDetach path.
func (a *App) handleLeaderTimeout(msg leaderTimeoutMsg) (tea.Model, tea.Cmd) {
	if a.leaderArmed && msg.gen == a.leaderGen && a.screen == ScreenExternal {
		a.leaderArmed = false
		a.leaderGen++
		a.screen = ScreenDashboard
	}
	return a, nil
}

// delegateDashboard is the ONLY a.dashboard.Update call site (B17): the
// dashboard sees every non-key message plus keys on its own screen, exactly
// once per App.Update pass. Never call a.dashboard.Update anywhere else.
func (a *App) delegateDashboard(msg tea.Msg) tea.Cmd {
	// On the dashboard screen it owns all input, so every message goes to it
	// here. Behind a screen (external pane / feed / connecting) it still
	// processes background (non-key) messages so its live state and toast
	// notifications stay current — but key presses there belong to the active
	// screen, not the dashboard.
	var dashCmd tea.Cmd
	if _, isKey := msg.(tea.KeyPressMsg); a.screen == ScreenDashboard || !isKey {
		next, cmd := a.dashboard.Update(msg)
		if dm, ok := next.(*Model); ok {
			a.dashboard = dm
		}
		dashCmd = cmd
	}
	return dashCmd
}

// updateExternalScreen routes a message on the external (opencode PTY) screen:
// ctrl+] is a leader chord for attention-nav / detach and everything else is
// forwarded to the embedded client. It reuses the already-computed dashCmd (B17).
func (a *App) updateExternalScreen(msg tea.Msg, dashCmd tea.Cmd) (tea.Model, tea.Cmd) {
	if a.external == nil {
		// Pane vanished: leave the external screen and disarm so a stale leader
		// can't outlive it. (The leaderTimeoutMsg guard already refuses to act
		// off ScreenExternal, but keeping the state clean is cheap.)
		a.leaderArmed = false
		a.leaderGen++
		a.screen = ScreenDashboard
		return a, dashCmd
	}
	if kp, ok := msg.(tea.KeyPressMsg); ok {
		// ctrl+] is a leader chord here, not an instant detach. It was already
		// the reserved detach key, so extending it into a prefix gives the pane
		// attention-nav (ctrl+] g / ctrl+] k jump to the next/prev session
		// needing you) without stealing any key the embedded opencode client
		// binds — the arming ctrl+] is swallowed, never forwarded. esc is still
		// forwarded so opencode keeps it for its own overlays; a lone ctrl+]
		// detaches once the chord lapses (a second ctrl+] or the leaderTimeout),
		// the deliberate cost of making ctrl+] a prefix (TODO §2d, 2026-07-07).
		switch leaderStep(a.leaderArmed, kp.String()) {
		case leaderArm:
			a.leaderArmed = true
			a.leaderGen++
			return a, tea.Batch(dashCmd, leaderTimeoutCmd(a.leaderGen))
		case leaderDetach:
			// Detach (back to the dashboard) without tearing down the pane — the
			// child keeps running so re-open is instant.
			a.leaderArmed = false
			a.leaderGen++
			a.screen = ScreenDashboard
			return a, dashCmd
		case leaderJumpNext:
			a.leaderArmed = false
			a.leaderGen++
			return a, a.leaderJump(dashCmd, a.dashboard.jumpToNextNeedingAttention())
		case leaderJumpPrev:
			a.leaderArmed = false
			a.leaderGen++
			return a, a.leaderJump(dashCmd, a.dashboard.jumpToPrevNeedingAttention())
		case leaderForward:
			// Disarm and forward THIS key to the child; the earlier arming
			// ctrl+] never reached it.
			a.leaderArmed = false
			a.leaderGen++
			a.external.handleKey(kp)
			return a, dashCmd
		default: // leaderIgnore
			// Not armed, not a leader key (incl. esc): forward to opencode.
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
}

// leaderJump completes an external-pane leader g/k jump. With a target session it
// minimizes the current pane (screen → dashboard; the child keeps running, and
// attachReadyMsg's O1 branch closes it only if a different session's pane comes
// up) and emits an attachMsg for the target — mirroring the transcript ctrl+g
// path (app.go, ScreenTranscript) minus its transcript-only park/live-SSE work.
// With nil (nothing needs attention) it stays on the external screen, already
// disarmed by the caller.
func (a *App) leaderJump(dashCmd tea.Cmd, target *Session) tea.Cmd {
	if target == nil {
		return dashCmd
	}
	a.screen = ScreenDashboard
	sess := *target
	return tea.Batch(dashCmd, func() tea.Msg { return attachMsg{sess: sess} })
}

// attachedSessionID returns the id of the session the user is currently viewing
// (external pane or activity feed), or "" when on the bare dashboard. It is the
// exclusion key for background-attention toasts.
func (a *App) attachedSessionID() session.ID {
	switch {
	case a.screen == ScreenExternal && a.external != nil:
		return a.external.sess.ID()
	case a.screen == ScreenFeed && a.feed != nil:
		return a.feed.ref.ID
	}
	return ""
}

// View renders the active screen (dashboard list, connecting splash, external
// pane, or the read-only activity feed), floating the cross-session attention
// toast over it.
func (a *App) View() tea.View {
	v := a.withToast(a.screenView())
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
	// §2c: the session identity moved off a persistent in-frame header onto the
	// terminal tab. bubbletea v2 diffs v.WindowTitle per frame and emits the OSC
	// itself, so we just declare the desired title — no manual escapes, no edge
	// tracking.
	v.WindowTitle = a.windowTitle()
	return v
}

// windowTitle is the terminal tab title for the current screen (§2c). It carries
// the session identity that used to live in the transcript's top header band:
// the attached session's display title on the transcript/external screens, and a
// plain "sandbox" everywhere else (dashboard, connecting, picker). bubbletea v2
// emits the OSC title sequence whenever this string changes between frames.
func (a *App) windowTitle() string {
	switch a.screen {
	case ScreenExternal:
		if a.external != nil {
			if t := a.external.session().DisplayTitle(); t != "" {
				return t
			}
		}
	case ScreenFeed:
		if a.feed != nil && a.feed.title != "" {
			return a.feed.title
		}
	}
	return "sandbox"
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
	case ScreenFeed:
		if a.feed == nil {
			return a.dashboard.View()
		}
		return tea.NewView(a.feed.View())
	default:
		return a.dashboard.View()
	}
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
	a.connectGen++
	gen := a.connectGen
	a.connectStage = StageCheck
	a.connectDetail = ""
	a.connectStartedAt = nowFunc()
	// Preempt the background observer connect burst for the duration of this
	// foreground attach (§5 leftover): observers pause on the gate so the attach
	// the user is waiting on wins the kube-apiserver.
	gate := a.dashboard.attachGate
	go func() {
		defer cancel()
		gate.enter()
		defer gate.exit()
		onStage := func(s ConnectStage, detail string) {
			select {
			case ch <- connectUpdateMsg{gen: gen, stage: &s, detail: detail}:
			case <-ctx.Done():
			}
		}
		res, err := connector(ctx, session.Ref{ID: sess.ID()}, sess.State.ProjectPath, onStage)
		if err != nil {
			ch <- connectUpdateMsg{gen: gen, failed: &attachFailedMsg{sess: session.Ref{ID: sess.ID()}, err: err}}
		} else {
			ready := attachReadyMsg{
				sess:          sess,
				client:        res.Client,
				reconnect:     res.Reconnect,
				endpoint:      res.Endpoint,
				opencodeCreds: res.OpencodeCreds,
				paneDial:      res.PaneDial,
				warning:       res.Warning,
				awaitWarning:  res.AwaitWarning,
				close:         res.Close,
			}
			ch <- connectUpdateMsg{gen: gen, ready: &ready}
		}
		close(ch)
	}()
	return tea.Batch(connectNextCmd(ch), connectTickCmd())
}

// createCmd provisions a brand-new session via the Creator in a background
// goroutine that streams ConnectStage updates (U1). params carries the chosen
// backend and (for claude) the selected Anthropic account id; an empty account
// id is the legacy/cluster-default path.
func (a *App) createCmd(params CreateParams) tea.Cmd {
	backend := params.Backend
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
	a.connectGen++
	gen := a.connectGen
	// The Creator's connect path (establish) emits StageCheck first, then advances
	// to StageResume while the freshly-provisioned pod schedules + pulls its image.
	// Initialize at StageCheck so the stepper doesn't briefly regress Resume→Check
	// on the first emitted update (the pod wait moved out of a pre-connect
	// backend.Start into establish — Phase 2).
	a.connectStage = StageCheck
	a.connectDetail = ""
	a.connectStartedAt = nowFunc()
	a.connectingOpencode = backend == session.BackendOpenCode
	// Preempt the observer connect burst while the new session provisions (§5).
	gate := a.dashboard.attachGate
	go func() {
		defer cancel()
		gate.enter()
		defer gate.exit()
		onStage := func(s ConnectStage, detail string) {
			select {
			case ch <- connectUpdateMsg{gen: gen, stage: &s, detail: detail}:
			case <-ctx.Done():
			}
		}
		res, err := creator(ctx, params, onStage)
		if err != nil {
			ch <- connectUpdateMsg{gen: gen, failed: &attachFailedMsg{err: err}}
		} else {
			ready := attachReadyMsg{
				sess:          SessionFromState(res.State),
				client:        res.Client,
				reconnect:     res.Reconnect,
				endpoint:      res.Endpoint,
				opencodeCreds: res.OpencodeCreds,
				paneDial:      res.PaneDial,
				warning:       res.Warning, // RV23: surface new-session sync warnings
				awaitWarning:  res.AwaitWarning,
				close:         res.Close,
			}
			ch <- connectUpdateMsg{gen: gen, ready: &ready}
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
// connectingView renders the animated connect screen: a block-pixel mascot, the
// title (text ramp, not bold brand color), an animated stepper, and a cancel
// hint, centered on screen.
func (a *App) connectingView() tea.View {
	title := "Connecting…"
	if a.connectingFor != nil {
		title = fmt.Sprintf("Connecting to %s…", a.connectingFor.Title)
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

	centered := lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center, body, pageWhitespace())
	v := tea.NewView(centered)
	v.AltScreen = true
	return v
}

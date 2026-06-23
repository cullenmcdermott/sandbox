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
	"github.com/cullenmcdermott/sandbox/internal/terminal"
	"github.com/cullenmcdermott/sandbox/tui/kit"
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
	reconnect     func(ctx context.Context) (RunnerClient, error)
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

	// progressActive tracks whether an OSC 9;4 tab-progress indicator is
	// currently set, so View emits a one-shot "clear" when the aggregate returns
	// to idle rather than re-emitting every frame (Stage 2). Only ever true on a
	// Ghostty terminal.
	progressActive bool

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

	// connectStage is the latest ConnectStage reported by the connector (U1).
	connectStage ConnectStage

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
		// Cancel the dashboard's background SSE for this session so we don't
		// have two concurrent SSE clients to the same runner (B2).
		a.dashboard.cancelLiveSSE(msg.sess.ID())
		// opencode-server sessions don't have a Go transcript; the local
		// `opencode attach` client owns the UI, embedded as a Tier-2 PTY pane.
		if msg.opencodeCreds != nil {
			// O1: if a different opencode session's pane is already live, close
			// it first to prevent goroutine/process leaks.
			if a.external != nil && a.external.sess.ID() != msg.sess.ID() {
				a.external.close()
				a.external = nil
			}
			pane := NewExternalPane(msg.sess, *msg.opencodeCreds)
			pane.w, pane.h = a.width, a.height
			a.external = pane
			a.screen = ScreenExternal
			return a, pane.Init()
		}
		// Reuse the warm model if this session was already retained (instant, with
		// the progress made while hidden); otherwise build a fresh one (cold open)
		// and register it as warm so future hide/show are O(1).
		var m *TranscriptModel
		if existing, ok := a.dashboard.retainedTranscript(msg.sess.ID()); ok {
			m = existing
			m.client = msg.client         // install the live (active) client
			m.reconnect = msg.reconnect   // and its reconnect callback
			m.seedSize(a.width, a.height) // background model never got a WindowSizeMsg
		} else {
			m = NewTranscript(msg.client, msg.sess, msg.reconnect)
			a.dashboard.putRetained(msg.sess.ID(), m)
		}
		// Thread detected terminal capabilities into the transcript so its
		// status-line effects (ctx-gauge sweep, etc.) light up only on a capable
		// terminal; the dashboard Model detected them once at startup.
		m.caps = a.dashboard.caps
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
			case "esc", "ctrl+]", "ctrl+4":
				// Universal escape: minimize back to the dashboard WITHOUT tearing
				// down the pane — the child keeps running so re-open is instant.
				a.screen = ScreenDashboard
				return a, dashCmd
			default:
				// Everything else is forwarded to the embedded opencode client.
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
	return a.withTerminalSignals(a.screenView())
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

// withTerminalSignals prepends the Stage 2 zero-width OSC control strings to the
// composed frame: the OSC 9;4 tab-progress state (recomputed each frame from the
// session aggregate) and any one-shot desktop notification queued during Update.
// Both are no-ops on a non-Ghostty terminal (progressState returns None and
// pendingOSC is never set), so non-Ghostty output is byte-identical to today.
// The external (opencode) PTY screen is left untouched — it owns the terminal.
func (a *App) withTerminalSignals(v tea.View) tea.View {
	if a.dashboard == nil || a.screen == ScreenExternal {
		return v
	}
	var pre strings.Builder
	// Tab progress: emit when active, plus a single clear on the active→idle
	// transition. progressState already returns None on non-Ghostty terminals.
	p := a.dashboard.progressState()
	if p != 0 { // terminal.ProgressNone == 0
		pre.WriteString(terminal.OSCProgress(p))
		a.progressActive = true
	} else if a.progressActive {
		pre.WriteString(terminal.OSCProgress(p))
		a.progressActive = false
	}
	// One-shot desktop notification queued by the toast transition.
	pre.WriteString(a.dashboard.takePendingOSC())
	// Stage 3: one-shot Kitty image transmission queued by the transcript's ctx
	// gauge when its value changed this frame (the only sanctioned out-of-band
	// write — it rides the frame on the changing frame, not every frame).
	// Prepended so the image exists before the placeholder cells reference it.
	if a.transcript != nil {
		pre.WriteString(a.transcript.takePendingKitty())
	}

	if pre.Len() == 0 {
		return v
	}
	v.Content = pre.String() + v.Content
	return v
}

// modalView composites the dashboard background with the transcript as a
// floating modal. z-order: dashboard < shadow < modal.
func (a *App) modalView() tea.View {
	bg := a.dashboard.View().Content
	w, h := a.width, a.height
	if w == 0 || h == 0 {
		v := tea.NewView(bg)
		v.AltScreen = true
		return v
	}

	mw := w - 6
	mh := h - 4
	if mw < 20 {
		mw = 20
	}
	if mh < 6 {
		mh = 6
	}
	mx := (w - mw) / 2
	my := (h - mh) / 2

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
		// Ghost the dashboard behind the popup so it reads as "out of focus"
		// context instead of bleeding live (colored) rows past the modal edges.
		lipgloss.NewLayer(dimBackdrop(bg, w, h)).X(0).Y(0).Z(0),
		lipgloss.NewLayer(shadow).X(mx + 2).Y(my + 1).Z(1),
		lipgloss.NewLayer(modal).X(mx).Y(my).Z(2),
	}

	canvas := lipgloss.NewCanvas(w, h)
	canvas.Compose(lipgloss.NewCompositor(layers...))
	v := tea.NewView(canvas.Render())
	v.AltScreen = true
	return v
}

// dimBackdrop ghosts the dashboard behind a modal: it strips each line's colors
// and re-renders them as dim text on the flat page background, normalized to a
// solid w×h block. The result is recognizably the dashboard, but recessed — so
// a floating popup reads as the focus and live rows (e.g. the status line) don't
// bleed through at full brightness past the modal's edges.
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
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	a.connectCancel = cancel
	ch := make(chan connectUpdateMsg, 8)
	a.connectCh = ch
	a.connectStage = StageCheck
	go func() {
		defer cancel()
		onStage := func(s ConnectStage) {
			select {
			case ch <- connectUpdateMsg{stage: &s}:
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
	a.connectStage = StageResume
	a.connectingOpencode = backend == session.BackendOpenCode
	go func() {
		defer cancel()
		onStage := func(s ConnectStage) {
			select {
			case ch <- connectUpdateMsg{stage: &s}:
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

// connectingView renders the animated "connecting…" screen (U1.5):
// title in theme.TextBright (text ramp, not bold Charple brand color), animated
// stepper, and hint in theme.TextMuted.
func (a *App) connectingView() tea.View {
	title := "Connecting…"
	if a.connectingFor != nil {
		title = fmt.Sprintf("Connecting to %s…", a.connectingFor.Title)
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
	stepper := connectingStepper(a.connectStage, a.connectFrame, applicable)

	hint := lipgloss.NewStyle().
		Foreground(theme.TextMuted).
		Render("(press any key to cancel)")

	// Build the block left-aligned (JoinVertical pads every line to the widest,
	// so the stepper rows line up relative to each other) before centering the
	// whole panel on screen. Centering the raw multi-line string instead would
	// center each line independently and render the steps ragged (T2).
	body := lipgloss.JoinVertical(lipgloss.Left, titleLine, "", stepper, "", hint)
	centered := lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center, body)

	v := tea.NewView(centered)
	v.AltScreen = true
	return v
}

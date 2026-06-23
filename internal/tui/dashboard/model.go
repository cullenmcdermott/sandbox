package dashboard

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/internal/terminal"
	"github.com/cullenmcdermott/sandbox/tui/anim"
	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// --------------------------------------------------------------------------
// Messages
// --------------------------------------------------------------------------

// PodEventMsg carries a single cluster-watch delta for one session. The
// dashboard's Update handler patches exactly that one session in the read-model.
type PodEventMsg struct {
	Event k8s.StateEvent
}

// animTickMsg drives the single gated motion loop (§C.2): one ~30fps tick that
// re-renders so every time-based interpolation (spinner hue, glyph fade-in)
// advances. It is only scheduled while anyMotionActive and self-stops otherwise.
type animTickMsg struct{}

// seedMsg carries the initial session list from Backend.List.
type seedMsg []session.State

// RunnerEventMsg carries a single SSE event from a live runner connection for
// one session. The Update handler applies ApplyRunnerEvent to patch that
// session's DashStatus in the read-model.
type RunnerEventMsg struct {
	// ID is the session the event belongs to.
	ID session.ID
	// Event is the normalized runner event.
	Event session.Event
	// StreamEnded is true when the SSE channel was closed without an event
	// (connection lost). The handler degrades to cluster-derived status.
	StreamEnded bool
}

// liveSSEReadyMsg is returned by the SSE-start Cmd when the channel is open.
type liveSSEReadyMsg struct {
	id     session.ID
	ch     <-chan session.Event
	cancel context.CancelFunc
	client RunnerClient
}

// liveSSEReconnectMsg fires after a backoff delay to (re)attempt a background
// status stream for a session whose stream dropped while the cluster still
// believes the pod is Running. attempt is the 0-based try number. This is how a
// transient port-forward blip self-heals instead of sticking a false 'failed'
// glyph on a perfectly healthy backgrounded session (RV1).
type liveSSEReconnectMsg struct {
	id      session.ID
	attempt int
}

// liveSSEReconnectFailedMsg reports that a background reconnect attempt could not
// open the stream. The handler retries with backoff up to liveSSEMaxRetries, then
// degrades the session to its honest status (Failed if it was mid-turn — B13's
// "runner unreachable = failed" — else cluster-derived).
type liveSSEReconnectFailedMsg struct {
	id      session.ID
	attempt int
}

// snapshotSaveInterval throttles non-transition snapshot writes (the usage
// stream fires often). Status transitions always persist immediately; usage-only
// updates coalesce to at most one disk write per interval per session, so a
// relaunch resumes within one interval of head without thrashing the index.
const snapshotSaveInterval = 3 * time.Second

// liveSSEMaxRetries bounds background-stream reconnect attempts before a session
// is declared unreachable. Small: a transient forward blip heals on attempt 0–1;
// a genuinely dead runner is surfaced as 'failed' within a few seconds.
const liveSSEMaxRetries = 3

// liveSSEReconnectDelay is the backoff before reconnect attempt n (2s, 4s, 8s…,
// capped) — long enough to ride out a port-forward re-establishment without
// hammering the connector (which resumes/port-forwards/health-checks each try).
func liveSSEReconnectDelay(attempt int) time.Duration {
	d := 2 * time.Second * (1 << attempt)
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

// liveSSEReconnectTick schedules the next reconnect attempt after a backoff.
func liveSSEReconnectTick(id session.ID, attempt int, delay time.Duration) tea.Cmd {
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return liveSSEReconnectMsg{id: id, attempt: attempt}
	})
}

// approveResultMsg signals that a ResolvePermission call completed (or failed).
type approveResultMsg struct {
	id  session.ID
	err error
}

// --------------------------------------------------------------------------
// Spinner frames
// --------------------------------------------------------------------------

// animFPS is the cadence of the single motion tick (~30fps). spinnerSubRate is
// how many ticks pass between busy-glyph advances, preserving the ~200ms spinner
// cadence off the faster shared loop.
const (
	animFPS        = 33 * time.Millisecond
	spinnerSubRate = 6
)

// --------------------------------------------------------------------------
// Model
// --------------------------------------------------------------------------

// Model is the Bubble Tea v2 model for the command-center dashboard.
// It is a child of the root App model (see app.go) but is also a valid
// tea.Model on its own for unit testing.
type Model struct {
	// backend is used to seed sessions on init.
	backend Backend

	// connector is used to open live SSE streams for per-session live status.
	// May be nil (no live status, graceful degradation).
	connector Connector

	// sessions is the canonical read-model: all live sessions, kept in the
	// current sort order. The watch Cmd patches individual entries here.
	sessions []Session

	// seeded is true once the first seedMsg (or the first PodEventMsg) has been
	// processed. Before seeded, the list shows skeleton bars; after seeded with
	// no sessions, it shows the first-run CTA (U2, spec 04-ux-responsiveness).
	seeded bool

	// attentionFirst, when true, floats Waiting/NeedsInput sessions to the top of
	// the list regardless of sort order (D4 attention routing). Toggled by the
	// AttentionToggle key binding.
	attentionFirst bool

	// Sorting
	sortKey SortKey
	sortDir SortDir

	// Filter
	filtering bool   // true while the / prompt is active
	filterBuf string // the live filter query being typed
	filter    string // the committed filter applied to the list view

	// Cursor in the filtered+sorted visible list.
	cursor int

	// ggPending tracks whether the user has typed one `g` (awaiting second `g`
	// for gg → top). Reset on any non-g key.
	ggPending bool

	// Help overlay visible, plus its grouped/expandable state.
	showHelp bool
	helpUI   helpModel

	// Cross-session "needs you" toast notification.
	toast           *notification
	toastTickActive bool

	// attachedID is the session the user is currently attached to (set by the
	// App on attach, cleared on detach). It is excluded from background-attention
	// toasts so the session you're already looking at never toasts itself.
	attachedID session.ID

	// ⌃K fuzzy quick-switcher overlay.
	switcher switcherModel

	// Pending-permission queue view.
	permQueue permQueueModel

	// Group-by-repo, rename, archive state.
	groupView groupViewState
	renaming  bool
	renameBuf string

	// Key map and its help renderer. The footer and `?` overlay are both
	// produced from `keys` via `help`, so they can't drift from the handlers.
	keys KeyMap
	help help.Model

	// Terminal size.
	width  int
	height int

	// Watch context cancel — called when the model is torn down so the
	// informer goroutine stops.
	watchCancel context.CancelFunc

	// liveSSECancels holds the cancel function for each session's live-status
	// SSE stream. Keyed by session.ID. Cancelled on quit or when the session
	// is removed/becomes non-running.
	liveSSECancels map[session.ID]context.CancelFunc

	// liveSSEChannels holds the open event channel for each session's live-
	// status SSE stream, so handleRunnerEvent can re-issue the next-read Cmd.
	liveSSEChannels map[session.ID]<-chan session.Event

	// liveSSEClients holds the live runner client for each open SSE stream so
	// approve/deny reuses the already-open port-forward instead of dialing a
	// fresh connection. Keyed by session.ID.
	liveSSEClients map[session.ID]RunnerClient

	// retained holds a live TranscriptModel for each warm (running-pod) session,
	// fed in the background by handleRunnerEvent. A warm session's model is never
	// destroyed while its pod runs, so showing it is an O(1) swap (see warm.go).
	retained map[session.ID]*TranscriptModel

	// Spinner frame index (global; applied per busy row).
	spinnerFrame int

	// engine is the unified motion engine (chat-styling-and-motion §C.2). It
	// tracks running spinners so the single gated tick loop only schedules while
	// motion is active.
	engine *anim.Engine

	// animating is true while the single gated motion tick loop is running. The
	// tick is only scheduled while anyMotionActive (a busy spinner or a row
	// mid-fade); it self-stops otherwise so an idle dashboard burns no CPU.
	animating bool

	// animSubFrame counts 30fps ticks; the slower busy-glyph spinner advances
	// every spinnerSubRate ticks so its cadence is preserved off the one loop.
	animSubFrame int

	// connectErr is the last attach error from the App, surfaced in the detail pane.
	connectErr error

	// confirm, when non-nil, is an active destructive-action confirmation
	// (currently only destroy). It renders a centered y/n dialog and captures
	// keys until resolved.
	confirm *confirmPrompt

	// destroyHook, when non-nil, is called after a successful backend.Destroy
	// to perform irreversible local cleanup (SSH alias removal, key deletion,
	// index entry removal). Wired by the CLI via WithDestroyHook (C2: TUI destroy
	// leaked local state).
	destroyHook func(id session.ID)

	// preDestroyHook, when non-nil, runs BEFORE backend.Destroy. It stops file
	// sync for the session so we tear the pod down cleanly instead of racing the
	// mutagen-over-SSH stream into "connection closed"/EOF errors. It is kept
	// separate from destroyHook because it must run regardless of whether Destroy
	// then succeeds, and unlike destroyHook it is recoverable (a re-attach
	// re-establishes sync).
	preDestroyHook func(id session.ID)

	// titleStore, when non-nil, persists user-chosen session titles across
	// restarts (T5). Renames write through it; seeded sessions read back from it.
	// nil in unit tests (renames stay in-memory).
	titleStore TitleStore

	// snapStore, when non-nil, persists the per-session live read-model so a
	// relaunch hydrates instantly and resumes the SSE stream from the cached seq
	// instead of replaying full history. nil in unit tests (no caching).
	snapStore SnapshotStore

	// actionErr is the last suspend/resume/destroy/create error, surfaced in
	// the detail pane. Cleared when an action succeeds.
	actionErr error

	// caps holds terminal capabilities detected once at startup. Every opt-in
	// Ghostty/terminal effect is gated behind a caps field; a zero Caps (the
	// default in tests) lights up nothing, so output matches today exactly.
	// See docs/ghostty-terminal-effects.md.
	caps terminal.Caps

	// pendingOSC holds a one-shot OSC control string (currently a desktop
	// notification) queued during Update and drained into the next frame by the
	// root App.View. Riding the frame string keeps notifications on the single
	// sanctioned output channel (Stage 2). Empty when nothing is queued; only
	// ever set when caps.IsGhostty.
	pendingOSC string
}

// New constructs a dashboard Model. backend may be nil (for unit tests that
// drive the model with manual seedMsg / PodEventMsg messages).
// connector may be nil; live per-session status will be skipped gracefully.
func New(backend Backend) *Model {
	return &Model{
		backend:         backend,
		sortKey:         SortByLastActive,
		sortDir:         SortDesc,
		keys:            DefaultKeyMap(),
		help:            newHelp(),
		liveSSECancels:  make(map[session.ID]context.CancelFunc),
		liveSSEChannels: make(map[session.ID]<-chan session.Event),
		liveSSEClients:  make(map[session.ID]RunnerClient),
		retained:        make(map[session.ID]*TranscriptModel),
		engine:          anim.NewEngine(),
		caps:            terminal.Detect(),
	}
}

// newHelp builds a bubbles help.Model styled with the charmtone palette so the
// footer and `?` overlay match the rest of the dashboard.
func newHelp() help.Model {
	h := help.New()
	h.ShortSeparator = "  ·  "
	h.Styles.ShortKey = lipgloss.NewStyle().Foreground(theme.Malibu)
	// Desc was theme.TextDim (#46406A) on theme.Surface — effectively dark-on-dark
	// and unreadable (T4). Use body text for the footer hints.
	h.Styles.ShortDesc = lipgloss.NewStyle().Foreground(theme.TextBody)
	h.Styles.ShortSeparator = lipgloss.NewStyle().Foreground(theme.BorderMedium)
	h.Styles.FullKey = lipgloss.NewStyle().Foreground(theme.Malibu)
	h.Styles.FullDesc = lipgloss.NewStyle().Foreground(theme.TextSecondary)
	h.Styles.FullSeparator = lipgloss.NewStyle().Foreground(theme.BorderMedium)
	h.Styles.Ellipsis = lipgloss.NewStyle().Foreground(theme.TextDim)
	return h
}

// WithConnector sets the Connector for live per-session SSE status updates.
// Call before Init to ensure SSE streams are opened on the initial seed.
func (m *Model) WithConnector(c Connector) *Model {
	m.connector = c
	return m
}

// WithDestroyHook registers a callback that is called after a successful
// backend.Destroy so the caller can perform local cleanup (sync teardown,
// SSH alias removal, key deletion). The CLI uses this to match what the
// `destroy` subcommand does (C2 fix).
func (m *Model) WithDestroyHook(fn func(id session.ID)) *Model {
	m.destroyHook = fn
	return m
}

// WithPreDestroyHook registers a callback run before backend.Destroy so the
// caller can stop file sync ahead of pod teardown, avoiding the mutagen-over-SSH
// stream erroring as the pod disappears.
func (m *Model) WithPreDestroyHook(fn func(id session.ID)) *Model {
	m.preDestroyHook = fn
	return m
}

// TitleStore persists user-chosen session titles so a rename survives a restart
// or reattach (T5). The dashboard reads it when seeding the list and writes to it
// when a rename is committed. Implemented by the CLI on top of the local index.
//
// It also persists the runner-generated auto title (T6) so the auto title
// survives a re-seed, where the cluster state carries no local label.
type TitleStore interface {
	LoadTitle(id session.ID) string
	SaveTitle(id session.ID, title string)
	LoadAutoTitle(id session.ID) string
	SaveAutoTitle(id session.ID, title string)
	// SaveClaudeSessionID persists the Claude SDK session UUID for a session so
	// the CLI can later make it resumable from the laptop (history.jsonl entry).
	SaveClaudeSessionID(id session.ID, claudeID string)
}

// WithTitleStore registers the persistent store for renamed session titles.
func (m *Model) WithTitleStore(s TitleStore) *Model {
	m.titleStore = s
	return m
}

// SessionSnapshot is the cached live read-model for one session at LastSeq. It
// is persisted locally (via SnapshotStore) so a relaunch can render the row's
// real status/usage immediately and resume the runner SSE stream from LastSeq
// instead of replaying the full event history (which is what made the dashboard
// flash notifications and count usage up from zero on every launch).
type SessionSnapshot struct {
	LastSeq               uint64
	DashStatus            SessionStatus
	PendingPermissionID   string
	PendingPermissionTool string
	Model                 string
	InputTokens           int
	OutputTokens          int
	CacheReadTokens       int
	CacheWriteTokens      int
	TotalCostUSD          float64
}

// SnapshotStore persists the per-session live read-model across restarts so the
// dashboard can hydrate instantly and resume rather than replay. Implemented by
// the CLI on top of the local index; nil in unit tests (no caching).
type SnapshotStore interface {
	// LoadSnapshot returns the cached snapshot for a session; ok is false when
	// none has been persisted yet.
	LoadSnapshot(id session.ID) (snap SessionSnapshot, ok bool)
	// SaveSnapshot persists the snapshot for a session.
	SaveSnapshot(id session.ID, snap SessionSnapshot)
}

// WithSnapshotStore registers the persistent per-session snapshot store.
func (m *Model) WithSnapshotStore(s SnapshotStore) *Model {
	m.snapStore = s
	return m
}

// --------------------------------------------------------------------------
// tea.Model interface
// --------------------------------------------------------------------------

// Init seeds the session list from the backend and starts the cluster watch.
func (m *Model) Init() tea.Cmd {
	var cmds []tea.Cmd

	if m.backend != nil {
		// Seed the list synchronously-ish via a Cmd so Init stays non-blocking.
		cmds = append(cmds, m.seedCmd())
		// Start the cluster watch.
		cmds = append(cmds, m.startWatchCmd())
	}

	return tea.Batch(cmds...)
}

// Update is the pure message handler. All I/O arrives as a tea.Msg.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case seedMsg:
		next, cmds := m.applySeed([]session.State(msg))
		cmds = append(cmds, m.maybeStartAnim())
		return next, tea.Batch(cmds...)

	case PodEventMsg:
		m.seeded = true // first watch event counts as loaded (U2)
		cmd := m.applyPodEvent(msg.Event)
		return m, tea.Batch(cmd, m.maybeStartAnim())

	case animTickMsg:
		// One gated loop drives all motion (§C.2). The busy glyph advances at a
		// sub-rate so its hue still pulses across the spinner ramp; the fade-in
		// interpolates from elapsed time in the View. Reschedule only while
		// motion is active so an idle dashboard schedules no timer.
		m.animSubFrame++
		if m.animSubFrame%spinnerSubRate == 0 {
			m.spinnerFrame++
		}
		if m.anyMotionActive() {
			return m, m.animCmd()
		}
		m.animating = false
		return m, nil

	case watchReadyMsg:
		return m.handleWatchReady(msg)
	case toastTickMsg:
		if m.toast == nil {
			m.toastTickActive = false
			return m, nil
		}
		if time.Since(m.toast.createdAt) > toastDismissAfter {
			m.toast = nil
			m.toastTickActive = false
			return m, nil
		}
		m.toastTickActive = true
		return m, toastTickCmd()

	case liveSSEReadyMsg:
		// Guard: if the session was deleted or suspended while the connector
		// was in flight, cancel the stream immediately (B14). We check the
		// session still exists and is still in a Running state before storing
		// the cancel; otherwise the stream would run until program exit.
		sess := m.sessionByID(msg.id)
		if sess.ID() == "" || sess.State.Status != session.StatusRunning {
			msg.cancel()
			return m, nil
		}
		// Store the cancel, channel, and client for this session's SSE stream.
		m.liveSSECancels[msg.id] = msg.cancel
		m.liveSSEChannels[msg.id] = msg.ch
		m.liveSSEClients[msg.id] = msg.client
		// Build (or reuse) the warm transcript for this session so the background
		// stream keeps a full, live chat in memory — making a later show an O(1)
		// swap instead of a rebuild+replay. Skip opencode sessions (no Go
		// transcript) and skip if this session is currently the foreground one
		// (its own active stream already owns the model).
		if sess.State.Backend != session.BackendOpenCode && msg.id != m.attachedID {
			m.ensureRetained(sess, msg.client)
			m.maybeWarnWarm()
		}
		return m, liveSSENextCmd(msg.id, msg.ch)

	case liveSSEReconnectMsg:
		// Backoff elapsed — try to re-open the background stream, unless the
		// session is gone/suspended (cluster watch owns its glyph now) or a pod
		// event already re-established the stream while we waited.
		sess := m.sessionByID(msg.id)
		if sess.ID() == "" || sess.State.Status != session.StatusRunning {
			return m, nil
		}
		if _, exists := m.liveSSECancels[msg.id]; exists {
			return m, nil
		}
		return m, m.reconnectLiveSSECmd(sess, msg.attempt)

	case liveSSEReconnectFailedMsg:
		// A reconnect attempt couldn't open the stream. Retry with backoff while
		// the cluster still believes the pod is Running and we have budget left;
		// otherwise declare it unreachable and show its honest status.
		next := msg.attempt + 1
		sess := m.sessionByID(msg.id)
		if sess.ID() != "" && sess.State.Status == session.StatusRunning && next < liveSSEMaxRetries {
			return m, liveSSEReconnectTick(msg.id, next, liveSSEReconnectDelay(next))
		}
		m.degradeUnreachable(msg.id)
		return m, nil

	case RunnerEventMsg:
		mdl, cmd := m.handleRunnerEvent(msg)
		var cmds []tea.Cmd
		cmds = append(cmds, cmd, m.maybeStartAnim())
		// Background attention notification: if a session other than the
		// attached one needs attention, surface a toast.
		cmds = append(cmds, m.notifyIfBackgroundAttention(m.attachedID))
		return mdl, tea.Batch(cmds...)

	case toastMsg:
		m.toast = &notification{
			sessionID: msg.id,
			title:     msg.title,
			note:      msg.note,
			status:    msg.status,
			createdAt: time.Now(),
		}
		// Stage 2: alongside the in-TUI toast, queue a real OS notification so a
		// background session needing attention escapes the terminal. Rides the
		// next frame via App.View (the one sanctioned non-Stage-3 output path).
		// Gated on Ghostty and the global off switch (NO_COLOR /
		// SANDBOX_REDUCE_MOTION). The toast dedup upstream makes this one-shot per
		// session attention state.
		if m.caps.IsGhostty && !m.caps.ReduceMotion {
			if osc := terminal.OSCNotify(msg.title, msg.note); osc != "" {
				m.pendingOSC = osc
			}
		}
		// Only start a tick loop if one isn't already running, else a burst of
		// toasts spawns multiple concurrent loops (faster-than-spec animation +
		// wasted timers). The running loop picks up the new toast on its next tick.
		if m.toastTickActive {
			return m, nil
		}
		m.toastTickActive = true
		return m, toastTickCmd()

	case actionResultMsg:
		if msg.err != nil {
			m.actionErr = fmt.Errorf("%s: %w", msg.action, msg.err)
		} else {
			m.actionErr = nil
		}
		for i := range m.sessions {
			if m.sessions[i].ID() == msg.id {
				m.sessions[i].PendingAction = ""
				break
			}
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// View renders the dashboard to a string for the Bubble Tea renderer.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

// Cancel stops the cluster-watch goroutine and all live SSE streams. Call
// this before discarding the model (e.g. on program exit or screen switch).
func (m *Model) Cancel() {
	if m.watchCancel != nil {
		m.watchCancel()
	}
	for id, cancel := range m.liveSSECancels {
		cancel()
		delete(m.liveSSECancels, id)
		delete(m.liveSSEChannels, id)
		delete(m.liveSSEClients, id)
	}
}

// --------------------------------------------------------------------------
// Commands
// --------------------------------------------------------------------------

func (m *Model) seedCmd() tea.Cmd {
	backend := m.backend
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		states, err := backend.List(ctx)
		if err != nil {
			return nil // non-fatal: dashboard starts empty
		}
		return seedMsg(states)
	}
}

func (m *Model) startWatchCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		ch, err := m.backend.Watch(ctx)
		if err != nil {
			cancel()
			return nil
		}
		// The cancel is delivered via watchReadyMsg and stored in
		// handleWatchReady (on the Update goroutine) to avoid racing on
		// m.watchCancel from this background Cmd.
		return watchReadyMsg{ch: ch, cancel: cancel}
	}
}

// watchReadyMsg is returned by startWatchCmd with the open channel.
type watchReadyMsg struct {
	ch     <-chan k8s.StateEvent
	cancel context.CancelFunc
}

// watchNextCmd blocks on one read from the watch channel and returns a
// PodEventMsg, then the Update loop re-issues itself for the next event.
func watchNextCmd(ch <-chan k8s.StateEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil // channel closed — watch goroutine done
		}
		return PodEventMsg{Event: ev}
	}
}

// animCmd schedules the next motion tick.
func (m *Model) animCmd() tea.Cmd {
	return tea.Tick(animFPS, func(time.Time) tea.Msg { return animTickMsg{} })
}

// rowMotionActive reports whether any row changed status recently enough that
// its glyph fade-in or status-flash is still in flight. The window is the longer
// of the two (statusFlashDur ≥ theme.FadeDuration) so the loop covers both.
func (m *Model) rowMotionActive() bool {
	for i := range m.sessions {
		if t := m.sessions[i].statusChangedAt; !t.IsZero() && time.Since(t) < statusFlashDur {
			return true
		}
	}
	return false
}

// anyMotionActive reports whether any motion is in flight (§C.2): a running
// busy spinner (tracked through the engine) or a row mid-fade/flash. When it is
// false the single tick loop stops scheduling itself.
func (m *Model) anyMotionActive() bool {
	m.engine.SetSpinners(m.countStatus(StatusBusy))
	return m.engine.AnyMotionActive(time.Now()) || m.rowMotionActive()
}

// maybeStartAnim starts the single gated motion tick loop if motion is active
// and the loop is not already running. Returns nil otherwise, so the dashboard
// schedules no timer while nothing is moving.
func (m *Model) maybeStartAnim() tea.Cmd {
	if !m.animating && m.anyMotionActive() {
		m.animating = true
		return m.animCmd()
	}
	return nil
}

// startLiveSSECmd opens an SSE stream for the given running session in a
// background Cmd. On success it returns liveSSEReadyMsg; on failure (runner
// unreachable) it degrades gracefully — the session keeps its cluster-derived
// status and the dashboard remains responsive.
//
// Each session is bounded to one concurrent SSE forward: we store the cancel
// function in liveSSECancels and cancel any prior stream before opening a new one.
func (m *Model) startLiveSSECmd(sess Session) tea.Cmd {
	connector := m.connector
	if connector == nil || sess.ID() == "" {
		return nil
	}
	// Cancel any existing stream for this session.
	m.cancelLiveSSE(sess.ID())

	id := sess.ID()
	ref := session.Ref{ID: id}
	projectPath := sess.State.ProjectPath
	// Resume from the last event we persisted for this session instead of 0, so
	// the runner replays only genuinely-new events rather than the full history
	// (the source of the launch-time notification flashing and usage count-up).
	afterSeq := sess.lastSeq

	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		// Connect (includes resume-if-suspended + port-forward + health).
		res, err := connector(ctx, ref, projectPath, func(ConnectStage) {})
		if err != nil {
			cancel()
			// Graceful degradation: stream could not be opened; no crash.
			return nil
		}
		ch, err := res.Client.EventsPassive(ctx, ref, afterSeq)
		if err != nil {
			cancel()
			return nil
		}
		// Deliver the ready message; the Update loop stores the cancel and
		// starts reading events.
		return liveSSEReadyMsg{id: id, ch: ch, cancel: cancel, client: res.Client}
	}
}

// reconnectLiveSSECmd is startLiveSSECmd's retry sibling: on success it delivers
// the same liveSSEReadyMsg (so the stream is stored and read like any other), but
// on failure it returns a liveSSEReconnectFailedMsg carrying the attempt number,
// so the Update loop can back off and retry — or eventually degrade — instead of
// silently giving up (which would leave a healthy-but-blipped session stuck busy).
func (m *Model) reconnectLiveSSECmd(sess Session, attempt int) tea.Cmd {
	connector := m.connector
	if connector == nil || sess.ID() == "" {
		return nil
	}
	m.cancelLiveSSE(sess.ID())

	id := sess.ID()
	ref := session.Ref{ID: id}
	projectPath := sess.State.ProjectPath
	// Resume from the last persisted event (see startLiveSSECmd): a reconnect
	// after a port-forward blip must not replay the whole stream either.
	afterSeq := sess.lastSeq

	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		res, err := connector(ctx, ref, projectPath, func(ConnectStage) {})
		if err != nil {
			cancel()
			return liveSSEReconnectFailedMsg{id: id, attempt: attempt}
		}
		ch, err := res.Client.EventsPassive(ctx, ref, afterSeq)
		if err != nil {
			cancel()
			return liveSSEReconnectFailedMsg{id: id, attempt: attempt}
		}
		return liveSSEReadyMsg{id: id, ch: ch, cancel: cancel, client: res.Client}
	}
}

// degradeUnreachable applies the honest end-state for a session whose background
// stream is gone and could not be reconnected: Failed if it was mid-turn (B13:
// "runner unreachable = failed"), otherwise the cluster-derived baseline.
func (m *Model) degradeUnreachable(id session.ID) {
	for i := range m.sessions {
		if m.sessions[i].ID() != id {
			continue
		}
		switch m.sessions[i].DashStatus {
		case StatusBusy, StatusWaiting:
			if m.sessions[i].DashStatus != StatusFailed {
				m.sessions[i].statusChangedAt = time.Now()
			}
			m.sessions[i].DashStatus = StatusFailed
		default:
			m.sessions[i].DashStatus = DeriveStatus(m.sessions[i].State)
		}
		return
	}
}

// cancelLiveSSE stops the SSE stream for the given session if one is running.
func (m *Model) cancelLiveSSE(id session.ID) {
	if cancel, ok := m.liveSSECancels[id]; ok {
		cancel()
		delete(m.liveSSECancels, id)
	}
	delete(m.liveSSEChannels, id)
	delete(m.liveSSEClients, id)
}

// liveSSENextCmd reads one event from the channel and re-issues itself.
// Returns a RunnerEventMsg (or StreamEnded=true on channel close).
func liveSSENextCmd(id session.ID, ch <-chan session.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return RunnerEventMsg{ID: id, StreamEnded: true}
		}
		return RunnerEventMsg{ID: id, Event: ev}
	}
}

// saveSnapshot persists the session's live read-model through the snapshot store
// so a relaunch resumes from here. force (or a status transition) writes
// immediately; usage-only updates coalesce to snapshotSaveInterval to avoid
// thrashing the index on the high-frequency usage stream. No-op without a store.
func (m *Model) saveSnapshot(s *Session, force bool) {
	if m.snapStore == nil {
		return
	}
	if !force {
		if !s.lastSnapSave.IsZero() && time.Since(s.lastSnapSave) < snapshotSaveInterval {
			return
		}
	}
	s.lastSnapSave = time.Now()
	m.snapStore.SaveSnapshot(s.ID(), SessionSnapshot{
		LastSeq:               s.lastSeq,
		DashStatus:            s.DashStatus,
		PendingPermissionID:   s.PendingPermissionID,
		PendingPermissionTool: s.PendingPermissionTool,
		Model:                 s.Model,
		InputTokens:           s.InputTokens,
		OutputTokens:          s.OutputTokens,
		CacheReadTokens:       s.CacheReadTokens,
		CacheWriteTokens:      s.CacheWriteTokens,
		TotalCostUSD:          s.TotalCostUSD,
	})
}

// handleRunnerEvent applies a single SSE event to the relevant session in the
// read-model. If the stream ended, it degrades back to the cluster-derived
// status (idle/suspended/failed) without crashing.
func (m *Model) handleRunnerEvent(msg RunnerEventMsg) (tea.Model, tea.Cmd) {
	if msg.StreamEnded {
		// warm→cold: a closed stream means the pod is no longer feeding us. Drop
		// the warm model unless the cluster still believes the pod is running (a
		// transient port-forward blip that the reconnect path below will retry).
		if s := m.sessionByID(msg.ID); s.State.Status != session.StatusRunning {
			m.dropRetained(msg.ID)
		}
		// Stream closed; clean up.
		m.cancelLiveSSE(msg.ID)
		delete(m.liveSSEChannels, msg.ID)
		var retryCmd tea.Cmd
		for i, s := range m.sessions {
			if s.ID() == msg.ID {
				switch m.sessions[i].DashStatus {
				case StatusBusy, StatusWaiting:
					// A mid-turn drop is either a genuinely dead runner (B13:
					// "runner unreachable = failed") or just a transient
					// port-forward blip on a healthy pod (common with client-go
					// SPDY forwards). Don't flip straight to a scary 'failed'
					// glyph: while the cluster still believes the pod is Running,
					// retry the background stream with backoff (preserving the
					// busy/waiting glyph) and only degrade to Failed once the
					// reconnects are exhausted (RV1). Background streams had no
					// reconnect path at all before this.
					if m.sessions[i].State.Status == session.StatusRunning {
						retryCmd = liveSSEReconnectTick(msg.ID, 0, liveSSEReconnectDelay(0))
					} else {
						// Cluster says not-running (suspended/gone) — honest now.
						m.sessions[i].DashStatus = DeriveStatus(m.sessions[i].State)
					}
				default:
					// Turn was already complete; fall back to cluster-derived state.
					m.sessions[i].DashStatus = DeriveStatus(m.sessions[i].State)
				}
				m.sessions[i].PendingPermissionID = ""
				m.sessions[i].PendingPermissionTool = ""
				// Persist the final state so a relaunch reflects it (and resumes
				// from the last seq we saw) rather than replaying from zero.
				m.saveSnapshot(&m.sessions[i], true)
				break
			}
		}
		return m, retryCmd
	}

	// Patch the session's status from this event.
	for i, s := range m.sessions {
		if s.ID() == msg.ID {
			changed := ApplyRunnerEvent(&m.sessions[i], msg.Event)
			// Advance the resume cursor and persist the snapshot so a relaunch
			// resumes from here instead of replaying history.
			if msg.Event.Seq > m.sessions[i].lastSeq {
				m.sessions[i].lastSeq = msg.Event.Seq
			}
			// Keep the foreground session fully "seen" so it never accumulates an
			// unread badge for output the user is actively watching.
			if msg.ID == m.attachedID {
				m.sessions[i].seenSeq = m.sessions[i].lastSeq
			}
			// Feed the warm model so the retained chat stays live in the
			// background (dedup is handled by the transcript's lastSeq guard).
			if tr, ok := m.retained[msg.ID]; ok {
				tr.ingest(msg.Event)
			}
			m.saveSnapshot(&m.sessions[i], changed)
			// Persist a runner-generated auto title so it survives a re-seed (the
			// cluster state carries no local label). RenamedTitle still wins at
			// display time, so this is safe even for a renamed session.
			if msg.Event.Type == session.EventSessionTitle &&
				m.titleStore != nil && m.sessions[i].AutoTitle != "" {
				m.titleStore.SaveAutoTitle(msg.ID, m.sessions[i].AutoTitle)
			}
			// Persist the Claude SDK session id (session.started) so the CLI can
			// make the session resumable from the laptop on shutdown.
			if msg.Event.Type == session.EventSessionStarted &&
				m.titleStore != nil && m.sessions[i].ClaudeSessionID != "" {
				m.titleStore.SaveClaudeSessionID(msg.ID, m.sessions[i].ClaudeSessionID)
			}
			if changed {
				m.sortSessions()
				m.clampCursor()
			}
			break
		}
	}

	// Re-issue the Cmd to read the next event from the stored channel.
	ch, ok := m.liveSSEChannels[msg.ID]
	if !ok {
		return m, nil // channel was cancelled; stop the loop
	}
	return m, liveSSENextCmd(msg.ID, ch)
}

// approveCmd fires a ResolvePermission call for the selected session.
// It is non-blocking (runs in a Cmd); errors are ignored (fire-and-forget).
func (m *Model) approveCmd(sess Session, allow bool) tea.Cmd {
	if sess.PendingPermissionID == "" {
		return nil
	}
	id := sess.ID()
	ref := session.Ref{ID: id}
	decision := session.PermissionDecision{
		Session:    id,
		Permission: sess.PendingPermissionID,
		Allow:      allow,
		Scope:      "once",
	}

	// Prefer the live SSE client: its port-forward is already open, so
	// approve/deny doesn't pay for a fresh connect + health check.
	if client, ok := m.liveSSEClients[id]; ok {
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			err := client.ResolvePermission(ctx, ref, decision)
			return approveResultMsg{id: id, err: err}
		}
	}

	connector := m.connector
	if connector == nil {
		return nil
	}
	projectPath := sess.State.ProjectPath
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		res, err := connector(ctx, ref, projectPath, func(ConnectStage) {})
		if err != nil {
			return approveResultMsg{id: id, err: err}
		}
		err = res.Client.ResolvePermission(ctx, ref, decision)
		return approveResultMsg{id: id, err: err}
	}
}

// --------------------------------------------------------------------------
// State mutations
// --------------------------------------------------------------------------

func (m *Model) applySeed(states []session.State) (*Model, []tea.Cmd) {
	// Build a lookup of already-known sessions so we can preserve any
	// runner-derived status and avoid cancelling live SSE streams (B10:
	// seed/watch race). Seeds are concurrent with the watch; PodEventMsgs
	// may have already arrived and updated DashStatus/SSE before seedMsg.
	existingByID := make(map[session.ID]Session, len(m.sessions))
	for _, s := range m.sessions {
		existingByID[s.ID()] = s
	}

	m.sessions = make([]Session, 0, len(states))
	for _, st := range states {
		if st.Status == session.StatusGone {
			continue
		}
		s := SessionFromState(st)
		// Restore a persisted rename (T5): the seed comes from the cluster, which
		// doesn't carry the user's local label, so read it back from the store.
		if s.RenamedTitle == "" && m.titleStore != nil {
			s.RenamedTitle = m.titleStore.LoadTitle(s.ID())
		}
		// Restore the persisted runner-generated auto title (T6), same reasoning.
		if s.AutoTitle == "" && m.titleStore != nil {
			s.AutoTitle = m.titleStore.LoadAutoTitle(s.ID())
		}
		if prev, ok := existingByID[s.ID()]; ok {
			// Carry a rename forward across re-seeds even without a store (tests).
			if s.RenamedTitle == "" {
				s.RenamedTitle = prev.RenamedTitle
			}
			// Carry the auto title forward across re-seeds, too.
			if s.AutoTitle == "" {
				s.AutoTitle = prev.AutoTitle
			}
			// When the pod is suspended or failed, the cluster status is
			// authoritative: a stale runner-derived "busy/waiting" (and any
			// pending permission) can never be resolved here, so do not carry it
			// forward — otherwise a re-seed of a now-suspended session shows a
			// phantom "waiting" permission badge (C12). This mirrors applyPodEvent.
			clusterStatus := DeriveStatus(st)
			if clusterStatus == StatusSuspended || clusterStatus == StatusFailed {
				s.DashStatus = clusterStatus
				// s is fresh from SessionFromState, so PendingPermission* are
				// already empty here.
			} else {
				// Pod still running: preserve runner-derived fields so a late
				// seedMsg does not downgrade a session the watch already updated
				// to busy/waiting (B10).
				s.DashStatus = prev.DashStatus
				s.statusChangedAt = prev.statusChangedAt
				s.PendingPermissionID = prev.PendingPermissionID
				s.PendingPermissionTool = prev.PendingPermissionTool
			}
			// Carry the SSE resume cursor + save throttle across re-seeds so a
			// later reconnect resumes from head, not 0 (which would replay).
			s.lastSeq = prev.lastSeq
			s.lastSnapSave = prev.lastSnapSave
		} else if m.snapStore != nil {
			// First time we've seen this session this launch: hydrate the cached
			// snapshot so the row shows its real status/usage immediately and the
			// SSE stream resumes from the cached seq instead of replaying history.
			if snap, ok := m.snapStore.LoadSnapshot(s.ID()); ok {
				s.lastSeq = snap.LastSeq
				// Only trust the cached running-status while the cluster agrees the
				// pod is up; a suspended/failed pod's status is authoritative and a
				// stale "busy/waiting" can never resolve (mirrors the prev branch +
				// C12). We still keep lastSeq so any later stream resumes cleanly.
				if cs := DeriveStatus(st); cs != StatusSuspended && cs != StatusFailed {
					s.applySnapshot(snap)
				}
			}
		}
		m.sessions = append(m.sessions, s)
	}
	m.sortSessions()
	m.clampCursor()
	m.seeded = true

	// Start live SSE streams for running sessions that don't already have one.
	var cmds []tea.Cmd
	if m.connector != nil {
		for i := range m.sessions {
			if m.sessions[i].State.Status == session.StatusRunning {
				id := m.sessions[i].ID()
				if _, exists := m.liveSSECancels[id]; !exists {
					cmds = append(cmds, m.startLiveSSECmd(m.sessions[i]))
				}
			}
		}
	}
	return m, cmds
}

// mergeClusterState overlays the cluster-watch–derived fields onto an existing
// rich session state. The watch's sandboxToState only carries lifecycle Status +
// identity (project/model/backend/pod/last-activity are left zero), so a full
// replace would blank the descriptive fields the seed List populated. The watch
// is authoritative only for Status; everything else is preserved unless the
// existing value is empty.
func mergeClusterState(existing, incoming session.State) session.State {
	merged := existing
	merged.Status = incoming.Status
	if merged.SandboxName == "" {
		merged.SandboxName = incoming.SandboxName
	}
	if merged.CreatedAt.IsZero() {
		merged.CreatedAt = incoming.CreatedAt
	}
	return merged
}

// applyPodEvent patches the read-model for one cluster-watch event and returns
// any Cmd needed to start/stop a live SSE stream for the affected session.
func (m *Model) applyPodEvent(ev k8s.StateEvent) tea.Cmd {
	id := ev.State.ID
	if ev.Deleted || ev.State.Status == session.StatusGone {
		// Remove from the list and cancel its SSE stream.
		for i, s := range m.sessions {
			if s.ID() == id {
				m.sessions = append(m.sessions[:i], m.sessions[i+1:]...)
				break
			}
		}
		m.cancelLiveSSE(id)
		m.dropRetained(id)
		m.clampCursor()
		return nil
	}
	// Patch or insert.
	for i, s := range m.sessions {
		if s.ID() == id {
			// Preserve runner-derived status fields (PendingPermissionID, etc.)
			// and the descriptive fields the seed List populated — the watch
			// only carries Status + identity, so merge rather than replace.
			merged := mergeClusterState(m.sessions[i].State, ev.State)
			clusterStatus := DeriveStatus(merged)
			m.sessions[i].State = merged
			// Only reset to cluster-derived status if it's more restrictive
			// (suspended / failed) — don't overwrite busy/waiting/needs-input
			// with idle just because the pod is still "running".
			if clusterStatus == StatusSuspended || clusterStatus == StatusFailed {
				if m.sessions[i].DashStatus != clusterStatus {
					m.sessions[i].statusChangedAt = time.Now()
				}
				m.sessions[i].DashStatus = clusterStatus
				m.sessions[i].PendingPermissionID = ""
				m.sessions[i].PendingPermissionTool = ""
				m.cancelLiveSSE(id)
				m.dropRetained(id)
			} else if ev.State.Status == session.StatusRunning && m.sessions[i].DashStatus == StatusSuspended {
				// Pod just resumed: reset to idle and start SSE.
				m.sessions[i].DashStatus = StatusIdle
				m.sessions[i].statusChangedAt = time.Now()
			}
			m.sessions[i].Title = deriveTitle(merged)
			m.sortSessions()
			m.clampCursor()
			// Start SSE if the session is now running and we don't have one.
			if ev.State.Status == session.StatusRunning && m.connector != nil {
				if _, exists := m.liveSSECancels[id]; !exists {
					return m.startLiveSSECmd(m.sessions[i])
				}
			}
			return nil
		}
	}
	// New session appeared — fade its glyph in.
	sess := SessionFromState(ev.State)
	sess.statusChangedAt = time.Now()
	m.sessions = append(m.sessions, sess)
	m.sortSessions()
	m.clampCursor()
	if ev.State.Status == session.StatusRunning && m.connector != nil {
		return m.startLiveSSECmd(sess)
	}
	return nil
}

func (m *Model) sortSessions() {
	SortSessions(m.sessions, m.sortKey, m.sortDir)
}

func (m *Model) clampCursor() {
	visible := m.visibleSessions()
	if m.cursor >= len(visible) {
		m.cursor = max(0, len(visible)-1)
	}
}

// visibleSessions returns the filtered+sorted subset of sessions to display.
func (m *Model) visibleSessions() []Session {
	q := m.filter
	if m.filtering {
		q = m.filterBuf
	}
	return sortByAttention(FilterSessions(m.sessions, q), m.attentionFirst)
}

// selectedSession returns the currently highlighted session, or nil.
func (m *Model) selectedSession() *Session {
	visible := m.visibleSessions()
	if len(visible) == 0 || m.cursor >= len(visible) {
		return nil
	}
	s := visible[m.cursor]
	return &s
}

// sessionByID returns the Session with the given ID from the dashboard's session
// list, or a zero Session when not found. Used by App to restart background SSE
// after detach (B2).
func (m *Model) sessionByID(id session.ID) Session {
	for _, s := range m.sessions {
		if s.ID() == id {
			return s
		}
	}
	return Session{}
}

// --------------------------------------------------------------------------
// Key handling
// --------------------------------------------------------------------------

func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	ks := msg.String()

	// A destructive-action confirmation captures all keys until resolved.
	if m.confirm != nil {
		switch ks {
		case "y", "Y":
			action := m.confirm.action
			for i := range m.sessions {
				if m.sessions[i].ID() == m.confirm.id {
					m.sessions[i].PendingAction = "destroy"
					break
				}
			}
			m.confirm = nil
			return m, action
		case "n", "N", "esc":
			for i := range m.sessions {
				if m.sessions[i].ID() == m.confirm.id {
					m.sessions[i].PendingAction = ""
					break
				}
			}
			m.confirm = nil
		}
		return m, nil
	}

	// While the help overlay is open, ↑/↓ + space drive the grouped surface;
	// any other key closes it.
	if m.showHelp {
		if m.helpUI.handleKey(ks) {
			return m, nil
		}
		m.showHelp = false
		return m, nil
	}

	// Switcher overlay takes all keys while open.
	if m.switcher.open {
		cmd, _ := m.switcherKey(msg)
		return m, cmd
	}

	// Permission queue overlay takes all keys while open.
	if m.permQueue.open {
		cmd, _ := m.permQueueKey(msg)
		return m, cmd
	}

	// Filtering mode intercepts most keys for the filter buffer.
	if m.filtering {
		return m.handleFilterKey(ks)
	}

	// Rename mode intercepts text input for the rename buffer.
	if m.renaming {
		return m.handleRenameKey(ks)
	}

	// q opens the pending-permission queue when sessions are waiting.
	if ks == "q" {
		if len(m.permQueueItems()) > 0 {
			m.openPermQueue()
			return m, nil
		}
	}

	// Rename selected session.
	if key.Matches(msg, m.keys.Rename) {
		m.openRename()
		return m, nil
	}

	// Archive selected finished session.
	if key.Matches(msg, m.keys.Archive) {
		m.archiveSelected()
		return m, nil
	}

	// In group view, space expands/collapses the repo group at the cursor.
	if m.groupView.open && ks == "space" {
		m.toggleRepoGroup()
		return m, nil
	}

	// Quit
	if key.Matches(msg, m.keys.Quit) {
		m.Cancel()
		return m, tea.Quit
	}

	// Help overlay (grouped, expandable; sourced from the keymap).
	if key.Matches(msg, m.keys.Help) {
		m.helpUI = newHelpModel("keybindings", keymapCategories(m.keys))
		m.showHelp = true
		return m, nil
	}
	// `g` is overloaded: a lone press toggles group view (handoff: Layout B is a
	// one-keystroke `g` toggle), while a quick `gg` jumps to the top. The first
	// `g` toggles group view and arms the chord; a second `g` reverts that
	// transient toggle and jumps to the top, so `gg` is a clean "go to top".
	if ks == "g" {
		if m.ggPending {
			m.ggPending = false
			m.toggleGroupView() // revert the toggle the first g applied
			m.cursor = 0
		} else {
			m.ggPending = true
			m.toggleGroupView()
		}
		return m, nil
	}
	if ks == "G" {
		m.ggPending = false
		visible := m.visibleSessions()
		if len(visible) > 0 {
			m.cursor = len(visible) - 1
		}
		return m, nil
	}
	// Any key other than g resets the gg pending state.
	if ks != "g" {
		m.ggPending = false
	}

	// Navigation: in group view, up/down move over rows in grouped order.
	if key.Matches(msg, m.keys.Up) {
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	}
	if key.Matches(msg, m.keys.Down) {
		visible := m.visibleRows()
		if m.cursor < len(visible)-1 {
			m.cursor++
		}
		return m, nil
	}

	// Filter start
	if key.Matches(msg, m.keys.Filter) {
		m.filtering = true
		m.filterBuf = ""
		return m, nil
	}

	// Sort: cycle key (disabled in group view).
	if !m.groupView.open && key.Matches(msg, m.keys.SortCycle) {
		m.sortKey = m.sortKey.Next()
		m.sortSessions()
		m.clampCursor()
		return m, nil
	}
	// Sort: flip direction (disabled in group view).
	if !m.groupView.open && key.Matches(msg, m.keys.SortFlip) {
		m.sortDir = m.sortDir.Flip()
		m.sortSessions()
		m.clampCursor()
		return m, nil
	}

	// Attention-first toggle (D4): float Waiting/NeedsInput to the top.
	if !m.groupView.open && key.Matches(msg, m.keys.AttentionToggle) {
		m.attentionFirst = !m.attentionFirst
		m.clampCursor()
		return m, nil
	}

	// Attach — flip to transcript screen via the App.
	if key.Matches(msg, m.keys.Attach) {
		sel := m.selectedRowSession()
		if sel != nil {
			return m, func() tea.Msg { return attachMsg{sess: *sel} }
		}
		return m, nil
	}

	// Approve / Deny — inline permission from the detail pane.
	if key.Matches(msg, m.keys.Approve) {
		sel := m.selectedRowSession()
		if sel != nil && sel.DashStatus == StatusWaiting {
			return m, m.approveCmd(*sel, true)
		}
		return m, nil
	}
	if key.Matches(msg, m.keys.Deny) {
		sel := m.selectedRowSession()
		if sel != nil && sel.DashStatus == StatusWaiting {
			return m, m.approveCmd(*sel, false)
		}
		return m, nil
	}

	// New session — delegated to the App, which owns the Creator.
	if key.Matches(msg, m.keys.New) {
		return m, func() tea.Msg { return createSessionMsg{} }
	}

	// Suspend — scale the selected session's pod to zero (recoverable).
	if key.Matches(msg, m.keys.Suspend) {
		sel := m.selectedRowSession()
		if sel != nil && sel.DashStatus != StatusSuspended {
			m.cancelLiveSSE(sel.ID())
			for i := range m.sessions {
				if m.sessions[i].ID() == sel.ID() {
					m.sessions[i].PendingAction = "suspend"
					break
				}
			}
			return m, m.suspendCmd(session.Ref{ID: sel.ID()})
		}
		return m, nil
	}

	// Resume — scale a suspended session's pod back up.
	if key.Matches(msg, m.keys.Resume) {
		sel := m.selectedRowSession()
		if sel != nil && sel.DashStatus == StatusSuspended {
			for i := range m.sessions {
				if m.sessions[i].ID() == sel.ID() {
					m.sessions[i].PendingAction = "resume"
					break
				}
			}
			return m, m.resumeCmd(session.Ref{ID: sel.ID()})
		}
		return m, nil
	}

	// Destroy — irreversible; gate behind a confirm dialog.
	if key.Matches(msg, m.keys.Destroy) {
		sel := m.selectedRowSession()
		if sel != nil {
			m.confirm = &confirmPrompt{
				message: "Destroy " + sel.DisplayTitle() + "?  This deletes the pod and PVC and cannot be undone.",
				action:  m.destroyCmd(session.Ref{ID: sel.ID()}),
				id:      sel.ID(),
			}
		}
	}
	return m, nil
}

func (m *Model) handleFilterKey(ks string) (tea.Model, tea.Cmd) {
	switch ks {
	case "esc":
		// Clear filter and exit filtering mode.
		m.filtering = false
		m.filterBuf = ""
		m.filter = ""
		m.clampCursor()
		return m, nil

	case "enter":
		// Commit the filter and drop back to list navigation.
		m.filter = m.filterBuf
		m.filtering = false
		m.clampCursor()
		return m, nil

	case "backspace", "delete":
		if r, size := utf8.DecodeLastRuneInString(m.filterBuf); r != utf8.RuneError {
			m.filterBuf = m.filterBuf[:len(m.filterBuf)-size]
		}

	default:
		// Accept printable characters; j/k also update cursor while filtering.
		if ks == "j" || ks == "down" {
			visible := m.visibleSessions()
			if m.cursor < len(visible)-1 {
				m.cursor++
			}
			return m, nil
		}
		if ks == "k" || ks == "up" {
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		}
		if len(ks) == 1 && ks[0] >= 32 && ks[0] < 127 {
			m.filterBuf += ks
		}
	}

	// Reset cursor to top when filter changes.
	m.cursor = 0
	m.clampCursor()
	return m, nil
}

// handleRenameKey routes keys to the rename buffer while the rename overlay is
// open: enter commits, esc cancels, backspace deletes, printable runes append.
func (m *Model) handleRenameKey(ks string) (tea.Model, tea.Cmd) {
	switch ks {
	case "esc":
		m.renaming = false
		m.renameBuf = ""
	case "enter":
		m.commitRename()
	case "backspace", "delete":
		if r, size := utf8.DecodeLastRuneInString(m.renameBuf); r != utf8.RuneError {
			m.renameBuf = m.renameBuf[:len(m.renameBuf)-size]
		}
	default:
		// Accept a single printable ASCII character (matches filter input).
		if len(ks) == 1 && ks[0] >= 32 && ks[0] < 127 {
			m.renameBuf += ks
		}
	}
	return m, nil
}

func (m *Model) render() string {
	if m.width == 0 {
		return "loading…\n"
	}

	if m.showHelp {
		overlay := m.renderHelp()
		return lipgloss.Place(m.width, m.height,
			lipgloss.Center, lipgloss.Center,
			overlay,
			lipgloss.WithWhitespaceChars(" "),
		)
	}

	if m.confirm != nil {
		overlay := m.renderConfirm()
		return lipgloss.Place(m.width, m.height,
			lipgloss.Center, lipgloss.Center,
			overlay,
			lipgloss.WithWhitespaceChars(" "),
		)
	}

	zoned := m.renderZoned(m.width, m.height)
	if !m.switcher.open && !m.permQueue.open && !m.renaming {
		return zoned
	}

	canvas := lipgloss.NewCanvas(m.width, m.height)
	canvas.Compose(lipgloss.NewCompositor(
		lipgloss.NewLayer(zoned).X(0).Y(0).Z(0),
	))

	if m.switcher.open {
		sw := m.renderSwitcher(m.width)
		swW := lipgloss.Width(firstLineOf(sw))
		swH := strings.Count(sw, "\n") + 1
		if swW > m.width {
			swW = m.width
		}
		if swH > m.height {
			swH = m.height
		}
		sx := (m.width - swW) / 2
		sy := (m.height - swH) / 2
		shadow := solidBlock(swW, swH, theme.Shadow)
		canvas.Compose(lipgloss.NewCompositor(
			lipgloss.NewLayer(shadow).X(sx+2).Y(sy+1).Z(8),
			lipgloss.NewLayer(sw).X(sx).Y(sy).Z(9),
		))
	}

	if m.permQueue.open {
		pq := m.renderPermQueue(m.width)
		pqW := lipgloss.Width(firstLineOf(pq))
		pqH := strings.Count(pq, "\n") + 1
		if pqW > m.width {
			pqW = m.width
		}
		if pqH > m.height {
			pqH = m.height
		}
		px := (m.width - pqW) / 2
		py := (m.height - pqH) / 2
		shadow := solidBlock(pqW, pqH, theme.Shadow)
		canvas.Compose(lipgloss.NewCompositor(
			lipgloss.NewLayer(shadow).X(px+2).Y(py+1).Z(8),
			lipgloss.NewLayer(pq).X(px).Y(py).Z(9),
		))
	}

	if m.renaming {
		overlay := m.renderRenameOverlay(m.width)
		canvas.Compose(lipgloss.NewCompositor(
			lipgloss.NewLayer(solidBlock(m.width, 3, theme.Shadow)).X(4).Y(m.height/3+1).Z(9),
			lipgloss.NewLayer(overlay).X(2).Y(m.height/3).Z(10),
		))
	}

	return canvas.Render()
}

// renderRowLines produces one string per row for the session list, including
// group headers when group view is enabled.
// renderRowLines lays the display rows into at most `height` physical lines.
// Session rows are two physical lines each; group headers are one. It returns the
// rendered lines plus the count of display rows fully shown (so the caller can
// summarize the overflow). Group headers and skeletons count as one line.
func (m *Model) renderRowLines(rows []groupedSession, width, height int) ([]string, int) {
	if len(rows) == 0 {
		// U2: three-way state branch so "loading", "empty cluster", and
		// "filter matched nothing" are visually distinct (spec 04-ux-responsiveness §U2).
		activeFilter := m.filter
		if m.filtering {
			activeFilter = m.filterBuf
		}
		switch {
		case !m.seeded:
			// Still loading the seed: show skeleton bars at list rhythm.
			n := height
			if n > 8 {
				n = 8
			}
			if n < 1 {
				n = 1
			}
			return strings.Split(skeletonRows(n, width), "\n"), 0
		case activeFilter != "":
			// Filter active but matched nothing.
			return []string{noMatchCopy(activeFilter)}, 0
		case len(m.sessions) == 0:
			// Seeded, no filter, genuinely empty cluster: first-run CTA.
			return strings.Split(m.firstRunView(width, height), "\n"), 0
		default:
			// Seeded, has sessions, but all filtered out — safety net.
			return []string{styleEmpty.Render("  No matches.")}, 0
		}
	}

	top := m.rowScrollTop(rows, height)
	lines := make([]string, 0, height)
	shown := 0
	for i := top; i < len(rows) && len(lines) < height; i++ {
		var rl []string
		if rows[i].session != nil {
			rl = strings.Split(m.renderSessionRow(*rows[i].session, i == m.cursor, width), "\n")
		} else {
			rl = []string{m.renderGroupHeader(rows[i].repo, width)}
		}
		if len(lines)+len(rl) > height {
			break // a partially-visible row counts as hidden (summarized below)
		}
		lines = append(lines, rl...)
		shown = i + 1
	}
	return lines, shown
}

// rowHeight is the physical line count for a display row: two lines per session
// (primary + dim sub-line), one for a group header.
func rowHeight(r groupedSession) int {
	if r.session != nil {
		return 2
	}
	return 1
}

// rowScrollTop returns the first visible display-row index so the cursor row is
// fully visible within `height` physical lines, anchoring the cursor toward the
// bottom when scrolling down (matching the old single-line viewport behavior).
func (m *Model) rowScrollTop(rows []groupedSession, height int) int {
	cur := m.cursor
	if cur < 0 {
		cur = 0
	}
	if cur >= len(rows) {
		cur = len(rows) - 1
	}
	top := cur
	used := rowHeight(rows[cur])
	for top > 0 {
		ph := rowHeight(rows[top-1])
		if used+ph > height {
			break
		}
		used += ph
		top--
	}
	return top
}

// renderSessionRow renders one session as two physical lines joined by "\n" (the
// row spec in docs/dashboard-redesign.md):
//
//	line 1: selection-bar(2) attention-dot(2) status-glyph(2) title(flex) right-aligned relTime
//	line 2 (dim, indented): "<model>·<client>  <short-id>  <ctx%>  [⚠ if failed]"
//
// Two lines disambiguate identical titles and fit model/ctx without crowding (P2/P4).
func (m *Model) renderSessionRow(s Session, selected bool, width int) string {
	// Selection bar.
	var bar string
	if selected {
		bar = styleSelectionBar.Render(theme.GlyphSelBar) + " "
	} else {
		bar = "  "
	}

	// Glyph: busy rows use the pre-rendered gradient spinner (P4); pending-action
	// rows also show a spinner so the user sees in-progress feedback (U3).
	var glyphRendered string
	if s.DashStatus == StatusBusy || s.PendingAction != "" {
		glyphRendered = theme.SpinnerFrame(m.spinnerFrame) + " "
	} else {
		gcol := theme.FadeColor(glyphColor(s.DashStatus), s.statusChangedAt)
		glyphRendered = lipgloss.NewStyle().Foreground(gcol).Render(s.DashStatus.Glyph()) + " "
	}

	// relTime: last-active, falling back to created-age so it's never a bare "—" (P3).
	relTime := styleRelTime.Render(rowRelTime(s))

	// Attention dot (D4): a colored ● for waiting/needs-input/failed rows; two
	// spaces otherwise, so the fixed-width layout never shifts.
	dot := attentionDot(s)
	var dotSlot string
	if dot != "" {
		dotSlot = dot + " "
	} else {
		dotSlot = "  "
	}

	// Line 1 title fills the space between the glyph and the right-aligned relTime.
	fixedW := 2 + 2 + 2 + 1 + lipgloss.Width(relTime)
	titleW := width - fixedW
	if titleW < 4 {
		titleW = 4
	}
	title := padRight(truncate(s.DisplayTitle(), titleW), titleW)

	var rowStyle lipgloss.Style
	if selected {
		rowStyle = styleRowSelected.Width(width)
	} else {
		rowStyle = styleRow.Width(width)
		// Status-flash: briefly pulse the row background toward the new status'
		// accent right after a status change (§C.3), drawing the eye to it.
		if bg, ok := flashBg(theme.Page, glyphColor(s.DashStatus), s.statusChangedAt); ok {
			rowStyle = rowStyle.Background(bg)
		}
		// Row-enter: a freshly-created row fades its title text in (§C.3). A
		// foreground blend (not a slide) so the fixed-width layout never shifts.
		if e := rowEnter(s.State.CreatedAt); e < 1 {
			rowStyle = rowStyle.Foreground(anim.LerpColor(theme.TextDim, theme.TextBody, e))
		}
	}
	line1 := rowStyle.Render(bar + dotSlot + glyphRendered + title + " " + relTime)

	// Line 2: dim sub-line indented under the title (bar+dot+glyph = 6 cols).
	sub := s.AgentLabel() + "  " + s.ShortID()
	if pct := s.CtxPercent(); pct > 0 {
		sub += fmt.Sprintf("  %d%%", pct)
	}
	if u := s.Unread(); u > 0 {
		sub += fmt.Sprintf("  ●%d", u)
	}
	if s.DashStatus == StatusFailed {
		sub += "  ⚠"
	}
	var subStyle lipgloss.Style
	if selected {
		subStyle = lipgloss.NewStyle().Foreground(theme.TextMuted).Background(theme.Raised).Width(width)
	} else {
		subStyle = lipgloss.NewStyle().Foreground(theme.TextMuted).Width(width)
	}
	line2 := subStyle.Render("      " + truncate(sub, max(4, width-6)))

	return line1 + "\n" + line2
}

// rowRelTime is the row's relative-time string: last-active, or created-age when
// the session has never reported activity, so the column is never a bare "—" (P3).
func rowRelTime(s Session) string {
	t := s.State.LastActivity
	if t.IsZero() {
		t = s.State.CreatedAt
	}
	return relativeTime(t)
}

// renderDetailLines produces lines for the right-hand detail pane.
func (m *Model) renderDetailLines(width, height int) []string {
	sel := m.selectedSession()
	if sel == nil {
		empty := styleEmpty.Render("  Select a session")
		return []string{empty}
	}
	s := *sel

	lines := []string{
		styleDetailTitle.Width(width).Render(s.DisplayTitle()),
		"",
	}

	// model line carries ctx% when known: "model   opus-4.8   ctx 62%" (Phase 3).
	modelVal := s.Model
	if modelVal != "" {
		if pct := s.CtxPercent(); pct > 0 {
			modelVal += fmt.Sprintf("   ctx %d%%", pct)
		}
	}
	kvPairs := []struct{ k, v string }{
		{"status", glyphStyle(s.DashStatus).Render(s.DashStatus.Glyph() + " " + s.DashStatus.String())},
		{"agent", ClientLabel(s.State.Backend)},
		{"model", modelVal},
		{"project", s.State.ProjectPath},
		{"session", string(s.ID())},
		{"pod", s.State.PodName},
		{"created", relativeTime(s.State.CreatedAt)},
		{"active", rowRelTime(s)},
	}
	// Cost line once usage has been reported (Phase 3).
	if s.TotalCostUSD > 0 {
		kvPairs = append(kvPairs, struct{ k, v string }{"cost", fmt.Sprintf("$%.2f", s.TotalCostUSD)})
	}
	// When a suspend/resume/destroy is in flight, append a pending line (U3).
	if s.PendingAction != "" {
		kvPairs = append(kvPairs, struct{ k, v string }{"pending", s.PendingAction + "…"})
	}

	// detailKVWidth is the fixed key column width for aligned key/value rows
	// (kit §KV, design-system §1.3). "created" and "project" are the longest keys.
	const detailKVWidth = 7
	for _, kv := range kvPairs {
		if kv.v == "" {
			continue // skip unknown fields (e.g. model before session.started)
		}
		lines = append(lines, kit.KV(kv.k, kv.v, detailKVWidth))
	}

	// Unread badge: events that arrived since this warm session was last viewed.
	if u := s.Unread(); u > 0 {
		badge := lipgloss.NewStyle().Foreground(theme.Gold).Bold(true).
			Render(fmt.Sprintf("● %d new", u))
		lines = append(lines, "", badge)
	}

	// ─ recent ─ : the last ≈3 main-thread tool calls, newest first (Phase 4).
	if n := len(s.RecentTools); n > 0 {
		lines = append(lines, detailRule("recent", width))
		shown := 0
		for i := n - 1; i >= 0 && shown < 3; i-- {
			t := s.RecentTools[i]
			tool := lipgloss.NewStyle().Foreground(theme.Malibu).Render(t.Tool)
			arg := lipgloss.NewStyle().Foreground(theme.TextSecondary).Render(truncate(t.Arg, max(4, width-lipgloss.Width(t.Tool)-3)))
			lines = append(lines, " "+tool+"  "+arg)
			shown++
		}
	}

	// When the session is waiting for approval, show the inline permission prompt.
	if s.DashStatus == StatusWaiting && s.PendingPermissionTool != "" {
		lines = append(lines, "")

		// Gold-bordered permission box.
		toolLabel := lipgloss.NewStyle().
			Foreground(theme.Gold).
			Bold(true).
			Render(theme.GlyphWaiting + " " + s.PendingPermissionTool)
		lines = append(lines, toolLabel)

		// Unified key hint row (kit §Kbd, design-system §1.3 priority 1).
		lines = append(lines, "  "+kit.KbdRow([2]string{"a", "approve"}, [2]string{"d", "deny"}, [2]string{"↵", "view diff"}))
	}

	// ─ needs you ─ : action hints when the session is actionable — waiting,
	// needs-input, or failed (P13: items become actionable from the dashboard).
	switch s.DashStatus {
	case StatusWaiting, StatusNeedsInput, StatusFailed:
		lines = append(lines, detailRule("needs you", width))
		var note string
		switch s.DashStatus {
		case StatusWaiting:
			note = theme.GlyphWaiting + " waiting for your approval"
		case StatusNeedsInput:
			note = theme.GlyphNeedsInput + " waiting for your input"
		case StatusFailed:
			note = theme.GlyphFailed + " session failed"
		}
		lines = append(lines, " "+lipgloss.NewStyle().Foreground(glyphColor(s.DashStatus)).Render(note))
		lines = append(lines, " "+kit.KbdRow([2]string{"↵", "attach"}, [2]string{"r", "rename"}, [2]string{"s", "suspend"}, [2]string{"x", "destroy"}))
	}

	// Show last connector error if any (kit §ErrorBlock, design-system §2.3).
	if m.connectErr != nil {
		lines = append(lines, "")
		for _, l := range strings.Split(kit.ErrorBlock(m.connectErr.Error(), "", ""), "\n") {
			lines = append(lines, l)
		}
	}

	// Show last action error if any (kit §ErrorBlock, design-system §2.3).
	if m.actionErr != nil {
		lines = append(lines, "")
		for _, l := range strings.Split(kit.ErrorBlock(m.actionErr.Error(), "", ""), "\n") {
			lines = append(lines, l)
		}
	}

	// Pad/truncate lines to width.
	out := make([]string, 0, height)
	for _, l := range lines {
		if len(out) >= height {
			break
		}
		out = append(out, padRight(truncate(l, width), width))
	}
	return out
}

// detailRule renders a "─ label ─────" section divider for the detail pane.
func detailRule(label string, width int) string {
	prefix := "─ " + label + " "
	d := width - lipgloss.Width(prefix)
	if d < 0 {
		d = 0
	}
	return styleDivider.Render(prefix + strings.Repeat("─", d))
}

// renderConfirm renders the centered y/n confirmation dialog for destructive
// actions. Calm by design (a single bordered question), not a red scream.
func (m *Model) renderConfirm() string {
	if m.confirm == nil {
		return ""
	}
	msg := lipgloss.NewStyle().Foreground(theme.TextBright).Bold(true).Render(m.confirm.message)
	hint := kit.KbdRow([2]string{"y", "yes"}, [2]string{"n", "no"})
	body := msg + "\n\n" + hint
	// D2: framed by the shared kit panel (content-fit width, coral border, raised
	// fill, 1×3 padding) — same frame as before, now via the design-system kit.
	return kit.Card(kit.CardOpts{
		Content:     body,
		BorderColor: theme.Coral,
		Background:  theme.Raised,
		PadV:        1,
		PadH:        3,
	})
}

// renderHelp renders the `?` overlay as the shared grouped, expandable help
// surface (categories sourced from the keymap; see help.go).
func (m *Model) renderHelp() string {
	return m.helpUI.view(m.width)
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

func (m *Model) countStatus(s SessionStatus) int {
	n := 0
	for _, sess := range m.sessions {
		if sess.DashStatus == s {
			n++
		}
	}
	return n
}

// progressState maps the per-frame session aggregate to a Ghostty tab/taskbar
// progress state (Stage 2): a pending permission (Waiting) shows the error state
// so it surfaces on an unfocused tab; any busy turn shows an indeterminate
// pulse; otherwise the indicator is cleared. Returns ProgressNone when the
// terminal is not Ghostty so the caller can skip emission entirely.
func (m *Model) progressState() terminal.Progress {
	// Honor the global off switch (NO_COLOR / SANDBOX_REDUCE_MOTION, folded into
	// caps.ReduceMotion) so output matches today exactly under it (D2/D4).
	if !m.caps.IsGhostty || m.caps.ReduceMotion {
		return terminal.ProgressNone
	}
	c := m.partition()
	switch {
	case c.waiting > 0:
		return terminal.ProgressError
	case c.busy > 0:
		return terminal.ProgressBusy
	default:
		return terminal.ProgressNone
	}
}

// takePendingOSC returns and clears any queued one-shot OSC string (the desktop
// notification). The root App.View drains it once per frame; clearing here makes
// it fire exactly once.
func (m *Model) takePendingOSC() string {
	s := m.pendingOSC
	m.pendingOSC = ""
	return s
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// truncate shortens s to at most maxW display columns.
func truncate(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	w := lipgloss.Width(s)
	if w <= maxW {
		return s
	}
	// Trim runes until we fit, appending '…'.
	r := []rune(s)
	for len(r) > 0 {
		candidate := string(r) + "…"
		if lipgloss.Width(candidate) <= maxW {
			return candidate
		}
		r = r[:len(r)-1]
	}
	return "…"
}

// padRight pads s with spaces to exactly width display columns.
func padRight(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

// --------------------------------------------------------------------------
// Update continuation for watchReadyMsg
// --------------------------------------------------------------------------

// handleWatchReady handles messages that need access to the watch channel reference.
// The root App routes watchReadyMsg here.
func (m *Model) handleWatchReady(msg watchReadyMsg) (tea.Model, tea.Cmd) {
	// Store the cancel so shutdown stops the informer.
	if m.watchCancel != nil {
		m.watchCancel() // cancel previous watch if any
	}
	m.watchCancel = msg.cancel
	// Start reading events from the watch channel.
	return m, watchNextCmd(msg.ch)
}

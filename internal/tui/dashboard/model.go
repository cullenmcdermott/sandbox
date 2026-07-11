package dashboard

import (
	"context"
	"errors"
	"fmt"
	"time"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/tui/anim"
	"github.com/cullenmcdermott/sandbox/tui/terminal"
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

	// observerConnector is a lightweight connect path for background (passive)
	// status streams: it does port-forward + runner health only, skipping the
	// attach-time file-sync setup so the per-session background streams stay cheap
	// (RV8). When nil, background streams fall back to the full connector.
	observerConnector Connector

	// sessions is the canonical read-model: all live sessions, kept in the
	// current sort order. The watch Cmd patches individual entries here.
	sessions []Session

	// seeded is true once the first seedMsg (or the first PodEventMsg) has been
	// processed. Before seeded, the list shows skeleton bars; after seeded with
	// no sessions, it shows the first-run CTA (U2, spec 04-ux-responsiveness).
	seeded bool

	// seedErr holds the error from a failed initial seed (e.g. the cluster is
	// unreachable). When set with no sessions yet, the list shows an actionable
	// error + retry instead of skeleton bars forever. Cleared on a successful
	// seed/watch event or an explicit retry.
	seedErr error

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

	// notifiedAttention is the set of background sessions already notified for
	// their CURRENT attention episode, so a steady-state waiting session doesn't
	// re-toast (and re-fire its OS notification) every time its 8s toast expires.
	// An entry is cleared when its session leaves attention (edge, not level).
	notifiedAttention map[session.ID]bool

	// attachedID is the session the user is currently attached to (set by the
	// App on attach, cleared on detach). It is excluded from background-attention
	// toasts so the session you're already looking at never toasts itself.
	attachedID session.ID

	// ⌃K fuzzy quick-switcher overlay.
	switcher switcherModel

	// Pending-permission queue view.
	permQueue permQueueModel

	// Group-by-repo and rename state.
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

	// watchCh is the open cluster-watch event channel. Held so the PodEventMsg
	// handler can re-issue watchNextCmd after each event — the self-perpetuating
	// reader idiom (mirrors liveSSEChannels). Without re-issuing, the watch would
	// deliver exactly one event for the model's whole lifetime and then go deaf.
	watchCh <-chan k8s.StateEvent

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

	// liveSSECloses holds each registered stream's transport teardown
	// (ConnectResult.Close — the SPDY forward + its reconnect loop). The stream's
	// cancel func stops only the SSE read; the forward is rooted at
	// context.Background() by design (it must outlive the connect ctx), so
	// WITHOUT this call every eviction/reconnect/suspend leaked a forward that
	// polls the API server every ≤10s forever (§1d C1). Invoked in cancelLiveSSE,
	// the single stream-teardown choke point.
	liveSSECloses map[session.ID]func()

	// liveSSEConnecting marks sessions whose background-connect Cmd is in flight
	// (issued but not yet resolved to ready/failed). Every launch guard checks
	// this IN ADDITION to liveSSECancels: a background connect is
	// connectSem-throttled and takes seconds, but liveSSECancels is only
	// populated on ready — so without an in-flight marker a seed + a watch event
	// (or two watch events) routinely both launch a connect for the same session,
	// and the second ready orphans the first stream. Set synchronously when a
	// connect Cmd is issued (single-threaded Update), cleared when it resolves.
	liveSSEConnecting map[session.ID]bool

	// liveSSEStreamGen holds the generation token of the CURRENTLY-registered
	// background stream per session; liveSSEGenCounter mints them monotonically.
	// Every connect is tagged with a generation at launch, and that generation
	// rides on its liveSSEReadyMsg / RunnerEventMsg / failure messages. A message
	// whose generation no longer matches the registered one comes from a
	// superseded or cancelled stream whose goroutine hadn't yet observed
	// cancellation; it is ignored, so an orphan's StreamEnded can't tear down the
	// healthy stream and its late events can't double-apply (§1a connect-side).
	liveSSEGenCounter uint64
	liveSSEStreamGen  map[session.ID]uint64

	// connectSem bounds how many background observer connects run their expensive
	// setup (cluster Status + port-forward + runner health) concurrently. On
	// launch the dashboard fans out one connect per running session; without a cap
	// a large session set hits the cluster with N simultaneous resume/forward/health
	// round-trips (FU2). Each background connect Cmd acquires a slot for the setup
	// phase only and releases it before the long-lived stream continues, so the cap
	// throttles the burst without limiting the number of open streams.
	connectSem chan struct{}

	// retained holds a live TranscriptModel for each warm (running-pod) session,
	// fed in the background by handleRunnerEvent. A warm session's model is never
	// destroyed while its pod runs, so showing it is an O(1) swap (see warm.go).
	retained map[session.ID]*TranscriptModel

	// maxObserverStreams caps the number of concurrently-established background
	// observer forwards (§1d). Zero uses defaultMaxObserverStreams. Set via
	// WithMaxObserverStreams / RunOptions.MaxObserverStreams. See observerCap.
	maxObserverStreams int

	// observerActiveAt is the LRU recency clock for established observer streams:
	// session → last time it was active (a live event applied) or visible
	// (focused/attached). The coldest entry is the eviction victim once the
	// established set exceeds observerCap. Pruned as streams are cancelled/evicted
	// so it tracks only live observers. See warm.go's observer manager.
	observerActiveAt map[session.ID]time.Time

	// attachGate lets a foreground attach/create connect preempt the background
	// observer connect burst: observer connect goroutines pause on it while a
	// foreground connect is in flight (§5 leftover). Foreground connects never
	// block on it. See attachGate in warm.go.
	attachGate *attachGate

	// reconcileMisses counts how many consecutive periodic cluster re-lists a
	// session has been absent from. The watch informer can miss a delete that
	// happened before its cache synced, leaving a phantom session in the list
	// forever; the reconcile loop drops a session only after it's missed twice
	// (~2 cycles) so a just-created session the snapshot predates — added by the
	// watch — isn't dropped out from under us. See reconcile.
	reconcileMisses map[session.ID]int

	// syncProber reports per-session sync health for the detail-pane indicator.
	// nil disables the indicator (unit-test default).
	syncProber SyncProber

	// syncProbedAt records the last time each warm session's Mutagen sync health
	// was probed. Each probe forks a `mutagen sync list` subprocess, so unfocused
	// warm sessions back off to unfocusedSyncPollInterval instead of forking every
	// poll tick; the focused session(s) still probe at syncPollInterval. Pruned to
	// the warm set each tick so it can't grow unbounded. See selectSyncProbeTargets.
	syncProbedAt map[session.ID]time.Time

	// syncReaper enumerates + terminates this tool's orphaned mutagen syncs. nil
	// disables the periodic sync GC (unit-test / no-backend default).
	syncReaper SyncReaper

	// orphanSince is the GC grace clock: mutagen sync identifier → first time it
	// was observed orphaned (pod endpoint down) AND its session was NOT in
	// gcRunning. An entry is dropped when the sync recovers or its session's pod
	// comes back, so the map only tracks durably-dead candidates and can't grow.
	orphanSince map[string]time.Time

	// gcRunning is the set of session ids whose pod is (or is coming) up per the
	// last authoritative cluster reconcile — Running or Creating. The sync GC
	// protects ONLY these. A Suspended session (the idle reaper sets replicas=0
	// from inside the cluster and cannot pause the host sync) is deliberately NOT
	// protected, so its thrashing orphan syncs are reaped after the grace window —
	// otherwise reaper-suspended sessions would leak ~8 Connecting syncs each
	// forever (the original 634-leak, re-labeled "gone"→"suspended").
	gcRunning map[session.ID]bool

	// idleTimeout is the reaper idle-timeout, used to render the warm session
	// "suspends in ~X" hint. Zero hides the hint.
	idleTimeout time.Duration

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

	// worktreeOps, when non-nil, is the injected convert-to-branch git surface
	// backing the `b` keymap. nil disables the flow (unit-test / library
	// default). Wired by the CLI via WithWorktreeOps.
	worktreeOps WorktreeOps

	// convert, when non-nil, is the active convert-to-branch modal. It captures
	// keys until esc/enter resolves it (see worktree.go).
	convert *convertModal

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

	// eventCache, when non-nil, persists each foreground session's transcript
	// events host-side so a cold re-attach rebuilds the conversation instantly and
	// streams only the delta (Workstream C). nil in unit tests (no caching).
	eventCache EventCache

	// actionErr is the last suspend/resume/destroy/create error, surfaced in
	// the detail pane. Cleared when an action succeeds.
	actionErr error

	// caps holds terminal capabilities detected once at startup. Every opt-in
	// Ghostty/terminal effect is gated behind a caps field; a zero Caps (the
	// default in tests) lights up nothing, so output matches today exactly.
	// See docs/archive/ghostty-terminal-effects.md.
	caps terminal.Caps
}

// New constructs a dashboard Model. backend may be nil (for unit tests that
// drive the model with manual seedMsg / PodEventMsg messages).
// connector may be nil; live per-session status will be skipped gracefully.
func New(backend Backend) *Model {
	return &Model{
		backend:           backend,
		sortKey:           SortByLastActive,
		sortDir:           SortDesc,
		keys:              DefaultKeyMap(),
		help:              newHelp(),
		liveSSECancels:    make(map[session.ID]context.CancelFunc),
		liveSSEChannels:   make(map[session.ID]<-chan session.Event),
		liveSSEClients:    make(map[session.ID]RunnerClient),
		liveSSECloses:     make(map[session.ID]func()),
		liveSSEConnecting: make(map[session.ID]bool),
		liveSSEStreamGen:  make(map[session.ID]uint64),
		syncProbedAt:      make(map[session.ID]time.Time),
		retained:          make(map[session.ID]*TranscriptModel),
		connectSem:        make(chan struct{}, maxConcurrentBackgroundConnects),
		observerActiveAt:  make(map[session.ID]time.Time),
		attachGate:        newAttachGate(),
		engine:            anim.NewEngine(),
		caps:              terminal.Detect(),
	}
}

// WithMaxObserverStreams overrides the steady-state cap on concurrently-
// established background observer forwards (§1d). Zero/negative keeps the
// default (defaultMaxObserverStreams). Call before Init.
func (m *Model) WithMaxObserverStreams(n int) *Model {
	if n > 0 {
		m.maxObserverStreams = n
	}
	return m
}

// WithConnector sets the Connector for live per-session SSE status updates.
// Call before Init to ensure SSE streams are opened on the initial seed.
func (m *Model) WithConnector(c Connector) *Model {
	m.connector = c
	return m
}

// WithObserverConnector injects the lightweight connect path used for background
// (passive) status streams — port-forward + runner health, no file-sync setup.
// nil leaves background streams on the full connector (the prior behavior).
func (m *Model) WithObserverConnector(c Connector) *Model {
	m.observerConnector = c
	return m
}

// WithSyncProber injects the sync-health probe used to render per-session sync
// status. nil disables the indicator (unit-test default).
func (m *Model) WithSyncProber(p SyncProber) *Model { m.syncProber = p; return m }

// WithSyncReaper injects the orphaned-sync GC used to terminate mutagen syncs
// whose session is gone. nil disables the GC (unit-test / no-backend default).
func (m *Model) WithSyncReaper(r SyncReaper) *Model { m.syncReaper = r; return m }

// WithIdleTimeout sets the reaper idle-timeout used to render the "suspends in"
// hint. Zero disables the hint.
func (m *Model) WithIdleTimeout(d time.Duration) *Model { m.idleTimeout = d; return m }

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
	PendingPermissionArg  string
	Model                 string
	InputTokens           int
	OutputTokens          int
	CacheReadTokens       int
	CacheWriteTokens      int
	TotalCostUSD          float64
	Branch                string
	Dirty                 bool
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

// EventCache persists a session's transcript events host-side (Workstream C) so a
// cold re-attach rebuilds the conversation instantly and resumes the runner SSE
// stream from the last cached seq, replaying only the delta instead of the whole
// history from 0. Implemented by the CLI on top of the local index; nil in unit
// tests (the transcript replays from the runner as before). Delta events
// (message/reasoning/tool .delta) are intentionally not cached — replay rebuilds
// final state from the completed events.
type EventCache interface {
	// LoadEvents returns a session's cached transcript events, in append order.
	LoadEvents(id session.ID) ([]session.Event, error)
	// AppendEvent persists one event to a session's cache (best effort).
	AppendEvent(id session.ID, ev session.Event) error
}

// WithEventCache registers the persistent host-side transcript cache.
func (m *Model) WithEventCache(c EventCache) *Model {
	m.eventCache = c
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
		// Periodically re-list the cluster to prune phantom sessions the watch
		// informer missed (a delete that happened before its cache synced).
		cmds = append(cmds, reconcileTickCmd())
	}

	// Start the warm-session sync/idle poll loop when an indicator source is
	// wired (the CLI injects a prober; unit tests leave it nil and skip ticking).
	if m.syncProber != nil || m.idleTimeout > 0 {
		cmds = append(cmds, syncPollCmd())
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

	case seedFailedMsg:
		// Initial seed failed (cluster unreachable): record it so the list shows an
		// actionable error + retry. Don't flip m.seeded — a later successful
		// seed/watch event or an explicit retry clears this.
		m.seedErr = msg.err
		return m, nil

	case PodEventMsg:
		m.seeded = true // first watch event counts as loaded (U2)
		m.seedErr = nil // a watch event proves the cluster is reachable
		cmd := m.applyPodEvent(msg.Event)
		cmds := []tea.Cmd{cmd, m.maybeStartAnim(), m.notifyIfBackgroundAttention(m.attachedID)}
		// Re-arm the reader for the next event. Guarded so reducer tests that drive
		// applyPodEvent via a synthetic PodEventMsg (and never set watchCh) don't
		// spawn a Cmd blocked forever on a nil channel.
		if m.watchCh != nil {
			cmds = append(cmds, watchNextCmd(m.watchCh))
		}
		return m, tea.Batch(cmds...)

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
		if nowFunc().Sub(m.toast.createdAt) > toastDismissAfter {
			m.toast = nil
			m.toastTickActive = false
			return m, nil
		}
		m.toastTickActive = true
		return m, toastTickCmd()

	case syncStatusMsg:
		for i := range m.sessions {
			if m.sessions[i].ID() == msg.id {
				m.sessions[i].SyncStatus = msg.status
				break
			}
		}
		return m, nil

	case idleStatusMsg:
		for i := range m.sessions {
			if m.sessions[i].ID() == msg.id {
				m.sessions[i].IdleSince = msg.idleSince
				break
			}
		}
		return m, nil

	case syncPollTickMsg:
		var cmds []tea.Cmd
		// Sync-health probes fork a `mutagen sync list` subprocess each, so gate them
		// on focus: the focused session(s) probe every tick, the rest back off (§4).
		if m.syncProbedAt == nil {
			m.syncProbedAt = make(map[session.ID]time.Time)
		}
		warm := make([]session.ID, 0, len(m.retained))
		for id := range m.retained { // warm sessions only
			warm = append(warm, id)
		}
		for _, id := range selectSyncProbeTargets(nowFunc(), warm, m.syncFocusSet(), m.syncProbedAt, unfocusedSyncPollInterval) {
			if c := m.probeSyncCmd(id); c != nil {
				cmds = append(cmds, c)
			}
		}
		// Idle probes reuse the session's already-open SSE client (no subprocess),
		// so they stay on every warm session each tick to keep the idle-reaper clock
		// responsive.
		for id := range m.retained {
			if c := m.probeIdleCmd(id); c != nil {
				cmds = append(cmds, c)
			}
		}
		cmds = append(cmds, syncPollCmd())
		return m, tea.Batch(cmds...)

	case reconcileTickMsg:
		// Kick a fresh full re-list and schedule the next tick. The list runs off
		// the Update goroutine and comes back as reconcileMsg.
		return m, tea.Batch(m.reconcileListCmd(), reconcileTickCmd())

	case reconcileMsg:
		states := []session.State(msg)
		m.reconcile(states)
		// Capture the authoritative "pod is up" set from this snapshot for the sync
		// GC. Using the fresh List (not m.sessions, which reconcile only prunes and
		// whose per-session Status can be stale) means a watch-missed-but-running
		// session is still protected, and a Suspended/gone one is correctly reaped.
		m.gcRunning = gcRunningSet(states)
		// Piggyback the orphaned-sync GC on the same snapshot. gcListOrphansCmd is
		// nil without a reaper, so this is a no-op in tests.
		return m, m.gcListOrphansCmd()

	case orphanGCMsg:
		return m, m.reapOrphans(msg.orphans)

	case liveSSEReadyMsg:
		// This connect resolved — clear its in-flight marker so guards reflect
		// reality (whether we register it below or discard it).
		delete(m.liveSSEConnecting, msg.id)
		// Guard: if the session was deleted or suspended while the connector
		// was in flight, cancel the stream immediately (B14). We check the
		// session still exists and is still in a Running state before storing
		// the cancel; otherwise the stream would run until program exit.
		sess := m.sessionByID(msg.id)
		if sess.ID() == "" || sess.State.Status != session.StatusRunning {
			msg.discard()
			return m, nil
		}
		// The foreground session's stream is owned by its transcript's active
		// client (B2's one-client intent; detach explicitly restarts the passive
		// stream). A background connector that becomes ready after a
		// detach→fast-reattach would otherwise install a SECOND passive stream —
		// double client + extra port-forward — alongside the active one. Mirror
		// the liveSSEReconnectMsg guard below: cancel the redundant incoming stream.
		if msg.id == m.attachedID {
			msg.discard()
			return m, nil
		}
		// A stream is already registered for this session — this incoming one is a
		// raced duplicate (two connects launched before either reached ready) that
		// beat the in-flight guard. Cancel it and keep the established stream;
		// blindly overwriting the map would orphan the live stream's cancel func,
		// leaking an uncancellable connection whose later StreamEnded would tear
		// down the healthy stream (§1a connect-side).
		if _, exists := m.liveSSECancels[msg.id]; exists {
			msg.discard()
			return m, nil
		}
		// Store the cancel, transport close, channel, client, and generation for
		// this SSE stream.
		m.liveSSECancels[msg.id] = msg.cancel
		if msg.close != nil {
			m.liveSSECloses[msg.id] = msg.close
		}
		m.liveSSEChannels[msg.id] = msg.ch
		m.liveSSEClients[msg.id] = msg.client
		m.liveSSEStreamGen[msg.id] = msg.gen
		// The just-established stream is the warmest (LRU); stamp it so it is never
		// the victim of the cap enforcement it may trigger, then evict the coldest
		// unprotected stream(s) if this registration pushed us over the cap (§1d).
		m.touchObserver(msg.id)
		m.enforceObserverCap(msg.id)
		// Mark the session catching-up (§1a step 3): the after=<seq> replay burst
		// that follows mutates state but must not toast/flash. Cleared at the
		// EventStreamLive boundary in handleRunnerEvent.
		for i := range m.sessions {
			if m.sessions[i].ID() == msg.id {
				m.sessions[i].catchingUp = true
				break
			}
		}
		// Build (or reuse) the warm transcript for this session so the background
		// stream keeps a full, live chat in memory — making a later show an O(1)
		// swap instead of a rebuild+replay. Skip opencode sessions (no Go
		// transcript).
		if sess.State.Backend != session.BackendOpenCode {
			m.ensureRetained(sess, msg.client)
			m.maybeWarnWarm()
		}
		return m, liveSSEBatchCmd(msg.id, msg.ch, msg.gen)

	case liveSSEReconnectMsg:
		// Backoff elapsed — try to re-open the background stream, unless the
		// session is gone/suspended (cluster watch owns its glyph now) or a pod
		// event already re-established the stream while we waited.
		sess := m.sessionByID(msg.id)
		if sess.ID() == "" || sess.State.Status != session.StatusRunning {
			return m, nil
		}
		// Skip only when a stream is already REGISTERED. Deliberately do NOT skip
		// on liveSSEConnecting: the reconnect tick is this session's only path to
		// the retry/degrade loop, so if an in-flight connect (e.g. a watch-driven
		// startLiveSSECmd that raced into the backoff window) later FAILS it
		// schedules no retry of its own — suppressing the reconnect here would then
		// strand a Running session with no stream and no pending retry (its glyph
		// frozen, degradeUnreachable never reached). Letting the reconnect proceed
		// at worst races that connect into a duplicate, which the generation token
		// + the ready-handler's cancel-incoming already make safe (§1a connect-side
		// review finding).
		if _, exists := m.liveSSECancels[msg.id]; exists {
			return m, nil
		}
		// The foreground session's stream is owned by its transcript (the attach
		// path cancels the background one deliberately — B2's one-client intent);
		// detach explicitly restarts the passive stream, so don't race it here.
		if msg.id == m.attachedID {
			return m, nil
		}
		return m, m.reconnectLiveSSECmd(sess, msg.attempt)

	case liveSSEConnectFailedMsg:
		// An initial background connect failed; clear its in-flight marker so a
		// later guard can retry. The session keeps its cluster-derived status
		// (graceful degradation) — no status change here. Also release the
		// hydrate-armed catch-up suppression: no stream means no EventStreamLive
		// boundary is coming to clear it, and a stuck flag would suppress the
		// toast for a genuinely-pending hydrated attention state for as long as
		// the pod stays unreachable-but-Running. A later successful connect
		// re-arms it at liveSSEReadyMsg.
		delete(m.liveSSEConnecting, msg.id)
		for i := range m.sessions {
			if m.sessions[i].ID() == msg.id {
				m.sessions[i].catchingUp = false
				break
			}
		}
		return m, m.notifyIfBackgroundAttention(m.attachedID)

	case liveSSEReconnectFailedMsg:
		// A reconnect attempt couldn't open the stream — clear its in-flight
		// marker. Retry with backoff while the cluster still believes the pod is
		// Running and we have budget left; otherwise declare it unreachable and
		// show its honest status.
		delete(m.liveSSEConnecting, msg.id)
		// Terminal forward (§1d Done()-wiring): the connect failed because the
		// Sandbox is gone (the port-forward's NotFound stop, c191c85, surfaces as
		// session.ErrSessionGone through the connector). Retrying can only fail the
		// same way, so drop the observer NOW rather than exhaust the backoff budget,
		// and let the watch/reconcile path prune the row. dropRetained releases the
		// warm model; degradeUnreachable shows the honest end-state meanwhile.
		if errors.Is(msg.err, session.ErrSessionGone) {
			m.dropRetained(msg.id)
			delete(m.observerActiveAt, msg.id)
			m.degradeUnreachable(msg.id)
			return m, m.notifyIfBackgroundAttention(m.attachedID)
		}
		next := msg.attempt + 1
		sess := m.sessionByID(msg.id)
		if sess.ID() != "" && sess.State.Status == session.StatusRunning && next < liveSSEMaxRetries {
			return m, liveSSEReconnectTick(msg.id, next, liveSSEReconnectDelay(next))
		}
		m.degradeUnreachable(msg.id)
		return m, m.notifyIfBackgroundAttention(m.attachedID)

	case RunnerEventMsg:
		mdl, cmd := m.handleRunnerEvent(msg)
		var cmds []tea.Cmd
		cmds = append(cmds, cmd, m.maybeStartAnim())
		// Background attention notification: if a session other than the
		// attached one needs attention, surface a toast.
		cmds = append(cmds, m.notifyIfBackgroundAttention(m.attachedID))
		return mdl, tea.Batch(cmds...)

	case RunnerEventBatchMsg:
		// §4 E5: a drained burst of passive-stream events reduced in ONE Update
		// pass. handleRunnerEventBatch applies every event (per-event side effects
		// identical to the single path); the post-handling below runs ONCE per
		// batch — that batching of the render pipeline is the whole point.
		mdl, cmd := m.handleRunnerEventBatch(msg)
		var cmds []tea.Cmd
		cmds = append(cmds, cmd, m.maybeStartAnim())
		cmds = append(cmds, m.notifyIfBackgroundAttention(m.attachedID))
		return mdl, tea.Batch(cmds...)

	case toastMsg:
		m.toast = &notification{
			sessionID: msg.id,
			title:     msg.title,
			note:      msg.note,
			status:    msg.status,
			createdAt: nowFunc(),
		}
		var cmds []tea.Cmd
		// Alongside the in-TUI toast, fire a real OS notification so a background
		// session needing attention escapes the terminal even when the user has
		// tabbed away. It MUST go out-of-band via tea.Raw: Bubble Tea v2's cell
		// renderer silently drops control strings spliced into View content (the
		// same reason Kitty graphics use tea.Raw — see cmd/tuikit-demo/kitty.go).
		// NotifyString picks the right escape per terminal (OSC 777 on Ghostty,
		// OSC 9 on iTerm2/WezTerm) and returns "" on terminals we can't target.
		// Suppressed under the global off switch (NO_COLOR / SANDBOX_REDUCE_MOTION).
		// The upstream edge-dedup makes this one-shot per attention episode.
		if !m.caps.ReduceMotion {
			if osc := terminal.NotifyString(m.caps, msg.title, msg.note); osc != "" {
				cmds = append(cmds, tea.Raw(osc))
			}
		}
		// Only start a tick loop if one isn't already running, else a burst of
		// toasts spawns multiple concurrent loops (faster-than-spec animation +
		// wasted timers). The running loop picks up the new toast on its next tick.
		if !m.toastTickActive {
			m.toastTickActive = true
			cmds = append(cmds, toastTickCmd())
		}
		return m, tea.Batch(cmds...)

	case worktreeStatusMsg:
		return m, m.openConvertModal(msg)

	case convertResultMsg:
		return m, m.handleConvertResult(msg)

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

	case approveResultMsg:
		// Surface a failed approve/deny instead of leaving the optimistic UI
		// looking successful: the decision never reached the runner, so the
		// session is still blocked. Reuses the detail-pane ErrorBlock render.
		if msg.err != nil {
			m.actionErr = fmt.Errorf("resolve permission: %w", msg.err)
		} else {
			m.actionErr = nil
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
	// Flush a final snapshot for every session before teardown (§1a step 4).
	// Snapshots are otherwise coalesced to a 3s throttle and only force-saved on
	// status transitions, so at quit the persisted resume cursor (lastSeq) plus
	// the last few seconds of usage/branch/cost are stale — which makes EVERY
	// relaunch replay a tail of history as if it were live. Force bypasses the
	// throttle; saveSnapshot no-ops when there is no store (unit tests).
	for i := range m.sessions {
		m.saveSnapshot(&m.sessions[i], true)
	}
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

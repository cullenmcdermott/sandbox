package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"math"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cullenmcdermott/sandbox/internal/models"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard/chat"
	"github.com/cullenmcdermott/sandbox/tui/anim"
	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/list"
	"github.com/cullenmcdermott/sandbox/tui/terminal"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// --------------------------------------------------------------------------
// TranscriptModel — the per-session transcript screen (spec screen 2).
//
// It streams a session's turns into a scrollback viewport with markdown-
// rendered agent replies, a prompt input, and an inline gold permission box.
// It reuses the dashboard's charmtone palette so the two screens share one
// visual language. The SSE + reconnect machinery mirrors the standalone
// tui.Model but renders through bubbles/glamour instead of raw line slicing.
// --------------------------------------------------------------------------

// transcript block kinds.
type tblockKind int

const (
	blockUser tblockKind = iota
	blockAssistant
	blockTool
	blockToolErr
	blockInfo
	blockError
	blockToolCard  // a structured tool call (see toolCard)
	blockShell     // a one-shot `!` shell result (pre-styled multi-line text)
	blockSubagent  // a dispatched Task rendered as a nested card (see subagentCard)
	blockFooter    // a dim per-turn model/cost footer (pre-styled, render verbatim)
	blockReasoning // thinking/reasoning block emitted by Reasoning* events (B8)
)

// toolStatus is the lifecycle of a tool-call card.
type toolStatus int

const (
	toolRunning toolStatus = iota
	toolOK
	toolErr
)

// toolCard is a compact, Crush-style tool-call: tool name + key argument
// (path/command/pattern) + a result summary, rendered as one line whose icon
// and color reflect its status. It is mutated in place when the matching
// tool.completed / tool.failed event arrives.
type toolCard struct {
	tool    string
	arg     string
	status  toolStatus
	summary string
}

// tblock is one rendered unit of the transcript. text is raw (markdown for
// assistant blocks) so blocks can be re-rendered at a new width on resize.
// tool is non-nil only for blockToolCard.
type tblock struct {
	kind tblockKind
	text string
	tool *toolCard
	sub  *subagentCard // non-nil only for blockSubagent
}

type transcriptPermission struct {
	id        string
	tool      string
	adds      int       // line additions (for the +N stat)
	dels      int       // line deletions (for the −N stat)
	diffLines []string  // "+"/"−"-prefixed lines, revealed by [↵] view diff
	isPlan    bool      // ExitPlanMode plan card (gold, three actions)
	plan      string    // the plan markdown, when isPlan
	since     time.Time // when the request arrived, for the appear transition
}

// transcript message types (prefixed to avoid colliding with the dashboard's
// own message set).
type tEventMsg session.Event

// tEventBatchMsg carries one or more SSE events coalesced by waitForEvent. A
// burst of stream deltas is applied in a single Update+View instead of one per
// event, so a fast turn doesn't re-render per delta or starve keystrokes (T1).
type tEventBatchMsg []session.Event

type tStreamEndedMsg struct{}
type tReconnectedMsg struct{ client RunnerClient }
type tReconnectFailedMsg struct{ err error }
type tRetryReconnectMsg struct{}
type turnErrMsg struct{ err error }

// permResolveErrMsg surfaces a failed permission approve/deny so the optimistic
// "[permission approved]" block isn't left looking successful when the decision
// never reached the runner (the session is still blocked waiting on it).
type permResolveErrMsg struct{ err error }

// TranscriptModel is the Bubble Tea model for a single attached session.
type TranscriptModel struct {
	client      RunnerClient
	ref         session.Ref
	projectPath string
	agent       string
	title       string

	width, height int

	body       *list.List          // virtualized transcript body (replaces viewport)
	items      []*blockItem        // one per m.blocks entry, parallel and index-stable
	streamItem *blockItem          // ephemeral trailing item for the live streaming turn
	streamAI   *chat.AssistantItem // persistent AI for live tail (A2 incremental render)
	input      textarea.Model      // multi-line composer (boxed; shift+enter inserts a newline)
	permBox    string              // cached rendered permission box (recomputed in layout)
	palette    string              // cached rendered slash palette (recomputed in layout)
	cmdSel     int                 // selected index in the slash palette

	// Grouped help overlay (`?` / /help), shared with the command center.
	showHelp bool
	helpUI   helpModel

	blocks       []tblock
	assistantBuf strings.Builder
	streaming    bool

	// droppedPartialIdx is the block index of a partial assistant message we
	// committed when the SSE stream dropped mid-message (B9 preserves the partial
	// so it isn't lost). It is -1 when there is no such pending block. On
	// reconnect the runner replays the message's `completed` event (full text) at
	// a higher seq; EventMessageCompleted replaces this block in place rather than
	// appending a duplicate, so the user doesn't see the reply twice (RV9).
	droppedPartialIdx int

	// pendingTools is a FIFO of block indices for tool cards awaiting their
	// result. tool.completed/failed events carry no tool name, so cards are
	// matched to results in start order.
	pendingTools []int

	// reasoningBuf accumulates ReasoningDelta text between ReasoningStarted and
	// ReasoningCompleted events (B8). Flushed to a blockReasoning block on
	// ReasoningCompleted.
	reasoningBuf strings.Builder
	reasoning    bool

	// Subagent nesting (slice 4b): Task cards keyed by tool_use id, plus a child
	// tool_use id → card index so child tool.* events nest under their Task.
	subagents  map[string]*subagentCard
	childIndex map[string]*toolCard

	// flatTools maps a (non-subagent) tool_use id to its card's block index, so
	// the duplicate tool.started the SDK emits per tool under
	// includePartialMessages (streaming content_block_start + the full assistant
	// message) collapses onto one card (C2) instead of rendering twice.
	flatTools map[string]int

	status     SessionStatus
	turnActive bool
	// activeTurnID is the runner's id for the in-flight turn, captured from the
	// turn.started event. It is the precise target for an interrupt; when it is
	// still empty (esc fired before turn.started lands) the interrupt request
	// carries an empty segment and the runner falls back to its sole active turn.
	activeTurnID session.TurnID
	lastSeq      uint64

	// Live working indicator (#4): set while a turn runs.
	turnStart     time.Time
	workFrame     int
	working       bool // the work-tick loop is scheduled
	inTok         int
	outTok        int
	costUSD       float64
	cacheReadTok  int
	cacheWriteTok int

	// Status-line state (slice 1): model + cwd from session.started; branch +
	// dirty/ahead/behind from workspace.status; ctxLimit from models.Limit; and
	// the per-attach permission mode cycled with shift+tab.
	model    string
	cwd      string
	ctxLimit int
	branch   string
	dirty    bool
	ahead    int
	behind   int
	mode     permMode
	pending  *transcriptPermission
	showDiff bool

	// Rate-limit windows (rate_limit.updated): real claude.ai plan utilization +
	// reset instants from the SDK /usage data. rlAvailable is false for
	// API-key/Bedrock/Vertex sessions; the status line hides the windows then
	// rather than fabricating values. rlSeen gates the windows until the first
	// event arrives. Reset times parse from RFC3339; zero => unknown.
	rlSeen      bool
	rlAvailable bool
	// rlSubscription is the claude.ai plan ('pro'/'max'/…), empty for headless
	// setup-token / API-key sessions. When rlAvailable is false and this is
	// empty, the status line shows "usage n/a (headless auth)" so the missing
	// windows read as an auth-mode limitation, not a glitch.
	rlSubscription string
	rl5hUtil       float64
	rl5hReset      time.Time
	rl7dUtil       float64
	rl7dReset      time.Time
	// Per-model weekly caps (Max plans). The *Seen flag distinguishes an absent
	// window from a present-but-0% one; the status line surfaces the one whose
	// model matches the attached model (see activeModelWindow).
	rlOpusSeen    bool
	rlOpusUtil    float64
	rlOpusReset   time.Time
	rlSonnetSeen  bool
	rlSonnetUtil  float64
	rlSonnetReset time.Time

	// availableModels is the account's supported-model list (models.available),
	// used to build the /model palette dynamically. Empty until the first turn's
	// init fetch arrives; the palette falls back to the opus/sonnet/haiku
	// aliases meanwhile (see commands.go modelGroupCmds).
	availableModels []session.ModelInfo

	// caps holds terminal capabilities (copied from the dashboard Model on
	// attach). Gates the opt-in ctx-gauge sweep + other Ghostty effects; a zero
	// Caps (tests, non-truecolor, NO_COLOR) lights up nothing, so the status
	// line is byte-identical to today. See docs/ghostty-terminal-effects.md.
	caps terminal.Caps

	// Stage 3 (Kitty graphics ctx gauge). kittyGaugeBucket is the last fill
	// fraction (quantized to whole percent) we transmitted an image for;
	// kittyGaugeID is that image's id (a new id each change forces a re-fetch);
	// pendingKitty holds the one-shot APC transmission queued when the bucket
	// changes, drained into the next frame by App.View. All zero/empty unless
	// caps.KittyGraphics. See docs/ghostty-terminal-effects.md §4.
	kittyGaugeBucket int
	kittyGaugeID     uint32
	pendingKitty     string

	// modelOverride is the in-session /model selection, sent as TurnInput.Model
	// on the next turn (empty => session/account default). Kept separate from
	// `model` (the SDK-reported active id used for display) so the status line
	// doesn't flicker between the requested alias and the resolved id.
	modelOverride string
	// defaultModel is the account/session default model id, captured from the
	// first session.started (before any /model override). /model-default restores
	// the status line to it optimistically; it self-heals to the SDK-resolved id
	// on the next session.started.
	defaultModel string

	// effortOverride is the in-session /effort selection, sent as
	// TurnInput.Effort on the next turn (empty => the SDK's adaptive-thinking
	// default). Stores the SDK wire value ("max" for the "ultracode" label), not
	// the display label. In-memory per-attach, NOT parked/snapshotted — same
	// lifecycle as modelOverride.
	effortOverride string

	// lastKeyAt is the time of the previous key event, used to gate the inline
	// permission box against type-ahead: an answer key (a/d/enter) only resolves
	// once input has been quiet for permissionGraceQuiet since both the box
	// appeared and the last keystroke, so a keystroke in flight when the box pops
	// can't auto-approve/deny it (chat-rendering §2.6).
	lastKeyAt time.Time

	// imode is the input mode. With vim modal editing off (the default) the
	// transcript stays in INSERT — the prompt is always focused so every key
	// types. /vim turns modal editing on (vimEnabled), opening in NORMAL where
	// single-key chords work (i/a insert, j/k scroll, / search, q detach) and
	// i/a enter INSERT (keystrokes type; esc returns to NORMAL).
	imode inputMode
	// vimEnabled gates vim-style modal editing. Off by default: the prompt is
	// always focused (imode pinned to INSERT) so there's no "press i to type"
	// surprise and esc keeps its interrupt/steer/detach meaning; /vim toggles it
	// on for the NORMAL/INSERT chords and the mode badge.
	vimEnabled bool

	reconnect    ReconnectFunc
	reconnecting bool
	terminating  bool
	// reconnectAttempts counts consecutive failed reconnect tries; it drives the
	// backoff and message throttling, and resets to 0 on a successful reconnect
	// (RV29: previously the loop retried every flat 3s forever, hammering the
	// connector and spamming the transcript with identical lines).
	reconnectAttempts int
	// reconnectStartedAt is when the current reconnect sequence began; the header
	// shows elapsed time so a slow cold-pod resume reads as progress, not a freeze.
	reconnectStartedAt time.Time
	// reconnectGaveUp is set when reconnect hits a permanent condition (the session
	// no longer exists). The retry loop stops and the header shows a terminal
	// "session gone" state instead of an endless "reconnecting…" (Fix D).
	reconnectGaveUp bool
	// reconnectStages carries live connect-stage updates from the in-flight
	// doReconnect goroutine into the Update loop (drained by waitForReconnectStage),
	// so the header shows "reconnecting — Starting pod / Waiting for runner" during
	// a slow cold-pod resume instead of a static label. reconnectStage/Detail hold
	// the latest; reconnectStageKnown gates the richer label until the first update.
	reconnectStages     chan reconnectStageMsg
	reconnectStage      ConnectStage
	reconnectDetail     string
	reconnectStageKnown bool

	// initialPrompt is submitted as the first turn once the stream is live
	// (from `sandbox claude "…"`). Consumed once on Init.
	initialPrompt string

	// queuedPrompt is a message typed while a turn runs; it is auto-sent when
	// the turn completes. When non-empty, esc acts as live steering.
	queuedPrompt string

	// search state for in-transcript search (slice 5i).
	search searchModel

	// unread marker: index of the first block not seen when detached.
	unreadIndex int

	// prompt composition state (slice 5g): multi-line buffer + $EDITOR draft.
	composeBuf    string
	composeCursor int

	events       <-chan session.Event
	streamCancel context.CancelFunc // cancels the live Events() stream (NEW-5)

	// Workstream C — replay/live boundary. While true, the stream (or the local
	// cache) is still feeding HISTORICAL events being caught up after an attach or
	// reconnect; the prompt line shows "loading transcript…" and the working
	// spinner is suppressed so replay can't masquerade as a live turn (#1). The
	// runner's `: replay-complete` comment (surfaced as session.EventStreamLive)
	// flips it false; a genuinely in-flight turn then resumes "working" because
	// turnActive is carried across the boundary. replayedCount drives the loading
	// progress readout.
	replaying     bool
	replayedCount int
	// attachSeq is the session's last-known event seq at attach (the dashboard's
	// resume cursor). It is the replay WATERMARK: catch-up is "done" once
	// m.lastSeq reaches it, so the loading state self-clears even if the runner
	// never sends the `: replay-complete` marker (an older runner, or a proxy that
	// strips SSE comments). attachSeq==0 means a fresh session with no history —
	// we never enter the loading state then, so a brand-new chat shows no "loading
	// transcript…" flash.
	attachSeq uint64

	// cache, when non-nil, is the host-side transcript cache (Workstream C): the
	// transcript loads it on a cold open to rebuild history instantly and appends
	// each non-delta event it observes (whether via its own foreground stream or
	// the warm background feed), so the next cold attach resumes from the cached
	// head instead of replaying from seq 0. nil in tests.
	cache EventCache
	// lastCachedSeq is the highest seq written to the cache. It makes maybeCache
	// idempotent per seq so a brief double-feed at the warm→foreground handoff
	// (a buffered background event racing the foreground stream) can't write a
	// duplicate line that would double-render on the next cold replay.
	lastCachedSeq uint64

	// bulkReplay is set while loadCachedTranscript applies a batch of cached
	// events. It makes syncBody a no-op so the per-event reconcile (which
	// re-fingerprints every prior item and rebuilds the list) is skipped during
	// the replay; the caller reconciles exactly once at the end, turning an
	// O(N^2) cold load into O(N).
	bulkReplay bool
	// reconciles counts reconcileItems() calls — a behavioral counter the bulk
	// replay test asserts on to prove the replay collapses to a single reconcile.
	reconciles int
	// fpComputes counts blockFP() calls — a behavioral counter proving that
	// immutable text blocks are fingerprinted once, not re-hashed every reconcile.
	fpComputes int
}

// cacheableEvent reports whether an event belongs in the host-side transcript
// cache. Synthetic stream markers (EventStreamLive, seq 0) are excluded, and the
// high-volume incremental deltas are skipped — replay rebuilds final state from
// the started/completed events, so caching deltas would only bloat the file.
func cacheableEvent(ev session.Event) bool {
	if ev.Seq == 0 {
		return false
	}
	switch ev.Type {
	case session.EventMessageDelta, session.EventReasoningDelta, session.EventToolDelta:
		return false
	}
	return true
}

// maybeCache appends an event to the host-side cache exactly once, in seq order.
// Used by BOTH the foreground stream (tEventMsg) and the warm background feed
// (ingest): a session is fed by exactly one of those at a time (attach cancels
// the background stream, detach cancels the foreground stream), and the
// lastCachedSeq guard covers the brief handoff race. Best effort — a write
// failure never breaks the turn (the runner's events.db is authoritative).
func (m *TranscriptModel) maybeCache(ev session.Event) {
	if m.cache == nil || !cacheableEvent(ev) || ev.Seq <= m.lastCachedSeq {
		return
	}
	if err := m.cache.AppendEvent(m.ref.ID, ev); err == nil {
		m.lastCachedSeq = ev.Seq
	}
}

// NewTranscript builds a transcript model for an attached session. reconnect
// re-establishes the connection (resume + port-forward + fresh client) when the
// SSE stream drops; it may be nil (no auto-reconnect).
func NewTranscript(client RunnerClient, sess Session, reconnect ReconnectFunc) *TranscriptModel {
	in := textarea.New()
	// The "❯" marks only the first row; wrapped/continuation rows align under it
	// with blank gutters, so a multi-line message reads as one prompt, not many.
	in.SetPromptFunc(2, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
			return "❯ "
		}
		return "  "
	})
	in.Placeholder = "type a message…"
	in.ShowLineNumbers = false
	in.CharLimit = 0
	in.SetHeight(1) // grows with content up to maxInputRows (see layout/renderInput)
	// Vim modal editing is off by default, so the composer opens in INSERT and is
	// focused by Init (enterInsert focuses, enterNormal blurs). /vim switches to
	// the modal NORMAL/INSERT register.

	return &TranscriptModel{
		client:      client,
		ref:         session.Ref{ID: sess.ID()},
		projectPath: sess.State.ProjectPath,
		agent:       sess.State.Backend,
		title:       sess.Title,
		status:      sess.DashStatus,
		body:        list.New(),
		input:       in,
		imode:       modeInsert, // vim off by default → always-focused INSERT
		// Sessions start in bypassPermissions (yolo) by default — the agent runs
		// without per-tool permission prompts. Relies on the IS_SANDBOX root-guard
		// fix (k8s buildEnv + runner spawn env); without it the first turn would
		// exit 1. Cycle with shift+tab or /auto /normal /plan to step down.
		mode:      modeBypass,
		reconnect: reconnect,
		// Replay watermark (Workstream C): the dashboard's last-known seq for this
		// session. 0 => a fresh session, so we never show a "loading transcript…"
		// flash; >0 => catch-up is done once the stream reaches it (works without
		// the runner's replay-complete marker).
		attachSeq: sess.lastSeq,

		droppedPartialIdx: -1,
	}
}

// ParkedTranscriptState holds the lightweight view/input state from a detached
// transcript so it can be restored when the user re-attaches to the same session
// (B3: "esc detaches with selection and scroll intact"). Fields that depend on
// block contents (scroll offset, search matches) are not saved because the SSE
// replay rebuilds blocks from scratch; only user-authored or session-config
// state is worth preserving.
type ParkedTranscriptState struct {
	composeBuf    string
	composeCursor int
	queuedPrompt  string
	searchQuery   string
	mode          permMode
}

// ParkState captures the ephemeral view/input state worth preserving across a
// detach→reattach cycle.
func (m *TranscriptModel) ParkState() ParkedTranscriptState {
	return ParkedTranscriptState{
		composeBuf:    m.composeBuf,
		composeCursor: m.composeCursor,
		queuedPrompt:  m.queuedPrompt,
		searchQuery:   m.search.query,
		mode:          m.mode,
	}
}

// RestoreParkedState applies previously parked view/input state to this model.
// Called after NewTranscript + Init on a re-attach to the same session.
func (m *TranscriptModel) RestoreParkedState(ps ParkedTranscriptState) {
	m.composeBuf = ps.composeBuf
	m.composeCursor = ps.composeCursor
	m.queuedPrompt = ps.queuedPrompt
	if ps.searchQuery != "" {
		m.search = searchModel{open: true, query: ps.searchQuery}
		m.updateSearchMatches()
	}
	m.mode = ps.mode
}

// Init focuses the prompt and opens the event stream. If an initial prompt was
// supplied (from `sandbox claude "…"`), it is submitted as the first turn.
func (m *TranscriptModel) Init() tea.Cmd {
	var cmds []tea.Cmd
	// Vim modal editing is off by default: focus the prompt so the user can type
	// immediately (no "press i" surprise). /vim drops to NORMAL when enabled.
	if !m.vimEnabled {
		cmds = append(cmds, m.input.Focus())
	}
	if m.client != nil {
		// Workstream C: seed history from the host-side cache (cold open) before
		// opening the stream, so attach is instant and the stream resumes from the
		// cached head (after=lastSeq) rather than replaying from seq 0.
		m.loadCachedTranscript()
		cmds = append(cmds, m.startEventStream())
	}
	if m.initialPrompt != "" {
		cmds = append(cmds, func() tea.Msg {
			return submitTextMsg{text: m.initialPrompt}
		})
	}
	return tea.Batch(cmds...)
}

// submitTextMsg triggers an automatic submission of the given prompt text.
type submitTextMsg struct{ text string }

// Update is the message handler. Detach keys (esc / ctrl+]) are intercepted by
// the parent App before delegation, so they never reach here.
func (m *TranscriptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case submitTextMsg:
		return m, m.submitText(msg.text)

	case tEventMsg:
		ev := session.Event(msg)
		cmd := m.handleEvent(ev)
		// Workstream C: mirror each streamed (non-delta) event into the host-side
		// cache so the next cold attach replays from here.
		m.maybeCache(ev)
		cmds := []tea.Cmd{m.maybeStartWorking(), cmd}
		if m.events != nil {
			cmds = append(cmds, m.waitForEvent)
		}
		return m, tea.Batch(cmds...)
	case tEventBatchMsg:
		// A coalesced burst (waitForEvent): apply every event in order, then render
		// ONCE (Update→View runs a single frame per message). handleEvent still
		// streamDelta()s per event, but that only re-fingerprints the live tail —
		// the expensive glamour render happens once, at View, for the whole batch.
		cmds := make([]tea.Cmd, 0, len(msg)+2)
		for _, e := range msg {
			if c := m.handleEvent(e); c != nil {
				cmds = append(cmds, c)
			}
			m.maybeCache(e)
		}
		cmds = append(cmds, m.maybeStartWorking())
		if m.events != nil {
			cmds = append(cmds, m.waitForEvent)
		}
		return m, tea.Batch(cmds...)
	case editorResultMsg:
		m.applyEditorResult(msg)
		return m, nil

	case workTickMsg:
		m.workFrame++
		// Re-render only when a subagent is in flight, so its header/child
		// spinner animates without forcing a re-render for flat cards.
		if m.hasRunningSubagent() {
			m.syncBody()
		}
		// Keep the 150ms work-tick loop running only while a turn is genuinely
		// live. An UNGRACEFUL stream drop (half-open socket from a suspended or
		// lost pod, with no SessionTerminating event) leaves turnActive set with
		// nothing to clear it; without the !reconnecting guard the loop would
		// re-fire forever, burning a full-screen repaint every 150ms and starving
		// keystroke handling on the single Bubble Tea goroutine (the sluggish-typing
		// symptom, same disconnected state as a pinned "reconnecting…"). The loop
		// re-arms via maybeStartWorking once live events flow again post-reconnect.
		if m.turnActive && !m.reconnecting {
			return m, workTickCmd()
		}
		m.working = false
		return m, nil

	case tStreamEndedMsg:
		// Commit any buffered streaming text before reconnecting (B9).
		// The runner won't re-emit deltas <= lastSeq, so anything already
		// buffered would be lost when EventMessageStarted resets assistantBuf.
		// Remember the committed block so the replayed message.completed (full
		// text, higher seq) replaces it in place instead of appending a duplicate
		// reply on reconnect (RV9).
		if idx := m.finalizeStreaming(); idx >= 0 {
			m.droppedPartialIdx = idx
		}
		m.cancelStream()
		if m.reconnect == nil {
			// A permanently-ended self-streaming transcript has no reconnect to
			// re-replay and clear replay state, so settle the prompt line out of
			// any "loading transcript…"/working state rather than wedging on it
			// (Workstream C boundary review; latent — production foreground
			// transcripts always have a non-nil reconnect).
			m.replaying = false
			m.working = false
			m.appendBlock(blockInfo, "[stream ended]")
			return m, nil
		}
		if !m.reconnecting {
			m.reconnecting = true
			m.reconnectStartedAt = nowFunc()
			m.appendBlock(blockInfo, "[connection lost — reconnecting…]")
		}
		return m, m.startReconnect()

	case tReconnectedMsg:
		m.client = msg.client
		m.reconnecting = false
		m.reconnectGaveUp = false
		m.reconnectStartedAt = time.Time{}
		m.reconnectStageKnown = false
		m.terminating = false
		// M37: re-anchor the permission grace to now. The model is reused across
		// an auto-reconnect, so a pending box keeps its original `since`; after a
		// multi-second drop that is already past permissionGraceCap, letting a
		// held key instantly answer the box as it becomes live again.
		if m.pending != nil {
			m.pending.since = nowFunc()
		}
		m.reconnectAttempts = 0
		m.appendBlock(blockInfo, "[reconnected]")
		return m, m.startEventStream()

	case tReconnectFailedMsg:
		// Permanent condition: the session was deleted from the cluster. Retrying
		// can never succeed, so stop the loop and show a terminal "session gone"
		// state instead of an endless "reconnecting…" (Fix D). The user leaves with
		// q; the background dashboard already reflects the deletion.
		if errors.Is(msg.err, session.ErrSessionGone) {
			m.reconnecting = false
			m.reconnectGaveUp = true
			m.turnActive = false
			m.working = false
			m.appendBlock(blockError, "✗ session no longer exists — press q to return to the dashboard")
			return m, nil
		}
		m.reconnectAttempts++
		delay := reconnectBackoff(m.reconnectAttempts)
		// Throttle transcript spam: show the first few failures (with the growing
		// backoff), then go quiet while still retrying, so a permanently dead or
		// destroyed session doesn't fill scrollback with an identical line every
		// few seconds forever (RV29).
		switch {
		case m.reconnectAttempts <= reconnectVerboseAttempts:
			m.appendBlock(blockInfo, fmt.Sprintf("[reconnect failed: %v — retrying in %s]", msg.err, delay))
		case m.reconnectAttempts == reconnectVerboseAttempts+1:
			m.appendBlock(blockInfo, "[still trying to reconnect in the background…]")
		}
		return m, tea.Tick(delay, func(time.Time) tea.Msg { return tRetryReconnectMsg{} })

	case tRetryReconnectMsg:
		return m, m.startReconnect()

	case reconnectStageMsg:
		// Live connect-stage update from the in-flight reconnect (FU1). Keep
		// draining until the channel closes so the header tracks the resume.
		if msg.done {
			return m, nil
		}
		m.reconnectStage = msg.stage
		m.reconnectDetail = msg.detail
		m.reconnectStageKnown = true
		return m, m.waitForReconnectStage

	case turnErrMsg:
		// The turn never started server-side (StartTurn POST failed), so no
		// turn.started/failed/completed event will ever arrive to clear the
		// optimistic busy state beginTurn() set in submitText. Roll it back
		// here — mirroring the turn-end events — so the working spinner stops
		// and the next prompt actually sends instead of being silently queued
		// behind a phantom active turn (recoverable only by detach/reattach).
		m.finalizeStreaming()
		m.status = StatusNeedsInput
		m.turnActive = false
		m.working = false
		m.appendBlock(blockError, "✗ "+msg.err.Error())
		return m, nil

	case interruptFailedMsg:
		// The interrupt request didn't reach the runner (the turn keeps running).
		// Surface it as a dim notice rather than silently doing nothing, so a
		// future regression in the interrupt path is visible.
		m.appendBlock(blockInfo, "[interrupt failed: "+msg.err.Error()+"]")
		return m, nil

	case permResolveErrMsg:
		// The approve/deny never reached the runner, so the agent is still blocked
		// on the permission. Surface it loudly instead of leaving the optimistic
		// "[permission approved/denied]" block looking successful.
		m.appendBlock(blockError, "✗ permission not delivered: "+msg.err.Error())
		return m, nil

	case shellResultMsg:
		m.appendShellBlock(msg.command, msg.res, msg.err)
		return m, nil
	}

	// Mouse wheel scrolls the transcript body.
	if mw, ok := msg.(tea.MouseWheelMsg); ok {
		switch mw.Button {
		case tea.MouseWheelUp:
			m.body.ScrollBy(-3)
		case tea.MouseWheelDown:
			m.body.ScrollBy(3)
		}
	}
	return m, nil
}

// scrollbarDragTo handles a left-button press/drag at coordinates relative to
// the transcript content's top-left (the caller subtracts the modal's inner
// origin). If it lands on the scrollbar column over the body it jumps the scroll
// position proportionally (drag-to-position) and returns true; otherwise it
// returns false so the event falls through to normal handling. It is the inverse
// of kit.Scrollbar's pos math: a row near the top maps to offset 0, near the
// bottom to the max offset.
func (m *TranscriptModel) scrollbarDragTo(relX, relY int) bool {
	// bodyTop = header(1) + divider(1); the scrollbar sits in the rightmost body
	// column (bodyView reserves m.width-1 for content, +1 for the bar).
	const bodyTop = 2
	bodyH := m.body.Height()
	if relX != m.width-1 || relY < bodyTop || relY >= bodyTop+bodyH {
		return false
	}
	maxOffset := m.body.TotalHeight() - bodyH
	if maxOffset <= 0 {
		return true // on the bar, but nothing to scroll
	}
	denom := bodyH - 1
	if denom < 1 {
		denom = 1
	}
	frac := float64(relY-bodyTop) / float64(denom)
	if frac < 0 {
		frac = 0
	} else if frac > 1 {
		frac = 1
	}
	target := int(math.Round(frac * float64(maxOffset)))
	m.body.ScrollBy(target - m.body.Offset())
	return true
}

// View renders the transcript screen (full-screen variant; see modalContent
// for the command-center modal overlay).
func (m *TranscriptModel) View() tea.View {
	if m.width == 0 {
		v := tea.NewView("loading…")
		v.AltScreen = true
		return v
	}
	if m.showHelp {
		overlay := lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			m.helpUI.view(m.width), pageWhitespace())
		v := tea.NewView(overlay)
		v.AltScreen = true
		return v
	}
	return tea.NewView(m.renderTranscript(m.width, m.height))
}

// modalContent renders the transcript sized for the command-center modal. The
// output is normalized to a solid w×h block (fitModal) so short rows don't leave
// the modal layer transparent on the right — otherwise the dark dashboard
// underneath shows through as a long dark line beside the status line (TODO.md).
func (m *TranscriptModel) modalContent(w, h int) string {
	if m.width != w || m.height != h {
		m.width, m.height = w, h
		m.layout()
	}
	return fitModal(m.renderTranscript(w, h), w, h)
}

// fitModal forces s to exactly h lines, each exactly w columns: short lines are
// padded with spaces (opaque cells, so the dashboard layer can't bleed through)
// and any over-wide line is ANSI-aware-truncated. Truncation is a backstop;
// renderInput already sizes itself to avoid overflow.
func fitModal(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i, l := range lines {
		if lipgloss.Width(l) > w {
			l = ansi.Truncate(l, w, "")
		}
		lines[i] = padRight(l, w)
	}
	return strings.Join(lines, "\n")
}

// previewView renders the transcript's history (header + divider + scrollable
// body) with a connect banner where the composer would normally sit. It is used
// during ScreenConnecting (Fix A) so a session's conversation is visible while
// its pod resumes, instead of a blank splash. Read-only: no input box.
func (m *TranscriptModel) previewView(w, h int, banner string) string {
	m.width, m.height = w, h
	bannerH := lipgloss.Height(banner)
	// header(1) + divider(1) + body + blank(1) + banner.
	bodyH := h - 3 - bannerH
	if bodyH < 1 {
		bodyH = 1
	}
	m.body.SetSize(max(1, w-1), bodyH)
	m.syncBody()
	m.body.GotoBottom()
	parts := []string{
		m.renderHeader(),
		styleDivider.Render(strings.Repeat("─", w)),
		m.bodyView(),
		"",
		banner,
	}
	return strings.Join(parts, "\n")
}

// renderTranscript builds the actual transcript string for the current size.
func (m *TranscriptModel) renderTranscript(w, h int) string {
	parts := []string{m.renderHeader(), styleDivider.Render(strings.Repeat("─", w)), m.bodyView()}
	if m.pending != nil {
		// Rebuild the box at render time so the permission-appear border fade
		// (§C.3) reads the live elapsed time rather than the cached layout build.
		if m.pending.isPlan {
			parts = append(parts, m.permBox)
		} else {
			parts = append(parts, m.buildPermissionBox(m.width))
		}
	}
	if m.palette != "" {
		parts = append(parts, m.palette)
	}
	// The search bar (T3) sits just above the input when open; without this it
	// was dead code and `/`-search opened with no visible affordance.
	if m.search.open {
		parts = append(parts, m.renderSearchBar(w))
	}
	// A blank line sets the input apart from the transcript so the composer has
	// room to breathe instead of butting against the last message (roominess).
	parts = append(parts, "", m.renderInput(), m.renderStatusLine())
	return strings.Join(parts, "\n")
}

// --------------------------------------------------------------------------
// Layout
// --------------------------------------------------------------------------

// layout (re)sizes the list body and input and reconciles items. It is called
// on resize and whenever the permission box appears/disappears or the diff view
// toggles, since those change the available body height.
func (m *TranscriptModel) layout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}

	permH := 0
	m.permBox = ""
	if m.pending != nil {
		if m.pending.isPlan {
			m.permBox = m.renderPlanCard(m.width)
		} else {
			m.permBox = m.buildPermissionBox(m.width)
		}
		permH = strings.Count(m.permBox, "\n") + 1
	}

	palH := 0
	m.palette = ""
	if m.paletteOpen() {
		m.palette = m.renderPalette(m.width)
		palH = strings.Count(m.palette, "\n") + 1
	}

	// The search bar consumes one row above the input when open (T3).
	searchH := 0
	if m.search.open {
		searchH = 1
	}

	// Size the composer first so inputRows() (which wraps on this width) is
	// accurate, then reserve the body height around the boxed input. Box inner
	// width = full width - scrollbar(1) - border(2) - padding(2).
	m.input.SetWidth(max(10, m.width-5))
	// header(1) + divider(1) + input gap(1) + box(border 2 + rows) + hint row(1).
	inputH := m.inputRows() + 3
	vpH := m.height - 3 - inputH - statusLineRows - permH - palH - searchH
	if vpH < 1 {
		vpH = 1
	}
	// Reserve one column on the right for the transcript scrollbar (§D).
	m.body.SetSize(max(1, m.width-1), vpH)
	m.syncBody()
}

// maxInputRows caps how tall the composer grows before it scrolls internally.
const maxInputRows = 6

// inputRows is the composer's current display height (1..maxInputRows), driving
// both the box render and the body-height reservation in layout.
func (m *TranscriptModel) inputRows() int {
	n := m.input.LineCount()
	if n < 1 {
		n = 1
	}
	if n > maxInputRows {
		n = maxInputRows
	}
	return n
}

// renderUnreadDivider draws a subtle "new since you left" line.
func (m *TranscriptModel) renderUnreadDivider() string {
	w := m.width - 2
	if w < 10 {
		w = 10
	}
	left := "─ new ─"
	line := left + strings.Repeat("─", w-lipgloss.Width(left))
	return lipgloss.NewStyle().Foreground(theme.TextMuted).Render(line)
}

// A2.1 (Calm) role gutter. gutterInset is the left inset a guttered message (or
// a place-indented subordinate block) occupies: 1 pad column + the 2-cell role
// bar "▌ ". Wrapping blocks render that much narrower so the bar + text still fit
// the body width.
const gutterInset = 3

// gutterPrefix puts a slim role-colored bar (Charple for the assistant, Guac for
// the user) down the left of every line of a message block — replacing the old
// "❯ " prefix. The bar is its own styled span so it never bleeds into the line.
func gutterPrefix(s string, bar color.Color) string {
	b := lipgloss.NewStyle().Foreground(bar).Render("▌ ")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = " " + b + l
	}
	return strings.Join(lines, "\n")
}

// placeIndent indents a subordinate block (tool card, footer, notice, reasoning)
// by gutterInset spaces so it aligns under the message column rather than under
// the role bar.
func placeIndent(s string) string {
	pad := strings.Repeat(" ", gutterInset)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}

// renderBlock renders a transcript block with its Calm chrome: user/assistant
// blocks get the role gutter bar; every other (subordinate) kind is indented to
// the message column. The raw content is produced by renderBlockRaw; an empty
// raw render stays empty (no stray bar/indent on a blank line).
func (m *TranscriptModel) renderBlock(b tblock) string {
	raw := m.renderBlockRaw(b)
	if raw == "" {
		return ""
	}
	switch b.kind {
	case blockUser:
		return gutterPrefix(raw, theme.Guac)
	case blockAssistant:
		return gutterPrefix(raw, theme.Charple)
	default:
		return placeIndent(raw)
	}
}

// assistantWrapWidth is the markdown word-wrap width for an assistant message
// body. It MUST be identical for the live streaming tail and the finalized block
// (T1): the tail wraps at this width while streaming, and if the finalized block
// wrapped even one column narrower, the extra wrapped lines would push the
// content up and the view would lurch off the bottom at message.completed. It
// reserves the gutter chrome (gutterInset) plus one column for the scrollbar.
func (m *TranscriptModel) assistantWrapWidth() int {
	w := m.width - 2 - gutterInset
	if w < 20 {
		w = 20
	}
	return w
}

// renderBlockRaw renders a block's bare content (no gutter/indent). Wrapping
// kinds reserve gutterInset columns so the chrome added by renderBlock fits.
func (m *TranscriptModel) renderBlockRaw(b tblock) string {
	switch b.kind {
	case blockUser:
		return styleTUser.Render(b.text)
	case blockAssistant:
		wrap := m.assistantWrapWidth()
		// Route assistant blocks through chat.AssistantItem + the pooled
		// glamour renderer (chat.MarkdownRenderer), replacing the per-layout
		// m.md allocation. RawRender emits no focus prefix, preserving
		// byte-for-byte parity with the former m.md.Render + TrimRight path.
		ai := chat.NewAssistantItem(&chat.AssistantMessage{Content: b.text, Finished: true})
		ai.SetRenderContentMD(func(text string, width int) string {
			r := chat.MarkdownRenderer(width)
			if r == nil {
				return styleTAssistant.Render(text)
			}
			out, err := r.Render(text)
			if err != nil {
				return styleTAssistant.Render(text)
			}
			return strings.TrimRight(out, "\n")
		})
		return ai.RawRender(wrap)
	case blockToolCard:
		if b.tool != nil {
			w := m.width - 2 - gutterInset
			if w < 10 {
				w = 10
			}
			return m.renderToolCard(b.tool, w)
		}
		return b.text
	case blockTool:
		return styleTTool.Render(b.text)
	case blockToolErr, blockError:
		return styleTError.Render(b.text)
	case blockInfo:
		return styleTInfo.Render(b.text)
	case blockShell, blockFooter:
		// Pre-styled block; render verbatim.
		return b.text
	case blockSubagent:
		if b.sub != nil {
			w := m.width - 2 - gutterInset
			if w < 10 {
				w = 10
			}
			return m.renderSubagentCard(b.sub, w)
		}
	case blockReasoning:
		// Render reasoning/thinking text as a muted "Thought:" prefix box
		// (chat-rendering §4.4). Shown in a compact single-line summary when
		// short, or multi-line for longer reasoning.
		lines := strings.Count(b.text, "\n") + 1
		label := lipgloss.NewStyle().Foreground(theme.TextMuted).Bold(true).Render("💭 Thought")
		if lines <= 1 {
			return label + lipgloss.NewStyle().Foreground(theme.TextMuted).Render(": "+b.text)
		}
		summary := firstLine(b.text)
		return label + lipgloss.NewStyle().Foreground(theme.TextMuted).
			Render(fmt.Sprintf(" (%d lines): %s…", lines, truncate(summary, 40)))
	}
	return b.text
}
func (m *TranscriptModel) appendBlock(kind tblockKind, text string) {
	m.blocks = append(m.blocks, tblock{kind: kind, text: text})
	m.syncBody()
}

// renderTodos formats a todo.updated checklist as one line per item with a
// status glyph (completed ✓, in_progress ▸, pending ○). For in-progress items
// the present-tense ActiveForm is preferred when set.
func renderTodos(todos []session.TodoItem) string {
	if len(todos) == 0 {
		return "📋 todo list cleared"
	}
	var b strings.Builder
	b.WriteString("📋 todo list")
	for _, t := range todos {
		var glyph string
		switch t.Status {
		case "completed":
			glyph = "✓"
		case "in_progress":
			glyph = "▸"
		default: // pending and any unknown status
			glyph = "○"
		}
		text := t.Content
		if t.Status == "in_progress" && t.ActiveForm != "" {
			text = t.ActiveForm
		}
		b.WriteString("\n  " + glyph + " " + text)
	}
	return b.String()
}

// startToolCard appends a running tool card and queues it for result matching.
func (m *TranscriptModel) startToolCard(tool, arg string) {
	m.blocks = append(m.blocks, tblock{
		kind: blockToolCard,
		tool: &toolCard{tool: tool, arg: arg, status: toolRunning},
	})
	m.pendingTools = append(m.pendingTools, len(m.blocks)-1)
	m.syncBody()
}

// startOrUpdateToolCard handles a tool.started for a flat (non-subagent) tool.
// The SDK emits tool.started twice for one tool under includePartialMessages —
// once from the streaming content_block_start (empty input) and once from the
// full assistant message (complete input) — both with the same toolUseId. The
// subagent path dedupes this by toolUseId; this does the same for flat tools so
// they render one card, updating the arg from the fuller (later) payload rather
// than appending a duplicate that would sit stuck "running" (C2).
func (m *TranscriptModel) startOrUpdateToolCard(p session.ToolPayload) {
	arg := toolArg(p.Tool, p.Input)
	if p.ToolUseID != "" {
		if idx, ok := m.flatTools[p.ToolUseID]; ok && idx >= 0 && idx < len(m.blocks) && m.blocks[idx].tool != nil {
			if arg != "" {
				m.blocks[idx].tool.arg = arg
			}
			m.syncBody()
			return
		}
	}
	m.startToolCard(p.Tool, arg)
	if p.ToolUseID != "" {
		if m.flatTools == nil {
			m.flatTools = map[string]int{}
		}
		m.flatTools[p.ToolUseID] = len(m.blocks) - 1
	}
}

// finishToolCard resolves the oldest pending tool card with a result. tool.
// completed/failed payloads carry no tool name, so cards are matched in start
// order; toolName (if present, e.g. on failure) is a label fallback.
func (m *TranscriptModel) finishToolCard(status toolStatus, summary, toolName string) {
	// Remap any ANSI the tool emitted in its result onto the theme palette (§A.2).
	summary = kit.RemapANSI(summary)
	if len(m.pendingTools) > 0 {
		idx := m.pendingTools[0]
		m.pendingTools = m.pendingTools[1:]
		if idx >= 0 && idx < len(m.blocks) && m.blocks[idx].tool != nil {
			m.blocks[idx].tool.status = status
			m.blocks[idx].tool.summary = summary
			m.syncBody()
			return
		}
	}
	// Orphan result (no matching start): render a standalone finished card.
	m.blocks = append(m.blocks, tblock{
		kind: blockToolCard,
		tool: &toolCard{tool: toolName, status: status, summary: summary},
	})
	m.syncBody()
}

// renderToolCard formats a tool card as a single compact line:
//
//	⏵ Read   path/to/file.go
//	✓ Bash   npm test            · exit 0
//	✗ Edit   main.go             · old_string not found
func (m *TranscriptModel) renderToolCard(c *toolCard, width int) string {
	var icon string
	var iconColor = theme.Malibu
	switch c.status {
	case toolRunning:
		// Static marker (not the spinner) so running cards don't force a full
		// transcript re-render on every work tick — only the prompt-line
		// indicator animates.
		icon = "⏵"
		iconColor = theme.Malibu
	case toolOK:
		icon = "✓"
		iconColor = theme.Guac
	case toolErr:
		icon = "✗"
		iconColor = theme.Coral
	}
	// A2.4 (Calm): mute the tool card — name in TextSecondary (not bold Malibu)
	// and arg in TextMuted; only the status icon keeps its color. Quiets the
	// densest, most-repeated transcript element without losing the at-a-glance
	// pass/fail/running signal.
	iconR := lipgloss.NewStyle().Foreground(iconColor).Render(icon)
	name := lipgloss.NewStyle().Foreground(theme.TextSecondary).Render(c.tool)

	line := iconR + " " + name
	if c.arg != "" {
		line += "  " + lipgloss.NewStyle().Foreground(theme.TextMuted).Render(truncate(c.arg, max(8, width/2)))
	}
	if c.summary != "" {
		sumColor := theme.TextMuted
		if c.status == toolErr {
			sumColor = theme.Coral
		}
		line += lipgloss.NewStyle().Foreground(sumColor).Render("  · " + truncate(c.summary, max(8, width/3)))
	}
	return line
}

// toolArg extracts the most informative single argument from a tool's input
// for the card label: a file path, command, pattern, or url.
func toolArg(tool string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	get := func(keys ...string) string {
		var raw map[string]any
		if json.Unmarshal(input, &raw) != nil {
			return ""
		}
		for _, k := range keys {
			if v, ok := raw[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	switch tool {
	case "Read", "Edit", "Write", "MultiEdit", "NotebookEdit":
		if p := get("file_path", "notebook_path", "path"); p != "" {
			return shortenPath(p)
		}
	case "Bash":
		return collapseSpaces(get("command"))
	case "Grep":
		return get("pattern")
	case "Glob":
		return get("pattern")
	case "WebFetch":
		return get("url")
	case "WebSearch":
		return get("query")
	}
	// Fall back to a path-ish or query-ish field if present.
	if p := get("file_path", "path", "command", "pattern", "url", "query"); p != "" {
		return collapseSpaces(p)
	}
	return ""
}

// toolSummary condenses a tool's output into a short result note.
func toolSummary(output string) string {
	if output == "" {
		return ""
	}
	n := strings.Count(output, "\n")
	if strings.TrimRight(output, "\n") != output {
		n--
	}
	if n >= 1 {
		return formatInt(n+1) + " lines"
	}
	return collapseSpaces(firstLine(output))
}

// shortenPath trims a long absolute path to its last two segments.
func shortenPath(p string) string {
	parts := strings.Split(strings.TrimRight(p, "/"), "/")
	if len(parts) <= 2 {
		return p
	}
	return ".../" + strings.Join(parts[len(parts)-2:], "/")
}

// collapseSpaces flattens runs of whitespace (incl. newlines) into single
// spaces so a multi-line command renders on one card line.
func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// --------------------------------------------------------------------------
// Rendering helpers
// --------------------------------------------------------------------------

// chatStatusLabel maps a session status to a single-session, action-oriented
// label for the chat header (T12). The dashboard's SessionStatus.String() names
// the internal state ("needs-input" for a finished turn, "waiting" for a pending
// permission), which a user reads backwards — "needs-input" looks like the agent
// is blocked on you when it actually means done/ready, and "waiting" is the one
// that truly needs you. These labels say what's true from your seat.
func chatStatusLabel(s SessionStatus) string {
	switch s {
	case StatusBusy:
		return "working"
	case StatusWaiting:
		return "awaiting approval"
	case StatusNeedsInput:
		return "ready for input"
	case StatusIdle:
		return "idle"
	case StatusSuspended:
		return "suspended"
	case StatusFailed:
		return "failed"
	default:
		return s.String()
	}
}

func (m *TranscriptModel) renderHeader() string {
	left := styleDetailTitle.Render(m.title)

	var right string
	if m.reconnectGaveUp {
		right = styleTError.Render("session gone")
	} else if m.reconnecting {
		// Show the live connect stage (FU1) — "reconnecting — Starting pod" — so a
		// slow cold-pod resume reads as real progress, falling back to a plain
		// label until the first stage arrives. Elapsed time is appended (Fix D).
		label := "reconnecting…"
		if m.reconnectStageKnown {
			label = "reconnecting — " + connectStageLabel(m.reconnectStage)
			if m.reconnectDetail != "" {
				label += " " + m.reconnectDetail
			}
		}
		if !m.reconnectStartedAt.IsZero() {
			if el := nowFunc().Sub(m.reconnectStartedAt); el >= time.Second {
				label += fmt.Sprintf(" (%s)", roundDur(el))
			}
		}
		right = styleTError.Render(label)
	} else {
		glyph := glyphStyle(m.status).Render(m.status.Glyph() + " " + chatStatusLabel(m.status))
		meta := styleTInfo.Render(m.agent + " · " + filepath.Base(m.projectPath))
		right = meta + "  " + glyph
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *TranscriptModel) renderInput() string {
	// The transcript opens in NORMAL (vim) mode with the prompt blurred, which
	// isn't discoverable — a new user doesn't know to press i to type (T13). Spell
	// it out in the placeholder and the hint, in plain language (not "insert").
	if m.imode == modeInsert {
		m.input.Placeholder = "type a message…"
	} else {
		m.input.Placeholder = "press i to type a message…"
	}

	// The composer sits in a rounded box that spans the body width (one column
	// reserved for the scrollbar gutter). Its border brightens to Charple when
	// you're typing (INSERT) and stays quiet otherwise, so the box itself signals
	// focus instead of a separate badge.
	boxW := max(20, m.width-1)
	m.input.SetWidth(boxW - 4) // border(2) + padding(2)
	m.input.SetHeight(m.inputRows())
	borderColor := theme.BorderMedium
	if m.imode == modeInsert {
		borderColor = theme.Charple
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Width(boxW - 2). // content width; the border adds the other 2 columns
		Render(m.input.View())

	// A thin row under the box: the vim-mode badge on the left (only when modal
	// editing is on), and the live working/loading indicator (or key hints)
	// right-aligned.
	var right string
	switch {
	case m.vimEnabled && m.imode == modeNormal:
		right = kit.KbdRow([2]string{"i", "type"}, [2]string{"q", "detach"})
	case m.vimEnabled:
		right = kit.KbdRow([2]string{"↵", "send"}, [2]string{"⇧↵", "newline"})
	default:
		// Vim off: the prompt is always focused, so surface how to leave (esc when
		// idle, or ctrl+]) instead of the modal "i to type" hint.
		right = kit.KbdRow([2]string{"↵", "send"}, [2]string{"esc", "detach"})
	}
	if m.replaying {
		right = m.loadingStatus()
	} else if m.turnActive {
		right = m.workingStatus()
	}
	badge := ""
	if m.vimEnabled {
		badge = m.modeBadge()
	}
	gap := m.width - lipgloss.Width(badge) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	hint := badge + strings.Repeat(" ", gap) + right

	return box + "\n" + hint
}

// buildPermissionBox renders the inline gold-bordered permission prompt with a
// +N −N diff stat and, when toggled, an expandable line-by-line diff.
func (m *TranscriptModel) buildPermissionBox(width int) string {
	p := m.pending

	head := lipgloss.NewStyle().Foreground(theme.OnGold).Bold(true).Render(theme.GlyphWaiting + " " + p.tool)
	if p.adds > 0 || p.dels > 0 {
		add := lipgloss.NewStyle().Foreground(theme.Guac).Render("+" + formatInt(p.adds))
		del := lipgloss.NewStyle().Foreground(theme.Coral).Render("−" + formatInt(p.dels))
		head += "   " + add + " " + del
	}
	hint := kit.KbdRow([2]string{"a", "approve"}, [2]string{"d", "deny"}, [2]string{"↵", "view diff"})

	lines := []string{head, hint}
	if m.showDiff && len(p.diffLines) > 0 {
		const maxDiff = 16
		shown := condenseDiff(p.diffLines, maxDiff)
		for _, l := range shown {
			var st lipgloss.Style
			switch {
			case strings.HasPrefix(l, "+"):
				st = lipgloss.NewStyle().Foreground(theme.Guac)
			case strings.HasPrefix(l, "−"):
				st = lipgloss.NewStyle().Foreground(theme.Coral)
			case strings.HasPrefix(l, "…"):
				st = lipgloss.NewStyle().Foreground(theme.TextDim)
			default: // context (" " prefix)
				st = lipgloss.NewStyle().Foreground(theme.TextMuted)
			}
			lines = append(lines, st.Render(truncate(l, max(4, width-6))))
		}
	}

	boxW := width - 2
	if boxW < 10 {
		boxW = 10
	}
	// Permission-appear: fade the gold border in from dim over the appear window
	// (§C.3), softening the mid-stream interruption.
	border := anim.LerpColor(theme.TextDim, theme.Gold, permissionAppear(p.since))
	// D2: framed by the shared kit panel — same rounded border, 0×1 padding, and
	// fixed width as before, with the animated border color passed through.
	return kit.Card(kit.CardOpts{
		Content:     strings.Join(lines, "\n"),
		BorderColor: border,
		PadV:        0,
		PadH:        1,
		Width:       boxW,
	})
}

// renderPlanCard renders the gold ExitPlanMode plan card (slice 1c): the plan
// text plus three actions — reject / approve-stay / approve-and-switch. It is
// deliberately distinct from the permission box so plan review reads as its own
// surface.
func (m *TranscriptModel) renderPlanCard(width int) string {
	boxW := width - 2
	if boxW < 20 {
		boxW = 20
	}
	inner := boxW - 4 // account for border + horizontal padding

	lines := []string{lipgloss.NewStyle().Foreground(theme.OnGold).Bold(true).Render("◈ Plan ready for review"), ""}

	body := strings.TrimSpace(m.pending.plan)
	if body == "" {
		body = "(the agent proposed a plan)"
	}
	const maxPlanLines = 18
	bodyStyle := lipgloss.NewStyle().Foreground(theme.TextBody)
	var wrapped []string
	for _, raw := range strings.Split(body, "\n") {
		wrapped = append(wrapped, wrapPlain(raw, inner)...)
	}
	if len(wrapped) > maxPlanLines {
		wrapped = append(wrapped[:maxPlanLines], "…")
	}
	for _, wl := range wrapped {
		lines = append(lines, bodyStyle.Render(wl))
	}

	lines = append(lines, "",
		kit.KbdRow([2]string{"r", "reject"}, [2]string{"a", "approve · stay in plan"}, [2]string{"↵", "approve & build →"}))

	// D2: framed by the shared kit panel (gold border, 0×1 padding, fixed width).
	return kit.Card(kit.CardOpts{
		Content:     strings.Join(lines, "\n"),
		BorderColor: theme.Gold,
		PadV:        0,
		PadH:        1,
		Width:       boxW,
	})
}

// wrapPlain word-wraps s to width columns (collapsing intra-line whitespace),
// returning at least one line so blank source lines survive as paragraph breaks.
func wrapPlain(s string, width int) []string {
	if width < 4 {
		width = 4
	}
	var lines []string
	var cur string
	for _, word := range strings.Fields(s) {
		switch {
		case cur == "":
			cur = word
		case lipgloss.Width(cur)+1+lipgloss.Width(word) <= width:
			cur += " " + word
		default:
			lines = append(lines, cur)
			cur = word
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// --------------------------------------------------------------------------
// Key handling
// --------------------------------------------------------------------------

// Permission grace gate (chat-rendering §2.6): an answer key resolves the inline
// permission box only once input has been quiet for permissionGraceQuiet since
// both the box appeared and the previous keystroke — so a keystroke already in
// flight when the box pops can't auto-approve/deny it. A hard cap makes the box
// answerable after permissionGraceCap regardless, so a held/repeating key can't
// lock it out forever.
const (
	permissionGraceQuiet = 250 * time.Millisecond
	permissionGraceCap   = 1500 * time.Millisecond
)

// permissionAnswerable reports whether the pending permission may be resolved by
// the current keystroke. prevKeyAt is the time of the previous key event.
func (m *TranscriptModel) permissionAnswerable(prevKeyAt time.Time) bool {
	if m.pending == nil {
		return false
	}
	now := nowFunc()
	if now.Sub(m.pending.since) >= permissionGraceCap {
		return true
	}
	quietSince := m.pending.since
	if prevKeyAt.After(quietSince) {
		quietSince = prevKeyAt
	}
	return now.Sub(quietSince) >= permissionGraceQuiet
}

func (m *TranscriptModel) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	// Track inter-key timing for the permission grace gate (chat-rendering §2.6).
	prevKeyAt := m.lastKeyAt
	m.lastKeyAt = nowFunc()
	// Grouped help overlay: ↑/↓ + space drive it; any other key closes it.
	if m.showHelp {
		if m.helpUI.handleKey(key) {
			return m, nil
		}
		m.showHelp = false
		return m, nil
	}
	// `?` opens the help when the prompt is empty (otherwise it types).
	if key == "?" && m.input.Value() == "" {
		m.openHelp()
		return m, nil
	}
	// esc, in priority order: close an open overlay; steer the running turn with
	// a queued prompt (interrupt + inject); interrupt a running turn outright;
	// leave INSERT for NORMAL. When none apply the App intercepts esc as a detach
	// before delegation (see escapeConsumes); ctrl+] / ctrl+4 always detach there.
	if key == "esc" {
		if m.search.open {
			cmd, _ := m.searchKey(msg)
			return m, cmd
		}
		if m.paletteOpen() {
			cmd, _ := m.paletteKey(msg)
			return m, cmd
		}
		if m.queuedPrompt != "" {
			return m, m.queueSteer()
		}
		if m.turnActive {
			return m, m.interruptTurn()
		}
		if m.vimEnabled && m.imode == modeInsert {
			m.enterNormal()
			return m, nil
		}
		// Vim off: a bare esc has no local meaning here. escapeConsumes already
		// returned false for this case, so the App intercepted esc as a detach and
		// this handler isn't reached — the return is just a safe fallthrough.
		return m, nil
	}
	if key == "ctrl+]" || key == "ctrl+4" {
		if m.queuedPrompt != "" {
			return m, m.queueSteer()
		}
		return m, nil
	}
	// space on an empty prompt collapses/expands all subagent cards.
	if key == "space" && m.input.Value() == "" && m.toggleSubagents() {
		return m, nil
	}
	// shift+tab cycles the permission mode (per-attach; reflected in the status
	// line's mode row and applied to the next turn's StartTurn).
	if key == "shift+tab" {
		m.mode = m.mode.next()
		return m, nil
	}

	// ctrl+f opens in-transcript search.
	if key == "ctrl+f" {
		m.openSearch()
		return m, nil
	}

	// ctrl+c collapses/uncollapses all tool/subagent cards.
	if key == "ctrl+c" {
		m.toggleSubagents()
		return m, nil
	}

	if m.search.open {
		cmd, _ := m.searchKey(msg)
		return m, cmd
	}

	if m.pending != nil {
		// Grace gate: a key in flight when the box popped can't auto-answer it.
		answerable := m.permissionAnswerable(prevKeyAt)
		if m.pending.isPlan {
			switch key {
			case "r":
				// reject: keep plan mode, deny the plan.
				if !answerable {
					return m, nil
				}
				return m, m.resolvePermission(false)
			case "a":
				// approve, stay in plan mode.
				if !answerable {
					return m, nil
				}
				return m, m.resolvePermission(true)
			case "enter":
				// approve & switch to accept-edits for subsequent turns.
				if !answerable {
					return m, nil
				}
				m.mode = modeAcceptEdits
				return m, m.resolvePermission(true)
			}
			if m.scrollKey(key) {
				return m, nil
			}
			return m, nil
		}
		switch key {
		case "a":
			if !answerable {
				return m, nil
			}
			return m, m.resolvePermission(true)
		case "d":
			if !answerable {
				return m, nil
			}
			return m, m.resolvePermission(false)
		case "enter":
			m.showDiff = !m.showDiff
			m.layout()
			return m, nil
		}
		if m.scrollKey(key) {
			return m, nil
		}
		return m, nil
	}

	// Slash palette: when the prompt starts with "/", keys drive the palette.
	if m.paletteOpen() {
		cmd, _ := m.paletteKey(msg)
		return m, cmd
	}

	// NORMAL mode (vim modal editing on) owns the keyboard once the overlays and
	// permission prompts above have had their turn: i/a enter INSERT, j/k/g/G
	// scroll, / searches, q detaches, and every other key is swallowed so the
	// blurred prompt never collects stray letters. With vim off imode is pinned
	// to INSERT, so this never engages and keys flow to the prompt.
	if m.vimEnabled && m.imode == modeNormal {
		cmd, handled := m.normalKey(key, msg)
		if handled {
			return m, cmd
		}
	}

	// $EDITOR composition: ctrl+o opens the editor with the current prompt.
	if key == "ctrl+o" {
		return m, m.openEditorPrompt()
	}

	// Multi-line composition: shift/alt+enter inserts a newline directly into
	// the input so multi-line prompts can be edited without leaving the chat.
	if key == "shift+enter" || key == "alt+enter" {
		m.input.SetValue(m.input.Value() + "\n")
		m.input.CursorEnd()
		m.layout() // the box grew a row — re-reserve body height
		return m, nil
	}

	if key == "enter" {
		// `!cmd` runs a one-shot shell; otherwise send the prompt as a turn.
		if val := strings.TrimSpace(m.input.Value()); strings.HasPrefix(val, "!") {
			cmd := m.runShell(strings.TrimPrefix(val, "!"))
			m.input.Reset()
			return m, cmd
		}
		return m, m.submit()
	}
	if m.scrollKey(key) {
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	// Typing the first "/" opens the palette; relayout so it appears immediately.
	if m.paletteOpen() {
		m.cmdSel = 0
		m.layout()
	}
	return m, cmd
}

// scrollKey moves the viewport for navigation keys, returning true if handled.
func (m *TranscriptModel) scrollKey(key string) bool {
	page := max(1, m.body.Height())
	switch key {
	case "pgup":
		m.body.ScrollBy(-page)
	case "pgdown", "pgdn":
		m.body.ScrollBy(page)
	case "ctrl+u":
		m.body.ScrollBy(-max(1, page/2))
	case "ctrl+d":
		m.body.ScrollBy(max(1, page/2))
	case "up":
		m.body.ScrollBy(-1)
	case "down":
		m.body.ScrollBy(1)
	case "home":
		m.body.GotoTop()
	case "end":
		m.body.GotoBottom()
	default:
		return false
	}
	return true
}

// submit sends the current prompt input as a new turn. If a turn is already
// running, the prompt is queued instead and sent when the turn completes.
func (m *TranscriptModel) submit() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return nil
	}
	if m.turnActive {
		m.queuedPrompt = text
		m.input.Reset()
		return nil
	}
	m.input.Reset()
	return m.submitText(text)
}

// queueSteer steers the active turn with the queued prompt: interrupt now, and
// let the EventTurnInterrupted handler submit the queued prompt once the runner
// has torn the turn down. It must NOT submit concurrently — a new turn POSTed
// while the old one is still active is rejected with 409 (R4 single-active-turn
// gate). Keeping queuedPrompt set defers the submit to interrupt-confirmation,
// sequencing it correctly. (If the interrupt is somehow lost, the queued prompt
// still flushes when the turn finishes naturally — EventTurnCompleted.)
func (m *TranscriptModel) queueSteer() tea.Cmd {
	if m.queuedPrompt == "" {
		return nil
	}
	if !m.turnActive {
		// No turn to interrupt (it already ended) — just send the queued prompt.
		text := m.queuedPrompt
		m.queuedPrompt = ""
		return m.submitText(text)
	}
	return m.interruptTurn()
}

// interruptTurn cancels the active turn. A bare esc maps here while a turn runs;
// the runner answers with EventTurnInterrupted, which clears turnActive and
// appends the "[interrupted]" marker (and flushes any queued steer prompt). The
// turn id targets this exact turn; when it is still empty (esc before
// turn.started landed) the runner falls back to its sole active turn.
func (m *TranscriptModel) interruptTurn() tea.Cmd {
	if !m.turnActive {
		return nil
	}
	ref := session.TurnRef{Session: m.ref.ID, Turn: m.activeTurnID}
	return func() tea.Msg {
		if err := m.client.InterruptTurn(context.Background(), m.ref, ref); err != nil {
			return interruptFailedMsg{err: err}
		}
		return nil
	}
}

// interruptFailedMsg surfaces an interrupt request failure so a future
// regression is visible (a dim notice) instead of a silent no-op.
type interruptFailedMsg struct{ err error }

// submitText starts a turn for the given prompt text, recording the user block
// and entering the busy state. Shared by interactive submit and the auto-
// submitted initial prompt.
func (m *TranscriptModel) submitText(text string) tea.Cmd {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	m.dropTrailingFooter() // A2.2: only the latest turn keeps a footer
	m.appendBlock(blockUser, text)
	m.beginTurn()
	return tea.Batch(startTurnCmd(m.client, m.ref, text, m.mode.apiValue(), m.modelOverride, m.effortOverride), m.maybeStartWorking())
}

// dropTrailingFooter removes the previous turn's footer block when a new turn
// begins, so only the latest turn carries the dim "◇ model · via … · cost"
// footer instead of one accumulating per turn (A2.2). It only removes the footer
// when it is the trailing block — which is the common case, since the footer is
// the last thing appended on turn.completed. This keeps the removal strictly
// index-safe (truncating the tail shifts no earlier flatTools/droppedPartialIdx
// index). If a post-turn block was appended after the footer (e.g. an
// "[interrupted]"/"[reconnected]" notice or an orphan tool result), the footer is
// buried and intentionally left in place rather than risk an interior splice that
// would invalidate those index maps; the result is at most one extra dim footer.
func (m *TranscriptModel) dropTrailingFooter() {
	if n := len(m.blocks); n > 0 && m.blocks[n-1].kind == blockFooter {
		m.blocks = m.blocks[:n-1]
		m.syncBody()
	}
}

// beginTurn enters the busy state for a freshly started turn and resets the
// live working indicator (elapsed clock + token/cost counters).
func (m *TranscriptModel) beginTurn() {
	// Replay/streamed turns reach here without going through submitText, so drop
	// the prior footer here too (no-op in the interactive path, where the new
	// user block is already the trailing block).
	m.dropTrailingFooter()
	m.status = StatusBusy
	m.turnActive = true
	// Clear the previous turn's id; turn.started repopulates it. Until then an
	// interrupt relies on the runner's sole-active-turn fallback.
	m.activeTurnID = ""
	m.turnStart = time.Now()
	m.inTok, m.outTok, m.costUSD = 0, 0, 0
}

// workTickMsg drives the working-indicator clock/spinner while a turn runs.
type workTickMsg struct{}

// workTickInterval is the refresh cadence of the working indicator.
const workTickInterval = 150 * time.Millisecond

func workTickCmd() tea.Cmd {
	return tea.Tick(workTickInterval, func(time.Time) tea.Msg { return workTickMsg{} })
}

// maybeStartWorking schedules the work-tick loop if a turn is active and the
// loop is not already running. Returns nil otherwise so no timer runs idle.
func (m *TranscriptModel) maybeStartWorking() tea.Cmd {
	// Don't animate "working" while replaying history (Workstream C): a replayed
	// turn.started must not drive the live spinner. Once the boundary flips
	// replaying false, the next call starts the loop if the turn is still active.
	if !m.working && m.turnActive && !m.replaying {
		m.working = true
		return workTickCmd()
	}
	return nil
}

// loadingStatus renders the prompt-line indicator shown while catching up
// historical events after an attach/reconnect (Workstream C): an honest "loading
// transcript…" with the count caught up so far, instead of the live "working…"
// spinner that made replay feel like the model was running (#1).
func (m *TranscriptModel) loadingStatus() string {
	ell := anim.Ellipsis(m.workFrame / spinnerSubRate)
	if anim.ReduceMotion() {
		ell = "…"
	}
	msg := "loading transcript" + ell
	if m.replayedCount > 0 {
		msg += fmt.Sprintf(" %d", m.replayedCount)
	}
	return lipgloss.NewStyle().Foreground(theme.Malibu).Render("⟳ " + msg)
}

// workingStatus renders the live indicator shown on the prompt line during a
// turn: spinner · elapsed · token counts · cost.
func (m *TranscriptModel) workingStatus() string {
	spin := theme.SpinnerFrame(m.workFrame)
	// Animated "working" ellipsis at a slower sub-rate than the spinner (§C.3),
	// collapsed to a static "…" under reduce-motion (§E).
	ell := anim.Ellipsis(m.workFrame / spinnerSubRate)
	if anim.ReduceMotion() {
		ell = "…"
	}
	working := lipgloss.NewStyle().Foreground(theme.Busy).Render("working" + ell)
	out := spin + " " + working + "  " +
		lipgloss.NewStyle().Foreground(theme.TextSecondary).Render(fmtElapsed(time.Since(m.turnStart)))
	if m.inTok > 0 || m.outTok > 0 {
		out += lipgloss.NewStyle().Foreground(theme.TextMuted).
			Render(fmt.Sprintf("  ↑%s ↓%s", kit.FormatTokens(m.inTok), kit.FormatTokens(m.outTok)))
	}
	if m.costUSD > 0 {
		out += lipgloss.NewStyle().Foreground(theme.Guac).Render("  " + kit.FormatCost(m.costUSD))
	}
	return out
}

// turnFooter renders the dim per-turn footer (§D): a diamond, the model, the
// client, elapsed, token in/out, and cost — e.g.
// "◇ Opus 4.8 · via anthropic · 12s · ↑3.1k ↓820 · $0.04". Empty when there is
// nothing meaningful to summarize.
func (m *TranscriptModel) turnFooter() string {
	parts := []string{"◇ " + shortModelName(m.model)}
	if m.agent != "" {
		parts = append(parts, "via "+MarkedClientLabel(m.agent))
	}
	if !m.turnStart.IsZero() {
		parts = append(parts, fmtElapsed(time.Since(m.turnStart)))
	}
	if m.inTok > 0 || m.outTok > 0 {
		parts = append(parts, fmt.Sprintf("↑%s ↓%s", kit.FormatTokens(m.inTok), kit.FormatTokens(m.outTok)))
	}
	if m.costUSD > 0 {
		parts = append(parts, kit.FormatCost(m.costUSD))
	}
	if len(parts) <= 1 && m.model == "" {
		return ""
	}
	return lipgloss.NewStyle().Foreground(theme.TextMuted).Render(strings.Join(parts, " · "))
}

// fmtElapsed renders a duration as a compact clock (e.g. "12s", "1m03s").
func fmtElapsed(d time.Duration) string {
	s := int(d.Seconds())
	if s < 60 {
		return formatInt(s) + "s"
	}
	mn := s / 60
	sec := s % 60
	pad := ""
	if sec < 10 {
		pad = "0"
	}
	return formatInt(mn) + "m" + pad + formatInt(sec) + "s"
}

// compactTokens renders a token count as e.g. "340" or "1.2k".
func compactTokens(n int) string {
	if n < 1000 {
		return formatInt(n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

// resolvePermission answers the pending permission and dispatches the decision.
func (m *TranscriptModel) resolvePermission(allow bool) tea.Cmd {
	if m.pending == nil {
		return nil
	}
	decision := session.PermissionDecision{
		Session:    m.ref.ID,
		Permission: m.pending.id,
		Allow:      allow,
		Scope:      "once",
	}
	label := "denied"
	if allow {
		label = "approved"
	}
	m.appendBlock(blockInfo, "  [permission "+label+"]")
	m.pending = nil
	m.showDiff = false
	if m.turnActive {
		m.status = StatusBusy
	}
	m.layout()

	client, ref := m.client, m.ref
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := client.ResolvePermission(ctx, ref, decision); err != nil {
			return permResolveErrMsg{err: err}
		}
		return nil
	}
}

// --------------------------------------------------------------------------
// Event handling
// --------------------------------------------------------------------------

// handleEvent applies a single runner event to the transcript. It returns a
// follow-up command when the event itself triggers async work (auto-reconnect).
// tailLines returns up to n plain text lines from the end of the transcript
// body, for the dashboard detail-pane preview of a warm session. It seeds the
// model's size for the requested width first (it may have been built in the
// background without a layout).
func (m *TranscriptModel) tailLines(n, width int) []string {
	m.seedSize(width, max(n+4, 8))
	body := m.bodyView()
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	// Drop trailing blank padding lines so the preview hugs real content.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// seedSize applies a terminal size to a model that was built in the background
// (and so never received a WindowSizeMsg). It mirrors the WindowSizeMsg handler
// so the model lays out correctly before its first foreground View.
func (m *TranscriptModel) seedSize(w, h int) {
	m.width, m.height = w, h
	m.layout()
}

// ingest applies a single event to this model from an external (background)
// source — the dashboard's passive stream feeding a warm, non-foreground model.
// It reuses handleEvent (which dedupes on lastSeq) and discards the returned
// Cmd, since a background model is never the active tea screen. Safe to call on
// a model whose own SSE stream has not been started.
func (m *TranscriptModel) ingest(ev session.Event) {
	_ = m.handleEvent(ev)
	// Workstream C: the warm/background feed must mirror events to the cache too.
	// Otherwise a session observed only in the background advances lastSeq without
	// ever caching, leaving a permanent hole that a later cold attach can't
	// backfill (it would resume past the gap and drop that history). maybeCache is
	// idempotent per seq, so the warm→foreground handoff can't double-write.
	m.maybeCache(ev)
}

func (m *TranscriptModel) handleEvent(ev session.Event) tea.Cmd {
	// Workstream C: the replay/live boundary marker. Not a persisted event (no
	// seq) — it only flips us out of replay so a genuinely in-flight turn (whose
	// turnActive survived the catch-up) resumes "working", while a session that
	// merely caught up history settles to idle.
	if ev.Type == session.EventStreamLive {
		m.replaying = false
		return nil
	}
	if ev.Seq > m.lastSeq {
		m.lastSeq = ev.Seq
	}
	if m.replaying {
		m.replayedCount++
		// Watermark boundary: once we've caught up to the seq the dashboard knew
		// about at attach, the catch-up is done — flip to live even if the runner
		// never sends the replay-complete marker. (attachSeq>0 guard so a stream
		// with no known cursor relies solely on the marker.)
		if m.attachSeq > 0 && m.lastSeq >= m.attachSeq {
			m.replaying = false
		}
	}

	var cmd tea.Cmd

	switch ev.Type {
	case session.EventTurnStarted:
		// Only (re)start the clock if we didn't already begin this turn locally
		// (interactive submit sets turnStart); a fresh stream/attach starts here.
		if !m.turnActive {
			m.beginTurn()
		}
		// Capture the runner's turn id so esc can target this exact turn (works on
		// attach/replay too, where StartTurn was never called locally).
		m.activeTurnID = ev.TurnID

	case session.EventSessionStarted:
		var p session.SessionStartedPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Model != "" {
			m.model = p.Model
			m.ctxLimit = models.Limit(p.Model).ContextLimit
			if m.defaultModel == "" {
				// First resolved id is the account/session default; remember it so
				// /model-default can restore the status line to it.
				m.defaultModel = p.Model
			}
		}
		if p.Cwd != "" {
			m.cwd = p.Cwd
		}

	case session.EventWorkspaceStatus:
		var p session.WorkspaceStatusPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.branch = p.Branch
		m.dirty = p.Dirty
		m.ahead = p.Ahead
		m.behind = p.Behind

	case session.EventUsageUpdated:
		var p session.UsagePayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.InputTokens > 0 {
			m.inTok = p.InputTokens
			m.cacheReadTok = p.CacheReadTokens
			m.cacheWriteTok = p.CacheWriteTokens
		}
		if p.OutputTokens > 0 {
			m.outTok = p.OutputTokens
		}
		if p.TotalCostUSD > 0 {
			m.costUSD = p.TotalCostUSD
		}

	case session.EventRateLimitUpdated:
		var p session.RateLimitPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.rlSeen = true
		m.rlAvailable = p.Available
		m.rlSubscription = p.SubscriptionType
		m.rl5hUtil = p.FiveHourUtil
		m.rl7dUtil = p.SevenDayUtil
		m.rl5hReset = parseResetTime(p.FiveHourResetsAt)
		m.rl7dReset = parseResetTime(p.SevenDayResetsAt)
		m.rlOpusSeen = p.SevenDayOpusUtil != nil
		if p.SevenDayOpusUtil != nil {
			m.rlOpusUtil = *p.SevenDayOpusUtil
		}
		m.rlOpusReset = parseResetTime(p.SevenDayOpusResetsAt)
		m.rlSonnetSeen = p.SevenDaySonnetUtil != nil
		if p.SevenDaySonnetUtil != nil {
			m.rlSonnetUtil = *p.SevenDaySonnetUtil
		}
		m.rlSonnetReset = parseResetTime(p.SevenDaySonnetResetsAt)

	case session.EventModelsAvailable:
		var p session.ModelsAvailablePayload
		if json.Unmarshal(ev.Payload, &p) == nil && len(p.Models) > 0 {
			m.availableModels = p.Models
		}

	case session.EventTurnCompleted:
		m.finalizeStreaming()
		// Per-turn footer: a dim model/cost summary so scrollback is self-
		// documenting (§D).
		if f := m.turnFooter(); f != "" {
			m.appendBlock(blockFooter, f)
		}
		m.status = StatusNeedsInput
		m.turnActive = false
		if m.queuedPrompt != "" {
			q := m.queuedPrompt
			m.queuedPrompt = ""
			cmd = m.submitText(q) // capture so the queued turn's POST actually runs
		}

	case session.EventTurnInterrupted:
		m.finalizeStreaming()
		m.status = StatusNeedsInput
		m.turnActive = false
		m.appendBlock(blockInfo, "[interrupted]")
		// A queued prompt here means the interrupt was a steer (queueSteer keeps
		// queuedPrompt set): now that the turn is torn down, submit it as the next
		// turn — sequenced after the interrupt so it can't 409 against the old one.
		if m.queuedPrompt != "" {
			q := m.queuedPrompt
			m.queuedPrompt = ""
			cmd = m.submitText(q)
		}

	case session.EventTurnFailed:
		m.finalizeStreaming()
		m.status = StatusFailed
		m.turnActive = false
		var p session.ErrorPayload
		_ = json.Unmarshal(ev.Payload, &p)
		msg := p.Message
		if strings.TrimSpace(msg) == "" {
			// Defensive: never render a bare "✗" with no reason if a payload
			// omits message (RV: turn.failed had a result-path shape without it).
			msg = "turn failed"
		}
		m.appendBlock(blockError, "✗ "+msg)

	case session.EventMessageStarted:
		// A new message means any partial we committed on an earlier drop now
		// stands on its own — it won't be replaced by this message's completed
		// (RV9), so stop tracking it.
		m.droppedPartialIdx = -1
		m.streaming = true
		m.assistantBuf.Reset()
		m.streamAI = chat.NewAssistantItem(&chat.AssistantMessage{Streaming: true})
		m.streamAI.SetRenderContentMD(func(text string, width int) string {
			r := chat.MarkdownRenderer(width)
			if r == nil {
				return styleTAssistant.Render(text)
			}
			out, err := r.Render(text)
			if err != nil {
				return styleTAssistant.Render(text)
			}
			return strings.TrimRight(out, "\n")
		})
		m.streamAI.SetRendererFactory(func(width int) chat.Renderer {
			return chat.MarkdownRenderer(width)
		})
	case session.EventMessageDelta:
		var p session.MessagePayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.streaming = true
		m.assistantBuf.WriteString(p.Content)
		m.streamDelta()

	case session.EventMessageCompleted:
		var p session.MessagePayload
		_ = json.Unmarshal(ev.Payload, &p)
		text := p.Content
		if text == "" {
			text = m.assistantBuf.String()
		}
		m.streaming = false
		m.assistantBuf.Reset()
		m.streamAI = nil
		switch {
		case m.droppedPartialIdx >= 0 && m.droppedPartialIdx < len(m.blocks) &&
			m.blocks[m.droppedPartialIdx].kind == blockAssistant:
			// RV9: this is the replayed full version of a partial we committed on
			// a mid-message stream drop (B9). Replace that block in place instead
			// of appending a second copy of the same reply.
			if strings.TrimSpace(text) != "" {
				m.blocks[m.droppedPartialIdx].text = text
				// In-place text mutation of an immutable-kind block: force its
				// fingerprint to recompute so the memoized reconcile re-renders it.
				m.markBlockDirty(m.droppedPartialIdx)
			}
			m.droppedPartialIdx = -1
			m.syncBody()
		case strings.TrimSpace(text) != "":
			m.appendBlock(blockAssistant, text)
		default:
			m.syncBody()
		}

	case session.EventToolStarted:
		var p session.ToolPayload
		_ = json.Unmarshal(ev.Payload, &p)
		switch {
		case p.Tool == "Task" || p.AgentName != "":
			m.startSubagent(p)
		case p.ParentToolUseID != "" && m.subagents[p.ParentToolUseID] != nil:
			m.startSubagentChild(p)
		default:
			m.startOrUpdateToolCard(p)
		}

	case session.EventToolCompleted:
		var p session.ToolPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if !m.finishNested(p, toolOK, toolSummary(p.Output)) {
			m.finishToolCard(toolOK, toolSummary(p.Output), p.Tool)
		}

	case session.EventToolFailed:
		var p session.ToolPayload
		_ = json.Unmarshal(ev.Payload, &p)
		// tool.failed carries its reason in `error` (the PreToolUse-block path)
		// OR in `output` (the SDK is_error result path emits no `error`). Fall
		// back to output so a failed card never renders as a bare red "✗" with
		// the reason silently dropped.
		summary := p.Error
		if summary == "" {
			summary = toolSummary(p.Output)
		}
		if !m.finishNested(p, toolErr, summary) {
			m.finishToolCard(toolErr, summary, p.Tool)
		}

	case session.EventPermissionRequested:
		var p session.PermissionPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Tool == "ExitPlanMode" {
			// Plan mode: the agent presents its plan for review. Surface the
			// distinct gold plan card (slice 1c) instead of the permission box.
			var pl struct {
				Plan string `json:"plan"`
			}
			_ = json.Unmarshal(p.Input, &pl)
			m.pending = &transcriptPermission{id: p.PermissionID, tool: p.Tool, isPlan: true, plan: pl.Plan, since: time.Now()}
		} else {
			adds, dels, diffLines := permissionDiffStat(p.Tool, p.Input)
			m.pending = &transcriptPermission{id: p.PermissionID, tool: p.Tool, adds: adds, dels: dels, diffLines: diffLines, since: time.Now()}
		}
		m.status = StatusWaiting
		m.layout()

	case session.EventPermissionResolved:
		m.pending = nil
		m.showDiff = false
		if m.turnActive {
			m.status = StatusBusy
		}
		m.layout()

	case session.EventSessionTerminating:
		var p session.TerminatingPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.terminating = true
		m.turnActive = false
		warn := "⚠ pod is being rescheduled — saving state, will reconnect"
		if p.Reason != "" {
			warn = "⚠ " + p.Reason + " — saving state, will reconnect"
		}
		m.appendBlock(blockInfo, warn)
		// Do NOT reconnect immediately (RV17): the runner is still alive for its
		// grace window and the Sandbox still has the dying pod (replicas=1), so an
		// instant reconnect would port-forward to the terminating pod and flap
		// [reconnected]/[connection lost]. Mark that we're reconnecting and let the
		// stream-end (tStreamEndedMsg, fired when the old pod's SSE actually
		// closes) drive doReconnect — by then the controller has rescheduled a
		// fresh pod, so getPodForSandbox (R7) returns the new one.
		if !m.reconnecting {
			m.reconnecting = true
			m.reconnectStartedAt = nowFunc()
			m.appendBlock(blockInfo, "[auto-reconnecting…]")
		}

	// ---- B8: Previously-dropped events, now handled ----

	case session.EventSessionStatusChanged:
		// Runner reports session status transitions (idle/busy/error).
		var p session.SessionStatusPayload
		_ = json.Unmarshal(ev.Payload, &p)
		switch p.Status {
		case "busy":
			m.status = StatusBusy
		case "idle":
			m.status = StatusNeedsInput
		case "error":
			m.status = StatusFailed
			// M1: surface the reason for an error status (was silently dropped).
			if p.Reason != "" {
				m.appendBlock(blockError, "session error: "+p.Reason)
			}
		}

	case session.EventReasoningStarted:
		// A reasoning/thinking block is beginning. Initialize the buffer.
		m.reasoning = true
		m.reasoningBuf.Reset()

	case session.EventReasoningDelta:
		// Incremental chunk of thinking text.
		if m.reasoning {
			var p session.MessagePayload // same {Content} shape as message.delta
			_ = json.Unmarshal(ev.Payload, &p)
			m.reasoningBuf.WriteString(p.Content)
		}

	case session.EventReasoningCompleted:
		// Flush the thinking block. M3: prefer the payload content — it is the
		// authoritative full text and the only source in the non-streaming case
		// (and even when streaming, the full message re-emits reasoning.started,
		// which resets the delta buffer, so the buffer alone would be empty here).
		var p session.MessagePayload // {Content}, same shape as message.delta
		_ = json.Unmarshal(ev.Payload, &p)
		text := strings.TrimSpace(p.Content)
		if text == "" {
			text = strings.TrimSpace(m.reasoningBuf.String())
		}
		m.reasoning = false
		m.reasoningBuf.Reset()
		if text != "" {
			m.appendBlock(blockReasoning, text)
		}

	case session.EventToolDelta:
		// tool.delta streams the tool's INPUT JSON as the model types it
		// (input_json_delta) — it is NOT tool output. Show it as a live preview
		// on the newest running card so the argument materializes in real time;
		// the finalized tool.started (deduped onto the same card) overwrites arg
		// with the cleanly-parsed value. The event carries no toolUseId, so we
		// target the most-recently-started card.
		var p session.ToolPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.PartialJSON != "" && len(m.pendingTools) > 0 {
			idx := m.pendingTools[len(m.pendingTools)-1]
			if idx >= 0 && idx < len(m.blocks) && m.blocks[idx].tool != nil {
				c := m.blocks[idx].tool
				c.arg = collapseSpaces(c.arg + p.PartialJSON)
				m.syncBody()
			}
		}

	case session.EventTodoUpdated:
		// Todo list changed; render the agent's current checklist so users can
		// follow its plan. Each event carries the full list (it replaces any
		// prior one), so we show it as a compact one-line-per-item block.
		var p session.TodoUpdatedPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.appendBlock(blockInfo, renderTodos(p.Todos))

	case session.EventError:
		var p session.ErrorPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.appendBlock(blockError, "error: "+p.Message)
	}
	return cmd
}

// finalizeStreaming commits any buffered streaming text as a final assistant
// block (covers turns that end without a message.completed) and returns that
// block's index, or -1 if nothing was committed.
func (m *TranscriptModel) finalizeStreaming() int {
	if m.streaming && strings.TrimSpace(m.assistantBuf.String()) != "" {
		m.streaming = false
		text := m.assistantBuf.String()
		m.assistantBuf.Reset()
		m.streamAI = nil
		m.appendBlock(blockAssistant, text)
		return len(m.blocks) - 1
	}
	m.streaming = false
	m.assistantBuf.Reset()
	m.streamAI = nil
	return -1
}

// --------------------------------------------------------------------------
// SSE stream + reconnect (mirrors tui.Model)
// --------------------------------------------------------------------------

// loadCachedTranscript rebuilds the transcript from the host-side cache on a cold
// open (Workstream C), advancing lastSeq to the cached head so the stream resumes
// from there. Guarded to a genuinely cold model (no blocks, no cursor): a warm
// model promoted to the foreground already holds its history, so re-loading would
// duplicate it. The replayed events feed the SAME handleEvent the live stream
// uses (so blocks/state stay identical), but are NOT re-written to the cache.
func (m *TranscriptModel) loadCachedTranscript() {
	if m.cache == nil || len(m.blocks) > 0 || m.lastSeq > 0 {
		return
	}
	events, err := m.cache.LoadEvents(m.ref.ID)
	if err != nil || len(events) == 0 {
		return
	}
	// Replay the cache synchronously (no UI shown yet); startEventStream then sets
	// the replay/loading state from the watermark for the remaining delta.
	//
	// Bulk mode: apply every event to m.blocks WITHOUT reconciling the list per
	// event — a naive replay calls syncBody→reconcileItems once per event, and
	// each reconcile re-fingerprints all prior items (hashing each block's full
	// text) and rebuilds the item set, making the cold load O(N^2). Suppress that,
	// then reconcile exactly once after the loop, so replay is O(N).
	m.bulkReplay = true
	for i := range events {
		_ = m.handleEvent(events[i])
		if events[i].Seq > m.lastCachedSeq {
			m.lastCachedSeq = events[i].Seq // already on disk; don't re-append
		}
	}
	m.bulkReplay = false
	m.syncBody()
}

func (m *TranscriptModel) startEventStream() tea.Cmd {
	// Tear down any prior stream first (e.g. on reconnect) so we don't leak its
	// context/connection (NEW-5).
	if m.streamCancel != nil {
		m.streamCancel()
		m.streamCancel = nil
	}
	// Enter replay ONLY when there is history to catch up to (the dashboard's
	// cursor is ahead of what we've rendered). A fresh session (attachSeq 0) or a
	// warm reattach already caught up never shows "loading transcript…". The state
	// clears at the watermark (handleEvent) or the runner's replay-complete marker,
	// whichever comes first — so it self-clears even against an older runner.
	m.replaying = m.attachSeq > m.lastSeq
	m.replayedCount = 0
	ctx, cancel := context.WithCancel(context.Background())
	events, err := m.client.Events(ctx, m.ref, m.lastSeq)
	if err != nil {
		cancel()
		// The stream never opened (non-200 from /events, or the port-forward
		// died between the connect health-check and here). Route into the same
		// reconnect path a mid-stream drop uses — which shows "[connection lost
		// — reconnecting…]" and retries — instead of returning nil and leaving
		// a connected-looking but inert transcript that receives no events and
		// never recovers.
		return func() tea.Msg { return tStreamEndedMsg{} }
	}
	m.events = events
	m.streamCancel = cancel
	return m.waitForEvent
}

// cancelStream tears down the transcript's live SSE stream. The App must call
// this before releasing the transcript on detach (NEW-5); otherwise the runner
// keeps a second SSE client open until GC / runner-side close, briefly defeating
// the "exactly one SSE client" intent (B2) and the idle-reaper accounting.
func (m *TranscriptModel) cancelStream() {
	if m.streamCancel != nil {
		m.streamCancel()
		m.streamCancel = nil
	}
	m.events = nil
}

// eventBatchMax caps how many buffered events one waitForEvent collapses into a
// single batch, so a relentless stream still yields to the render/key loop
// between batches rather than spinning the drain forever.
const eventBatchMax = 512

// waitForEvent blocks for the next event, then non-blockingly drains any
// already-buffered events into one batch. Coalescing a burst of stream deltas
// into a single Update+View is what keeps a fast turn from re-rendering per
// delta and starving keystrokes (T1 lag). If the channel closes mid-drain the
// batch is delivered as-is; the next waitForEvent's blocking receive then
// surfaces tStreamEndedMsg.
func (m *TranscriptModel) waitForEvent() tea.Msg {
	if m.events == nil {
		return nil
	}
	ev, ok := <-m.events
	if !ok {
		return tStreamEndedMsg{}
	}
	batch := []session.Event{ev}
	for len(batch) < eventBatchMax {
		select {
		case ev, ok := <-m.events:
			if !ok {
				return tEventBatchMsg(batch)
			}
			batch = append(batch, ev)
		default:
			return tEventBatchMsg(batch)
		}
	}
	return tEventBatchMsg(batch)
}

// reconnectVerboseAttempts is how many failed reconnects emit a transcript line
// before the loop goes quiet (it keeps retrying at the capped backoff).
const reconnectVerboseAttempts = 3

// reconnectBackoff returns the delay before reconnect attempt n: 3s, 6s, 12s,
// 24s, then capped at 30s — so a transient blip heals fast while a dead session
// backs off instead of hammering the connector every 3s forever (RV29).
func reconnectBackoff(attempt int) time.Duration {
	d := 3 * time.Second
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= 30*time.Second {
			return 30 * time.Second
		}
	}
	return d
}

// reconnectAttemptTimeout bounds a single reconnect attempt. It must comfortably
// exceed a cold-pod resume (schedule + image pull + boot + 30s runner health),
// which can run past two minutes on a cold node, or a legitimate slow resume
// would be cut short and bounced into the backoff loop. Retries continue after
// it; the give-up is driven by error classification (ErrSessionGone), not this.
const reconnectAttemptTimeout = 180 * time.Second

// reconnectStageMsg carries one connect-stage update from the in-flight
// doReconnect into the Update loop. done=true signals the stage channel closed
// (the attempt finished) so the waiter stops.
type reconnectStageMsg struct {
	stage  ConnectStage
	detail string
	done   bool
}

// startReconnect opens a fresh stage channel and launches both the reconnect
// attempt and the stage drainer, so the header can show live resume progress.
func (m *TranscriptModel) startReconnect() tea.Cmd {
	m.reconnectStages = make(chan reconnectStageMsg, 8)
	m.reconnectStage = 0
	m.reconnectDetail = ""
	m.reconnectStageKnown = false
	return tea.Batch(m.doReconnect, m.waitForReconnectStage)
}

// waitForReconnectStage drains one stage update from the current reconnect's
// channel, mirroring waitForEvent. It re-subscribes (via the Update handler)
// until doReconnect closes the channel.
func (m *TranscriptModel) waitForReconnectStage() tea.Msg {
	ch := m.reconnectStages
	if ch == nil {
		return reconnectStageMsg{done: true}
	}
	msg, ok := <-ch
	if !ok {
		return reconnectStageMsg{done: true}
	}
	return msg
}

func (m *TranscriptModel) doReconnect() tea.Msg {
	if m.reconnect == nil {
		return tReconnectFailedMsg{err: fmt.Errorf("no reconnect available")}
	}
	ch := m.reconnectStages
	onStage := func(s ConnectStage, detail string) {
		if ch == nil {
			return
		}
		// Non-blocking: never stall the reconnect on a slow/absent UI drainer.
		select {
		case ch <- reconnectStageMsg{stage: s, detail: detail}:
		default:
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), reconnectAttemptTimeout)
	defer cancel()
	client, err := m.reconnect(ctx, onStage)
	if ch != nil {
		close(ch) // unblock the stage waiter; this attempt is done
	}
	if err != nil {
		return tReconnectFailedMsg{err: err}
	}
	return tReconnectedMsg{client: client}
}

// startTurnCmd posts a new turn and surfaces a synchronous start failure; the
// turn itself streams back over SSE.
func startTurnCmd(client RunnerClient, ref session.Ref, prompt, mode, model, effort string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := client.StartTurn(ctx, ref, session.TurnInput{Prompt: prompt, Mode: mode, Model: model, Effort: effort}); err != nil {
			return turnErrMsg{err: err}
		}
		return nil
	}
}

// --------------------------------------------------------------------------
// Diff stat
// --------------------------------------------------------------------------

// permissionDiffStat extracts a +adds/−dels line count and a "+"/"−"-prefixed
// diff preview from an edit-like tool's input. Non-edit tools yield zeroes.
func permissionDiffStat(tool string, input json.RawMessage) (adds, dels int, diffLines []string) {
	switch tool {
	case "Edit":
		var p struct {
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		}
		if json.Unmarshal(input, &p) == nil {
			return diffOf(p.OldString, p.NewString)
		}
	case "Write":
		var p struct {
			Content string `json:"content"`
		}
		if json.Unmarshal(input, &p) == nil {
			return diffOf("", p.Content)
		}
	case "MultiEdit":
		var p struct {
			Edits []struct {
				OldString string `json:"old_string"`
				NewString string `json:"new_string"`
			} `json:"edits"`
		}
		if json.Unmarshal(input, &p) == nil {
			for _, e := range p.Edits {
				a, d, dl := diffOf(e.OldString, e.NewString)
				adds += a
				dels += d
				diffLines = append(diffLines, dl...)
			}
			return adds, dels, diffLines
		}
	}
	return 0, 0, nil
}

// diffOf computes a minimal line diff between oldStr and newStr via LCS, so an
// edit that changes a few lines of many renders as a few +/− lines around
// unchanged context — not the whole block twice. diffLines are prefixed with
// "+", "−", or " " (context). adds/dels are the changed-line counts.
func diffOf(oldStr, newStr string) (adds, dels int, diffLines []string) {
	a := splitLines(oldStr)
	b := splitLines(newStr)
	n, mm := len(a), len(b)

	// LCS length table (suffix DP).
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, mm+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := mm - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	// Walk the table emitting an interleaved unified diff.
	i, j := 0, 0
	for i < n && j < mm {
		switch {
		case a[i] == b[j]:
			diffLines = append(diffLines, " "+a[i])
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			diffLines = append(diffLines, "−"+a[i])
			dels++
			i++
		default:
			diffLines = append(diffLines, "+"+b[j])
			adds++
			j++
		}
	}
	for ; i < n; i++ {
		diffLines = append(diffLines, "−"+a[i])
		dels++
	}
	for ; j < mm; j++ {
		diffLines = append(diffLines, "+"+b[j])
		adds++
	}
	return adds, dels, diffLines
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// condenseDiff keeps changed lines plus one line of surrounding context and
// collapses long unchanged runs into a "… N unchanged" marker, capping the
// result at maxLines (with a trailing "… more" when it overflows).
func condenseDiff(lines []string, maxLines int) []string {
	n := len(lines)
	keep := make([]bool, n)
	for i, l := range lines {
		if strings.HasPrefix(l, "+") || strings.HasPrefix(l, "−") {
			keep[i] = true
			if i > 0 {
				keep[i-1] = true
			}
			if i+1 < n {
				keep[i+1] = true
			}
		}
	}
	var out []string
	for i := 0; i < n; {
		if keep[i] {
			out = append(out, lines[i])
			i++
			continue
		}
		j := i
		for j < n && !keep[j] {
			j++
		}
		if skipped := j - i; skipped > 0 {
			out = append(out, "… "+formatInt(skipped)+" unchanged")
		}
		i = j
	}
	if len(out) > maxLines {
		out = append(out[:maxLines], "… more")
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Transcript styles (styleTUser, styleTAssistant, …) are declared and rebuilt
// in styles.go's rebuildStyles so they track the active light/dark theme.

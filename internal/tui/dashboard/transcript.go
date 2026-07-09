package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard/chat"
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
	blockWarn // a warning notice (pod reschedule, degradation) — Warning tone, between info and error
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

// toolCard is a tool-call rendered as the two-line ⏺-head + ⎿-elbow idiom: line
// one is "⏺ Name(arg)" with the bullet colored by status, line two is the
// indented "⎿  <result>" elbow. It is mutated in place when the matching
// tool.completed / tool.failed event arrives, and ctrl+o toggles its expanded
// state to reveal the edit diff / captured output.
type toolCard struct {
	tool    string
	arg     string
	rawJSON string          // accumulated tool.delta input fragments (parsed into arg, never shown raw)
	input   json.RawMessage // tool input, retained so ctrl+o expansion can reconstruct the edit diff (permission_diff) post-approval
	status  toolStatus
	summary string
	output  string // captured (runner-capped) tool output, revealed on ctrl+o expansion
	// expanded is the ctrl+o expansion state: when set the card renders its
	// available content (arg / edit diff / captured output) under the elbow.
	expanded bool
	// card is the list card that renders this tool. For a flat tool it is the
	// tool's own card; for a subagent child it is the parent Task's card (children
	// render inside that card). Mutating the tool bumps card's version so the list
	// re-renders. May be nil for throwaway/test cards.
	card *blockCard
}

// The per-block data (kind/text/tool/sub) lives on *blockCard (transcript_list.go),
// which is both the block's data and the list.Item that renders it — one unified
// representation, not a data struct plus a parallel item wrapper.

type transcriptPermission struct {
	id        string
	tool      string
	arg       string    // headline argument (Bash command, file path, URL, …) so approval is never blind
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
	// sessionReadModel holds the shared SSE-derived read-model state
	// (DashStatus, Model/CtxLimit, usage tokens/cost, git Branch/Dirty/Ahead/Behind,
	// cwd, defaultModel, the pending-permission descriptor) reduced by the single
	// ApplyEvent reducer that the dashboard Session embeds too. handleEvent
	// delegates that state to it and keeps only presentation (blocks, streaming,
	// the rich transcriptPermission UI). Its exported fields (m.DashStatus,
	// m.InputTokens, …) are promoted, so the transcript refers to them directly.
	sessionReadModel

	client      RunnerClient
	ref         session.Ref
	projectPath string
	agent       string
	title       string

	width, height int

	body       *list.List          // virtualized transcript body (replaces viewport)
	streamItem *blockCard          // ephemeral trailing card for the live streaming turn
	streamAI   *chat.AssistantItem // persistent AI for live tail (A2 incremental render)
	input      textarea.Model      // multi-line composer (boxed; shift+enter inserts a newline)
	permBox    string              // cached rendered permission box (recomputed in layout)
	palette    string              // cached rendered slash palette (recomputed in layout)
	cmdSel     int                 // selected index in the slash palette

	// Grouped help overlay (`?` / /help), shared with the command center.
	showHelp bool
	helpUI   helpModel

	blocks       []*blockCard // the transcript blocks; each IS a list.Item
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

	turnActive bool
	// activeTurnID is the runner's id for the in-flight turn, captured from the
	// turn.started event. It is the precise target for an interrupt; when it is
	// still empty (esc fired before turn.started lands) the interrupt request
	// carries an empty segment and the runner falls back to its sole active turn.
	activeTurnID session.TurnID
	lastSeq      uint64

	// Live working indicator (#4): set while a turn runs.
	turnStart time.Time
	workFrame int
	working   bool // the work-tick loop is scheduled

	// Status-line state (slice 1): model + cwd from session.started; branch +
	// dirty/ahead/behind from workspace.status; ctxLimit from models.Limit — all
	// now on the embedded sessionReadModel — plus the per-attach permission mode
	// cycled with shift+tab and the inline permission card.
	mode     permMode
	pending  *transcriptPermission
	showDiff bool

	// syncStatus mirrors the dashboard's polled mutagen sync health for THIS
	// session ("synced"/"syncing"/"stalled"/""), fed by App after each dashboard
	// delegation. Surfaced as a trailing row-1 status-line segment so a stalled
	// file sync is visible while attached (it otherwise only showed in the
	// dashboard detail pane). Empty renders nothing, so the default status line
	// stays byte-identical — no golden churn.
	syncStatus string

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
	// line is byte-identical to today. See docs/archive/ghostty-terminal-effects.md.
	caps terminal.Caps

	// Stage 3 (Kitty graphics ctx gauge). kittyGaugeBucket is the last fill
	// fraction (quantized to whole percent) we transmitted an image for;
	// kittyGaugeID is that image's id (a new id each change forces a re-fetch);
	// pendingKitty holds the one-shot APC transmission queued when the bucket
	// changes, drained into the next frame by App.View. All zero/empty unless
	// caps.KittyGraphics. See docs/archive/ghostty-terminal-effects.md §4.
	kittyGaugeBucket int
	kittyGaugeID     uint32
	pendingKitty     string

	// modelOverride is the in-session /model selection, sent as TurnInput.Model
	// on the next turn (empty => session/account default). Kept separate from
	// `Model` (the SDK-reported active id used for display, on the embedded
	// sessionReadModel) so the status line doesn't flicker between the requested
	// alias and the resolved id. (defaultModel, the account/session default
	// captured from the first session.started for /model-default, lives on the
	// embedded read-model.)
	modelOverride string

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

	// autopilot is the running /loop or /goal driver, if any (autopilot.go).
	autopilot autopilotState
	// autopilotGenSeq is a monotonic counter snapshotted into autopilot.gen so a
	// loop tick left over from a stopped/restarted run is recognized as stale.
	autopilotGenSeq int
	// lastAssistantText is the most recent non-empty assistant message text,
	// captured on message.completed and reset at turn start. /goal reads it to
	// detect the completion sentinel once a turn ends.
	lastAssistantText string
	// advisorEnabled requests the SDK "advisor" tool for new turns (the /advisor
	// toggle), sent as TurnInput.Advisor. Honored once the runner SDK exposes an
	// advisor option; see session.TurnInput.Advisor.
	advisorEnabled bool
	// idleTimeout mirrors the dashboard's reaper idle timeout (copied on attach and
	// when a warm model is built). cmdLoop reads it to warn when a /loop interval is
	// long enough for the pod to suspend mid-loop (§1e item 4). Zero disables the
	// warning (tests / no reaper configured).
	idleTimeout time.Duration

	reconnect    ReconnectFunc
	reconnecting bool
	terminating  bool
	// transportClose tears down the foreground connection's transport (the SPDY
	// port-forwards behind m.client — ConnectResult.Close). Installed on attach,
	// invoked+cleared by the App's parkTranscript on every detach path (§1d C1):
	// after detach the background observer stream owns the session's live client
	// (autopilotTick reroutes through it), so the parked foreground forward was
	// pure leak. nil while detached/warm or in tests.
	transportClose func()
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
	// events. It makes syncItems a no-op so the per-event commit (which rebuilds
	// the list item set) is skipped during the replay; the caller commits exactly
	// once at the end, turning an O(N^2) cold load into O(N).
	bulkReplay bool
	// reconciles counts commitItems() calls — a behavioral counter the bulk
	// replay test asserts on to prove the replay collapses to a single commit.
	reconciles int

	// lastThemeEpoch is the theme.Epoch() observed at the previous commit. A
	// /theme swap bumps the global epoch but leaves every committed card's version
	// stable, so their cached ANSI — with the old palette baked in — would survive
	// until an unrelated change. When the epoch differs from this, commitItems
	// bumps every card's version so tui/list re-serves fresh-palette renders
	// without waiting for a width change (§1c).
	lastThemeEpoch uint64
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
		sessionReadModel: sessionReadModel{DashStatus: sess.DashStatus},
		client:           client,
		ref:              session.Ref{ID: sess.ID()},
		projectPath:      sess.State.ProjectPath,
		agent:            sess.State.Backend,
		title:            sess.Title,
		body:             list.New(),
		input:            in,
		imode:            modeInsert, // vim off by default → always-focused INSERT
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
		// Seed the epoch so the first commit doesn't spuriously force-bump every
		// card: a fresh model's cards render fresh (version 0, no cache entry), so
		// the §1c force is only needed on a genuine later /theme swap.
		lastThemeEpoch: theme.Epoch(),
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
			cmds = append(cmds, m.waitForEvent())
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
			cmds = append(cmds, m.waitForEvent())
		}
		return m, tea.Batch(cmds...)
	case editorResultMsg:
		m.applyEditorResult(msg)
		return m, nil

	case workTickMsg:
		m.workFrame++
		// Re-render only the in-flight subagent cards, so their header/child
		// spinner animates without forcing a re-render for flat cards.
		if m.bumpRunningSubagents() {
			m.syncItems()
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
			m.appendBlock(blockError, "✗ session no longer exists — press esc to return to the dashboard")
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
		return m, m.waitForReconnectStage()

	case turnErrMsg:
		// The turn never started server-side (StartTurn POST failed), so no
		// turn.started/failed/completed event will ever arrive to clear the
		// optimistic busy state beginTurn() set in submitText. Roll it back
		// here — mirroring the turn-end events — so the working spinner stops
		// and the next prompt actually sends instead of being silently queued
		// behind a phantom active turn (recoverable only by detach/reattach).
		m.finalizeStreaming()
		m.DashStatus = StatusNeedsInput
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

// Transcript styles (styleTUser, styleTAssistant, …) are declared and rebuilt
// in styles.go's rebuildStyles so they track the active light/dark theme.

package dashboard

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard/chat"
	"github.com/cullenmcdermott/sandbox/tui/kit"
)

// E1: tool.delta preview-parse throttle. toolArg re-parses the ENTIRE
// accumulated input buffer, so running it per fragment is O(N²) on a large
// streamed input (e.g. a 100KB Write). Parse every delta while the buffer is
// under toolPreviewEagerBytes (live feel on the common small input), then only
// once per additional toolPreviewStepBytes of growth. The preview is purely
// cosmetic — tool.started later overwrites arg with the cleanly-parsed value —
// so a slightly stale preview on a huge input is fine. Worst case is
// ~(eager/frag) + (total/step) parses instead of one per delta.
const (
	toolPreviewEagerBytes = 2048
	toolPreviewStepBytes  = 2048
)

func (m *TranscriptModel) appendBlock(kind tblockKind, text string) {
	m.blocks = append(m.blocks, m.newBlockCard(kind, text))
	m.syncItems()
}

// startToolCard appends a running tool card and queues it for result matching.
func (m *TranscriptModel) startToolCard(tool, arg string) {
	card := m.newBlockCard(blockToolCard, "")
	card.tool = &toolCard{tool: tool, arg: arg, status: toolRunning, card: card}
	m.blocks = append(m.blocks, card)
	m.pendingTools = append(m.pendingTools, len(m.blocks)-1)
	m.syncItems()
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
			// The fuller (later) tool.started payload carries the complete input;
			// retain it so ctrl+o expansion can rebuild the edit diff post-approval.
			changed := false
			if arg != "" {
				m.blocks[idx].tool.arg = arg
				changed = true
			}
			if len(p.Input) > 0 {
				m.blocks[idx].tool.input = p.Input
				changed = true
			}
			if changed {
				m.blocks[idx].Bump()
			}
			m.syncItems()
			return
		}
	}
	m.startToolCard(p.Tool, arg)
	if last := len(m.blocks) - 1; last >= 0 && m.blocks[last].tool != nil {
		m.blocks[last].tool.input = p.Input
	}
	if p.ToolUseID != "" {
		if m.flatTools == nil {
			m.flatTools = map[string]int{}
		}
		m.flatTools[p.ToolUseID] = len(m.blocks) - 1
	}
}

// finishToolCard resolves a pending tool card with a result. The runner emits
// the tool_use id on tool.completed/failed (runner/src/mapping.ts,
// opencode-turn.ts), so we close the EXACT card that id names — even when it
// isn't the oldest pending one (D1). This is what keeps parallel tool_use from
// landing results on the wrong card, and an interrupted tool whose completion
// never arrives from poisoning FIFO order for the session's life. Only when no
// id is present (the PreToolUse-hook synthetic tool.failed in claude.ts omits
// it, as do pre-toolUseId runners) do we fall back to matching in start order;
// toolName (if present, e.g. on failure) is a label fallback for the orphan case.
func (m *TranscriptModel) finishToolCard(status toolStatus, summary, toolName, output, toolUseID string) {
	// Remap any ANSI the tool emitted in its result onto the theme palette (§A.2).
	summary = kit.RemapANSI(summary)
	if toolUseID != "" {
		if idx, ok := m.flatTools[toolUseID]; ok && idx >= 0 && idx < len(m.blocks) && m.blocks[idx].tool != nil {
			m.removePending(idx)
			m.blocks[idx].tool.status = status
			m.blocks[idx].tool.summary = summary
			m.blocks[idx].tool.output = output
			m.blocks[idx].Bump()
			m.syncItems()
			return
		}
	}
	if len(m.pendingTools) > 0 {
		idx := m.pendingTools[0]
		m.pendingTools = m.pendingTools[1:]
		if idx >= 0 && idx < len(m.blocks) && m.blocks[idx].tool != nil {
			m.blocks[idx].tool.status = status
			m.blocks[idx].tool.summary = summary
			m.blocks[idx].tool.output = output
			m.blocks[idx].Bump()
			m.syncItems()
			return
		}
	}
	// Orphan result (no matching start): render a standalone finished card.
	card := m.newBlockCard(blockToolCard, "")
	card.tool = &toolCard{tool: toolName, status: status, summary: summary, output: output, card: card}
	m.blocks = append(m.blocks, card)
	m.syncItems()
}

// removePending drops a block index from the pendingTools FIFO wherever it sits,
// so an id-matched completion (finishToolCard) closes an out-of-order card
// without leaving a stale slot that a later FIFO fallback would mis-pop.
func (m *TranscriptModel) removePending(blockIdx int) {
	for i, v := range m.pendingTools {
		if v == blockIdx {
			m.pendingTools = append(m.pendingTools[:i:i], m.pendingTools[i+1:]...)
			return
		}
	}
}

// drainPendingTools closes any tool cards still marked running at a turn
// boundary (D1). tool.completed/failed matching alone can strand a card forever
// when a tool is interrupted before its result arrives; draining on every
// turn-terminal event guarantees no card renders "running" across turns. Cards
// are marked failed (the only non-running terminal status) with the boundary
// reason as their summary so the interruption/failure is visible in scrollback.
func (m *TranscriptModel) drainPendingTools(summary string) {
	if len(m.pendingTools) == 0 {
		return
	}
	for _, idx := range m.pendingTools {
		if idx >= 0 && idx < len(m.blocks) && m.blocks[idx].tool != nil && m.blocks[idx].tool.status == toolRunning {
			m.blocks[idx].tool.status = toolErr
			if m.blocks[idx].tool.summary == "" {
				m.blocks[idx].tool.summary = summary
			}
			m.blocks[idx].Bump()
		}
	}
	m.pendingTools = nil
	m.syncItems()
}

// toggleLatestToolCard flips the expansion of the most recent flat tool card
// (ctrl+o on an empty composer). Expanding reveals the card's available content —
// the edit diff, the captured output, or the full argument — under the elbow.
// Cards with nothing to expand are skipped (H7): toggling one was a silent
// no-op that stranded expanded=true, making the card pop open by itself when a
// later tool.completed delivered output. Already-expanded cards stay
// toggleable regardless, so collapse always works. Bumps the card's version so
// the list re-renders, and returns whether a card was toggled (false when
// there is no expandable card, so ctrl+o falls through to $EDITOR
// composition). Per-card focus navigation is a follow-up; toggling the latest
// expandable card is the smallest correct affordance.
func (m *TranscriptModel) toggleLatestToolCard() bool {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		b := m.blocks[i]
		if b.kind == blockToolCard && b.tool != nil {
			if !b.tool.expanded && !m.toolCardExpandable(b.tool) {
				continue
			}
			b.tool.expanded = !b.tool.expanded
			b.Bump()
			m.syncItems()
			return true
		}
	}
	return false
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
		m.syncItems()
	}
}

// beginTurn enters the busy state for a freshly started turn and resets the
// live working indicator (elapsed clock + token/cost counters).
func (m *TranscriptModel) beginTurn() {
	// Replay/streamed turns reach here without going through submitText, so drop
	// the prior footer here too (no-op in the interactive path, where the new
	// user block is already the trailing block).
	m.dropTrailingFooter()
	m.DashStatus = StatusBusy
	m.turnActive = true
	// Clear the previous turn's id; turn.started repopulates it. Until then an
	// interrupt relies on the runner's sole-active-turn fallback.
	m.activeTurnID = ""
	m.turnStart = nowFunc()
	m.InputTokens, m.OutputTokens, m.TotalCostUSD = 0, 0, 0
	// Clear the retained assistant text so a stale /goal sentinel from a prior
	// turn can't trip completion before this turn produces its own reply.
	m.lastAssistantText = ""
}

// ingest applies a single event to this model from an external (background)
// source — the dashboard's passive stream feeding a warm, non-foreground model.
// It reuses handleEvent (which dedupes on lastSeq) and discards the returned
// Cmd, since a background model is never the active tea screen. Cmd-producing
// side effects (the queued-prompt flush) are gated on foreground inside
// handleEvent, so nothing is lost by the discard. Safe to call on a model whose
// own SSE stream has not been started.
func (m *TranscriptModel) ingest(ev session.Event) {
	_ = m.handleEvent(ev)
	// Workstream C: the warm/background feed must mirror events to the cache too.
	// Otherwise a session observed only in the background advances lastSeq without
	// ever caching, leaving a permanent hole that a later cold attach can't
	// backfill (it would resume past the gap and drop that history). maybeCache is
	// idempotent per seq, so the warm→foreground handoff can't double-write.
	m.maybeCache(ev)
}

// handleEvent applies a single runner event to the transcript. It returns a
// follow-up command when the event itself triggers async work (auto-reconnect).
func (m *TranscriptModel) handleEvent(ev session.Event) tea.Cmd {
	// Workstream C: the replay/live boundary marker. Not a persisted event (no
	// seq) — it only flips us out of replay so a genuinely in-flight turn (whose
	// turnActive survived the catch-up) resumes "working", while a session that
	// merely caught up history settles to idle.
	if ev.Type == session.EventStreamLive {
		m.replaying = false
		return nil
	}
	// Seq dedup: drop any persisted event at or below the cursor. After a
	// detach the dashboard's passive stream resumes from ITS (stale) cursor and
	// re-feeds events this retained model already rendered; without this guard
	// those replays duplicate transcript blocks. Locally-synthesized events
	// (seq 0) always pass.
	if ev.Seq != 0 {
		if ev.Seq <= m.lastSeq {
			return nil
		}
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

	// Shared read-model reducer (embedded sessionReadModel): the ONE place that
	// unmarshals status/model/usage/git/permission payloads and derives
	// DashStatus/Model/CtxLimit/InputTokens/…/Branch/pending-permission. It no-ops
	// for events it doesn't own (deltas, tool/message/reasoning), so it's safe to
	// call for every event. handleEvent below keeps ONLY transcript presentation
	// (blocks, streaming buffers, the rich permission card) and reads back the
	// parsed payloads it needs via res, so nothing is unmarshalled twice.
	res := m.ApplyEvent(ev) // the embedded sessionReadModel's reducer

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

	case session.EventContextCompacted:
		// The shared reducer already reset the ctx% token baseline to the
		// post-compaction size. Presentation-only here: drop a one-line marker so a
		// long run's compaction is visible in scrollback, using the parsed payload
		// the reducer handed back (no re-unmarshal).
		marker := "context compacted"
		if p := res.compacted; p != nil {
			switch {
			case p.PreTokens > 0 && p.PostTokens > 0:
				marker = fmt.Sprintf("context compacted · %s→%s tokens",
					kit.FormatTokens(p.PreTokens), kit.FormatTokens(p.PostTokens))
			case p.PreTokens > 0:
				marker = fmt.Sprintf("context compacted · %s tokens", kit.FormatTokens(p.PreTokens))
			case p.PostTokens > 0:
				marker = fmt.Sprintf("context compacted · %s tokens", kit.FormatTokens(p.PostTokens))
			}
		}
		m.appendBlock(blockInfo, marker)

	case session.EventRateLimitUpdated:
		var p session.RateLimitPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.rlSeen = true
		// §2c: the rate-limit row is transient — it surfaces for rlTransientWindow
		// after each update, then fades, rather than owning a permanent second row.
		m.rlUpdatedAt = nowFunc()
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

	case session.EventAutopilotState:
		// ADR §3 render-from-events: the runner-owned driver's armed chip,
		// iteration counter, and terminal scrollback line all derive from this
		// event. The background toast + OS notification for a stopped driver is
		// raised in the dashboard reducer (applyRunnerEvent) so it can respect the
		// replay/live boundary and target background sessions; here we only touch
		// this model's presentation (safe to re-apply on replay).
		m.applyAutopilotState(ev)

	case session.EventTurnCompleted:
		// Status → needs-input is set by the shared reducer.
		m.finalizeStreaming()
		// Close any tool card whose result never arrived (D1) so it can't render
		// "running" into the next turn.
		m.drainPendingTools("no result")
		// Per-turn footer: a dim model/cost summary so scrollback is self-
		// documenting (§D).
		if f := m.turnFooter(); f != "" {
			m.appendBlock(blockFooter, f)
		}
		m.turnActive = false
		// Flush the queued prompt only while foreground (m.events != nil, i.e.
		// this model owns the live stream). A parked/background model is fed via
		// ingest(), which discards the returned Cmd — flushing there would mutate
		// state (user block, turnActive) while the startTurnCmd is thrown away:
		// phantom busy, prompt never sent. Keeping it queued preserves it for the
		// next re-attach.
		queuedFlushed := false
		if m.queuedPrompt != "" && m.events != nil {
			q := m.queuedPrompt
			m.queuedPrompt = ""
			cmd = m.submitText(q) // capture so the queued turn's POST actually runs
			queuedFlushed = true
		}
		// Autopilot continuation (foreground only, m.events != nil — a background
		// ingest() discards the returned Cmd). A queued prompt is a manual steer
		// that already stopped the driver in submit(), so it takes precedence.
		if !queuedFlushed && m.events != nil && m.autopilot.active() {
			// ended is ignored foreground: stopAutopilot already appended the
			// reason and the user is watching. The detached path (handleRunnerEvent)
			// is where a termination raises a toast/OS notification.
			if c, _ := m.autopilotAfterTurn(); c != nil {
				cmd = c
			}
		}

	case session.EventTurnInterrupted:
		// Status → needs-input is set by the shared reducer.
		m.finalizeStreaming()
		// The runner emits nothing terminal for an in-flight tool on abort, so
		// drain the pending cards here (D1) — otherwise an esc mid-Bash leaves that
		// card "running" forever and the next turn's tool.completed FIFO-pops it.
		m.drainPendingTools("interrupted")
		m.turnActive = false
		m.appendBlock(blockInfo, "[interrupted]")
		// A queued prompt here means the interrupt was a steer (queueSteer keeps
		// queuedPrompt set): now that the turn is torn down, submit it as the next
		// turn — sequenced after the interrupt so it can't 409 against the old one.
		// Foreground-only for the same reason as turn.completed above: a background
		// ingest() discards the Cmd, so flushing there would lose the prompt.
		if m.queuedPrompt != "" && m.events != nil {
			q := m.queuedPrompt
			m.queuedPrompt = ""
			cmd = m.submitText(q)
		}

	case session.EventTurnFailed:
		// Status → failed is set by the shared reducer.
		m.finalizeStreaming()
		m.drainPendingTools("interrupted") // in-flight tools die with the failed turn (D1)
		m.turnActive = false
		var p session.TurnFailedPayload
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
		m.streamAI.SetRenderContentMD(renderAssistantMD)
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
		if p.Role == "user" {
			// A user-role message.completed is the runner echoing an injected /
			// string user message (runner/src/mapping.ts handleUserMessage), not
			// assistant output. Render it with the user's own styling instead of
			// as assistant markdown, and keep it out of lastAssistantText (the
			// /goal sentinel scan reads that). Dedup against the optimistic user
			// block appended at submit so an echo of the just-sent prompt doesn't
			// double up.
			//
			// Use strictly p.Content here — NOT the assistantBuf fallback computed
			// above for the assistant path — so an empty user echo is an
			// unconditional no-op and can never attribute buffered assistant text
			// to the user.
			if t := strings.TrimSpace(p.Content); t != "" {
				if n := len(m.blocks); n > 0 && m.blocks[n-1].kind == blockUser &&
					strings.TrimSpace(m.blocks[n-1].text) == t {
					m.syncItems() // duplicate of the optimistic block — skip
				} else {
					m.appendBlock(blockUser, p.Content)
				}
			} else {
				m.syncItems()
			}
			break
		}
		// Retain the last substantive assistant text so /goal can scan it for the
		// completion sentinel when the turn ends.
		if strings.TrimSpace(text) != "" {
			m.lastAssistantText = text
		}
		switch {
		case m.droppedPartialIdx >= 0 && m.droppedPartialIdx < len(m.blocks) &&
			m.blocks[m.droppedPartialIdx].kind == blockAssistant:
			// RV9: this is the replayed full version of a partial we committed on
			// a mid-message stream drop (B9). Replace that block in place instead
			// of appending a second copy of the same reply.
			if strings.TrimSpace(text) != "" {
				m.blocks[m.droppedPartialIdx].text = text
				// In-place text mutation of a committed block: bump its version so
				// tui/list re-renders it with the replayed full text.
				m.blocks[m.droppedPartialIdx].Bump()
			}
			m.droppedPartialIdx = -1
			m.syncItems()
		case strings.TrimSpace(text) != "":
			m.appendBlock(blockAssistant, text)
		default:
			m.syncItems()
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
			m.finishToolCard(toolOK, toolSummary(p.Output), p.Tool, p.Output, p.ToolUseID)
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
			m.finishToolCard(toolErr, summary, p.Tool, p.Output, p.ToolUseID)
		}

	case session.EventPermissionRequested:
		// The shared reducer set status → waiting and captured the plain descriptor.
		// Presentation-only here: build the rich plan/diff permission card from the
		// payload the reducer handed back (no re-unmarshal).
		if p := res.permission; p != nil {
			if p.Tool == "ExitPlanMode" {
				// Plan mode: the agent presents its plan for review. Surface the
				// distinct gold plan card (slice 1c) instead of the permission box.
				var pl struct {
					Plan string `json:"plan"`
				}
				_ = json.Unmarshal(p.Input, &pl)
				// nowFunc (not time.Now) so the anti-type-ahead grace gate is anchored
				// on the same injectable clock permissionAnswerable() compares against
				// — otherwise a test that swaps nowFunc can't exercise the gate.
				m.pending = &transcriptPermission{id: p.PermissionID, tool: p.Tool, isPlan: true, plan: pl.Plan, since: nowFunc()}
			} else {
				adds, dels, diffLines := permissionDiffStat(p.Tool, p.Input)
				m.pending = &transcriptPermission{id: p.PermissionID, tool: p.Tool, arg: toolArg(p.Tool, p.Input), adds: adds, dels: dels, diffLines: diffLines, since: nowFunc()}
			}
		}
		m.layout()

	case session.EventPermissionResolved:
		// The shared reducer reverted status → busy. Presentation-only here.
		m.pending = nil
		m.showDiff = false
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
		m.appendBlock(blockWarn, warn)
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
		// Runner reports session status transitions (idle/busy/error). The shared
		// reducer applied the status mapping; presentation-only here: surface the
		// error reason (M1: was silently dropped) via the reducer's statusReason.
		if res.statusReason != "" {
			m.appendBlock(blockError, "session error: "+res.statusReason)
		}

	case session.EventReasoningStarted:
		// A reasoning/thinking block is beginning. Initialize the buffer and show
		// the live "∴ Thinking" tail immediately (§2b gap 3) instead of a bare
		// spinner — syncItems reconciles the ephemeral reasoning tail into the list.
		m.reasoning = true
		m.reasoningBuf.Reset()
		m.resetReasoningWrapCache()
		m.syncItems()

	case session.EventReasoningDelta:
		// Incremental chunk of thinking text — stream it into the live tail as it
		// arrives (§2b gap 3), mirroring the assistant streamDelta hot path.
		if m.reasoning {
			var p session.MessagePayload // same {Content} shape as message.delta
			_ = json.Unmarshal(ev.Payload, &p)
			m.reasoningBuf.WriteString(p.Content)
			m.streamDelta()
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
		m.resetReasoningWrapCache()
		if text != "" {
			m.appendBlock(blockReasoning, text) // appendBlock syncs the body (clears the live tail)
		} else {
			// Nothing to commit, but the live "∴ Thinking" tail must still be torn
			// down now that reasoning has ended (§2b gap 3) — appendBlock's implicit
			// syncItems didn't run.
			m.syncItems()
		}

	case session.EventToolDelta:
		// tool.delta streams the tool's INPUT JSON as the model types it
		// (input_json_delta) — it is NOT tool output. Show it as a live preview
		// on the card it belongs to so the argument materializes in real time;
		// the finalized tool.started (deduped onto the same card) overwrites arg
		// with the cleanly-parsed value. The runner attributes each delta to its
		// tool_use block (D6), so target by id; a parented (subagent-child)
		// delta whose id has no flat card is dropped rather than animating onto
		// a main-thread card's argument. Only an id-less delta (pre-D6 runner)
		// falls back to the newest-pending heuristic.
		var p session.ToolPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.PartialJSON != "" {
			idx := -1
			switch {
			case p.ToolUseID != "":
				if i, ok := m.flatTools[p.ToolUseID]; ok {
					idx = i
				}
			case p.ParentToolUseID != "":
				// Parented but id-less: never guess — the newest pending flat
				// card is a main-thread card by construction.
			case len(m.pendingTools) > 0:
				idx = m.pendingTools[len(m.pendingTools)-1]
			}
			if idx >= 0 && idx < len(m.blocks) && m.blocks[idx].tool != nil {
				c := m.blocks[idx].tool
				// Accumulate the fragments in a Builder — never string concat
				// (E1: concat copies the whole buffer per delta ⇒ O(N²)).
				c.rawBuf.WriteString(p.PartialJSON)
				total := c.rawBuf.Len()
				// Throttle the preview parse (E1): toolArg is a full Unmarshal of
				// the entire buffer. Parse eagerly while small, then only after
				// each further +step of growth (watermark = lastExtractLen).
				if total < toolPreviewEagerBytes || total-c.lastExtractLen >= toolPreviewStepBytes {
					c.lastExtractLen = total
					m.argExtracts++ // E1 cost pin: counts full-buffer parses
					// Never show raw JSON: parse the buffer (closing an open string
					// value with `"}` — the common mid-stream shape) and preview the
					// extracted argument. Frames that don't parse keep the last good
					// preview.
					raw := c.rawBuf.String()
					if arg := toolArg(c.tool, json.RawMessage(raw)); arg != "" {
						c.arg = collapseSpaces(arg)
					} else if arg := toolArg(c.tool, json.RawMessage(raw+`"}`)); arg != "" {
						c.arg = collapseSpaces(arg)
					}
				}
				// Re-render just this already-registered card, mirroring streamDelta
				// (transcript_list.go): the list cache is keyed on (item, version),
				// so a Bump alone invalidates only this card. E1: dropped the
				// per-delta m.syncItems(), which rebuilt the whole item set.
				m.blocks[idx].Bump()
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

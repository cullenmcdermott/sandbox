package dashboard

// autopilot.go — the chat's autonomous drivers: `/loop`, `/goal`, and the
// `/advisor` toggle, modeled on Claude Code's standard commands.
//
//   - /loop [interval] <prompt>  re-runs a prompt on a wall-clock interval
//     (default 10m) until you stop it. Fires the first iteration immediately,
//     then every interval; a cycle is skipped (not queued) while a turn is
//     still live so it never 409s against the single-active-turn gate.
//   - /goal <condition>          keeps working turn-after-turn toward a stated
//     condition. After each turn the last assistant message is checked for a
//     completion sentinel the agent is instructed to emit; until it appears (or
//     a safety iteration cap is hit) a continuation prompt is auto-sent. This
//     is the client-side analogue of standard Claude Code's small-model judge —
//     the agent self-reports "done" instead of a separate Haiku pass.
//   - /advisor                   toggles requesting the SDK "advisor" tool (a
//     stronger model the executor may consult on hard calls) for new turns. The
//     intent is carried on TurnInput.Advisor; see that field for the SDK caveat.
//
// Only one driver runs at a time (m.autopilot). A manually typed prompt or an
// esc interrupt hands control back to the user and stops the driver.

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// autopilotKind is which autonomous driver (if any) is running.
type autopilotKind int

const (
	autopilotOff autopilotKind = iota
	autopilotLoop
	autopilotGoal
)

const (
	// defaultLoopInterval matches standard Claude Code's `/loop` default.
	defaultLoopInterval = 10 * time.Minute
	// minLoopInterval floors the interval so a fat-fingered "/loop 1s" can't
	// hammer the runner faster than a turn can plausibly complete.
	minLoopInterval = 5 * time.Second
	// goalMaxIterations caps a `/goal` run so a goal that never self-reports
	// done can't loop forever; it pauses (re-runnable) instead.
	goalMaxIterations = 50
	// goalSentinel is the token the agent is told to emit, on its own line, once
	// the goal is fully met. Detection normalizes surrounding punctuation/emoji.
	goalSentinel = "GOAL_MET"
)

// autopilotState is the live driver. The zero value means "off"; gen is a
// snapshot of the model's monotonic counter so a queued loop tick scheduled by
// an earlier run is recognized as stale after a stop/restart.
type autopilotState struct {
	kind     autopilotKind
	prompt   string        // loop: the prompt to re-run; goal: the goal condition
	interval time.Duration // loop only
	iter     int           // turns started by this driver so far
	gen      int           // snapshot of m.autopilotGenSeq at start
}

func (s autopilotState) active() bool { return s.kind != autopilotOff }

// autopilotTickMsg fires one loop iteration. sess identifies the owning session
// so the App can route the tick to the right model whether it is foreground or a
// detached (warm) background model — this is what keeps a /loop firing across a
// detach. gen guards against a tick left over from a stopped or replaced loop.
type autopilotTickMsg struct {
	sess session.ID
	gen  int
}

// startAutopilot installs a fresh driver, stamping it with the next generation
// so any in-flight tick from a prior run is invalidated.
func (m *TranscriptModel) startAutopilot(s autopilotState) {
	m.autopilotGenSeq++
	s.gen = m.autopilotGenSeq
	s.iter = 0
	m.autopilot = s
}

// stopAutopilot clears the driver and (when one was actually running) notes the
// reason in the transcript. Bumping the generation invalidates a pending tick.
func (m *TranscriptModel) stopAutopilot(reason string) {
	if !m.autopilot.active() {
		return
	}
	m.autopilot = autopilotState{}
	m.autopilotGenSeq++
	if reason != "" {
		m.appendBlock(blockInfo, reason)
	}
}

// dispatchAutopilot handles the arg-taking `/loop` and `/goal` commands and the
// `/advisor` toggle when they are typed straight into the prompt. It returns
// (cmd, true) when raw is one of these (so the palette should not also try to
// select), or (nil, false) to fall through to normal palette selection.
func (m *TranscriptModel) dispatchAutopilot(raw string) (tea.Cmd, bool) {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return nil, false
	}
	switch fields[0] {
	case "/loop":
		return m.cmdLoop(fields[1:]), true
	case "/goal":
		// Preserve the condition's original spacing/casing (only the command
		// word is stripped) so multi-word goals read back verbatim.
		return m.cmdGoal(strings.TrimSpace(raw[len(fields[0]):])), true
	case "/advisor":
		return m.cmdAdvisor(fields[1:]), true
	}
	return nil, false
}

// cmdLoop starts (or stops) a `/loop`. Grammar: `/loop [interval] <prompt>` |
// `/loop stop`.
func (m *TranscriptModel) cmdLoop(args []string) tea.Cmd {
	if len(args) == 1 && (args[0] == "stop" || args[0] == "off") {
		if m.autopilot.kind == autopilotLoop {
			m.stopAutopilot("⟳ loop stopped")
		} else {
			m.appendBlock(blockInfo, "no loop is running")
		}
		return nil
	}
	// An optional leading token is the interval when it parses as a duration
	// ("5m", "30s", "1h"); otherwise the whole remainder is the prompt.
	interval, rest := defaultLoopInterval, args
	if len(args) > 0 {
		if d, err := time.ParseDuration(args[0]); err == nil {
			interval, rest = d, args[1:]
		}
	}
	prompt := strings.TrimSpace(strings.Join(rest, " "))
	if prompt == "" {
		m.appendBlock(blockInfo, "usage: /loop [interval] <prompt>   e.g. /loop 5m run the tests and summarize any failures")
		m.appendBlock(blockInfo, "tip: have the prompt emit "+goalSentinel+" on its own line when the backlog is empty and the loop stops itself")
		return nil
	}
	if interval < minLoopInterval {
		interval = minLoopInterval
	}
	// Item 4: a loop interval at or beyond the reaper's idle timeout means the pod
	// suspends while the loop waits between ticks; the warm model is then dropped
	// and the loop silently lapses (item 3). Warn rather than clamp — the user may
	// keep the session busy another way — so the failure mode is at least visible.
	if m.idleTimeout > 0 && interval >= m.idleTimeout {
		m.appendBlock(blockInfo, fmt.Sprintf("⚠ interval %s ≥ idle timeout %s — the pod may suspend between iterations and end the loop; use a shorter interval or keep this session active", humanInterval(interval), humanInterval(m.idleTimeout)))
	}
	m.startAutopilot(autopilotState{kind: autopilotLoop, prompt: prompt, interval: interval})
	m.appendBlock(blockInfo, fmt.Sprintf("⟳ loop started — every %s · esc to stop", humanInterval(interval)))
	// First iteration now, then on the interval.
	return tea.Batch(m.autopilotSubmit(), m.scheduleAutopilotTick())
}

// cmdGoal starts (or stops) a `/goal`. Grammar: `/goal <condition>` |
// `/goal stop`.
func (m *TranscriptModel) cmdGoal(condition string) tea.Cmd {
	if condition == "stop" || condition == "off" {
		if m.autopilot.kind == autopilotGoal {
			m.stopAutopilot("◎ goal stopped")
		} else {
			m.appendBlock(blockInfo, "no goal is running")
		}
		return nil
	}
	if condition == "" {
		m.appendBlock(blockInfo, "usage: /goal <condition>   e.g. /goal all tests pass and the linter is clean")
		return nil
	}
	if m.turnActive {
		m.appendBlock(blockInfo, "finish or interrupt the current turn before starting a goal")
		return nil
	}
	m.startAutopilot(autopilotState{kind: autopilotGoal, prompt: condition})
	m.appendBlock(blockInfo, "◎ goal set — working until met · esc to stop")
	m.autopilot.iter++
	return m.submitText(goalPrompt(condition))
}

// cmdAdvisor toggles (or explicitly sets) the advisor request for new turns.
func (m *TranscriptModel) cmdAdvisor(args []string) tea.Cmd {
	switch {
	case len(args) == 1 && args[0] == "on":
		m.advisorEnabled = true
	case len(args) == 1 && args[0] == "off":
		m.advisorEnabled = false
	default:
		m.advisorEnabled = !m.advisorEnabled
	}
	if m.advisorEnabled {
		m.appendBlock(blockInfo, "⚖ advisor on — new turns may consult a stronger model on hard calls")
	} else {
		m.appendBlock(blockInfo, "⚖ advisor off")
	}
	return nil
}

// autopilotSubmit runs one loop iteration: it starts a turn for the loop prompt
// when the chat is idle, or skips (returns nil) when a turn is still live or no
// loop is running. Goal continuation is handled in autopilotAfterTurn, not here.
func (m *TranscriptModel) autopilotSubmit() tea.Cmd {
	if m.autopilot.kind != autopilotLoop || m.turnActive {
		return nil
	}
	m.autopilot.iter++
	return m.submitText(m.autopilot.prompt)
}

// scheduleAutopilotTick arms the next loop tick, tagged with the current
// generation so a later stop/restart makes it a no-op.
func (m *TranscriptModel) scheduleAutopilotTick() tea.Cmd {
	if m.autopilot.kind != autopilotLoop {
		return nil
	}
	gen, sess := m.autopilot.gen, m.ref.ID
	return tea.Tick(m.autopilot.interval, func(time.Time) tea.Msg {
		return autopilotTickMsg{sess: sess, gen: gen}
	})
}

// autopilotTick handles a loop tick: reschedule the next one and, when idle,
// fire an iteration. A stale tick (stopped/replaced loop) is dropped.
func (m *TranscriptModel) autopilotTick(msg autopilotTickMsg) tea.Cmd {
	if m.autopilot.kind != autopilotLoop || msg.gen != m.autopilot.gen {
		return nil
	}
	return tea.Batch(m.autopilotSubmit(), m.scheduleAutopilotTick())
}

// autopilotAfterTurn drives an autopilot driver when a turn completes. It runs in
// BOTH the foreground (handleEvent) and detached (handleRunnerEvent) paths, so a
// /goal keeps chaining and a /loop can self-terminate even after the user detaches
// to the dashboard (§1e items 1–2). It returns a continuation Cmd to POST (goal
// mode only; loop is tick-driven) or nil, and a non-empty `ended` reason when the
// driver just stopped. stopAutopilot has already written that reason to the
// transcript for scrollback; a detached caller additionally raises it as a
// toast/OS notification so the user isn't left watching a parked chat.
func (m *TranscriptModel) autopilotAfterTurn() (cont tea.Cmd, ended string) {
	switch m.autopilot.kind {
	case autopilotGoal:
		if goalReached(m.lastAssistantText) {
			const reason = "✅ goal reached"
			m.stopAutopilot(reason)
			return nil, reason
		}
		if m.autopilot.iter >= goalMaxIterations {
			reason := fmt.Sprintf("◎ goal paused after %d iterations — run /goal again to keep going", goalMaxIterations)
			m.stopAutopilot(reason)
			return nil, reason
		}
		m.autopilot.iter++
		return m.submitText(goalContinue), ""
	case autopilotLoop:
		// Item 2: a /loop whose prompt tells the agent to emit the sentinel once
		// the backlog is empty terminates here instead of burning a turn every
		// interval forever. Non-sentinel completions fall through to the tick.
		if goalReached(m.lastAssistantText) {
			const reason = "⟳ loop finished — nothing left to do"
			m.stopAutopilot(reason)
			return nil, reason
		}
	}
	return nil, ""
}

// goalPrompt is the opening instruction that puts the agent into goal mode and
// establishes the completion sentinel.
func goalPrompt(condition string) string {
	return fmt.Sprintf(`You are now in GOAL mode. Work autonomously toward this goal, taking one concrete step at a time:

%s

After each step, if — and only if — the goal is fully and verifiably met, end your message with a line containing exactly %s and nothing else. If it is not met, do not stop to ask for confirmation: state the single next action and take it.`, condition, goalSentinel)
}

// goalContinue nudges the agent to keep going after a turn that did not report
// completion.
const goalContinue = "Continue toward the goal. If it is now fully met, end with " + goalSentinel + " on its own line; otherwise take the next concrete step."

// goalReached reports whether text contains the completion sentinel on a line of
// its own. Each line is normalized to its [A-Za-z0-9_] run so decorations like
// "✅ GOAL_MET" or "**GOAL_MET**" still match, while an incidental mention such
// as "I'll print GOAL_MET when done" (extra words on the line) does not.
func goalReached(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		norm := strings.Map(func(r rune) rune {
			switch {
			case r == '_',
				r >= 'A' && r <= 'Z',
				r >= 'a' && r <= 'z',
				r >= '0' && r <= '9':
				return r
			default:
				return -1
			}
		}, line)
		if strings.EqualFold(norm, goalSentinel) {
			return true
		}
	}
	return false
}

// humanInterval renders a duration compactly for the transcript/chip: "10m",
// "5m", "1h30m", "45s". Built from components so it drops only genuinely-zero
// units (Duration.String's "10m0s" must not be naively suffix-trimmed to "1").
func humanInterval(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	mnt := d / time.Minute
	d -= mnt * time.Minute
	s := d / time.Second
	var b strings.Builder
	if h > 0 {
		fmt.Fprintf(&b, "%dh", h)
	}
	if mnt > 0 {
		fmt.Fprintf(&b, "%dm", mnt)
	}
	if s > 0 || b.Len() == 0 {
		fmt.Fprintf(&b, "%ds", s)
	}
	return b.String()
}

// autopilotChip is the hint-row indicator shown while a driver runs (empty when
// off, so the idle hint row is byte-identical to before).
func (m *TranscriptModel) autopilotChip() string {
	var label string
	switch m.autopilot.kind {
	case autopilotLoop:
		label = "⟳ loop " + humanInterval(m.autopilot.interval)
	case autopilotGoal:
		label = fmt.Sprintf("◎ goal · turn %d", m.autopilot.iter)
	default:
		return ""
	}
	return lipgloss.NewStyle().Foreground(theme.Malibu).Render(label) +
		styleSLMuted.Render(" · esc to stop")
}

// autopilotUsageHint returns a one-line usage cue for the palette when the query
// is an arg-taking command mid-type (so "/loop 5m …" shows guidance instead of
// "no matching commands"). Empty for anything else.
func autopilotUsageHint(query string) string {
	first := query
	if i := strings.IndexByte(query, ' '); i >= 0 {
		first = query[:i]
	}
	style := lipgloss.NewStyle().Foreground(theme.TextMuted)
	switch first {
	case "loop":
		return style.Render("⟳ /loop [interval] <prompt> — enter to start")
	case "goal":
		return style.Render("◎ /goal <condition> — enter to start")
	case "advisor":
		return style.Render("⚖ /advisor — enter to toggle")
	}
	return ""
}

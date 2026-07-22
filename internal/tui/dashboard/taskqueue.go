package dashboard

import (
	"fmt"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// taskqueue.go — the async action task queue (§C1 in docs/tui-premium-plan.md),
// a gh-dash lift (MIT) rebuilt as original code. Our toasts route *attention*;
// nothing confirmed slow/destructive *actions*. The queue tracks in-flight
// lifecycle actions (suspend/resume/destroy) and renders them in the statusline
// right segment: a spinner + "Suspending …" while running, then "✓ … suspended"
// / "✗ failed: …" on settle, with a `[⟳ N]` badge when several run at once and a
// 2s auto-clear after the last task settles. It complements — does not replace —
// the toast system.

// taskClearAfter is how long a settled task lingers in the statusline before the
// queue auto-clears it.
const taskClearAfter = 2 * time.Second

// taskState is a task's lifecycle phase.
type taskState int

const (
	taskRunning taskState = iota // in flight; shows a spinner
	taskDone                     // finished OK; shows ✓ until auto-clear
	taskFailed                   // errored; shows ✗ until auto-clear
)

// actionTask is a single tracked lifecycle action.
type actionTask struct {
	id           string // stable key, e.g. "suspend:<session-id>"
	startText    string // shown while running ("Suspending alpha…")
	finishedText string // shown on success ("alpha suspended")
	state        taskState
	err          error     // set when state == taskFailed
	settledAt    time.Time // when the task finished/failed (drives auto-clear)
}

// taskQueue tracks in-flight and recently-settled lifecycle actions in insertion
// order (most recent last), so the statusline can surface the current action and
// clear it 2s after it settles. The zero value is ready to use.
type taskQueue struct {
	tasks []*actionTask
}

// find returns the tracked task with the given id, or nil.
func (q *taskQueue) find(id string) *actionTask {
	for _, t := range q.tasks {
		if t.id == id {
			return t
		}
	}
	return nil
}

// start registers (or restarts) a running task. Re-starting a same-id task
// resets it in place, so a rapid suspend→resume cycle reuses one slot rather
// than stacking stale entries.
func (q *taskQueue) start(id, startText, finishedText string) {
	if t := q.find(id); t != nil {
		t.startText = startText
		t.finishedText = finishedText
		t.state = taskRunning
		t.err = nil
		t.settledAt = time.Time{}
		return
	}
	q.tasks = append(q.tasks, &actionTask{
		id:           id,
		startText:    startText,
		finishedText: finishedText,
		state:        taskRunning,
	})
}

// finish marks a task succeeded and stamps its settle time. No-op if unknown.
func (q *taskQueue) finish(id string, now time.Time) {
	if t := q.find(id); t != nil {
		t.state = taskDone
		t.err = nil
		t.settledAt = now
	}
}

// fail marks a task errored and stamps its settle time. No-op if unknown.
func (q *taskQueue) fail(id string, err error, now time.Time) {
	if t := q.find(id); t != nil {
		t.state = taskFailed
		t.err = err
		t.settledAt = now
	}
}

// prune drops settled tasks whose auto-clear window has elapsed. Running tasks
// are always kept. Called on the motion tick so the queue can't grow unbounded.
func (q *taskQueue) prune(now time.Time) {
	kept := q.tasks[:0]
	for _, t := range q.tasks {
		if t.state == taskRunning || now.Sub(t.settledAt) < taskClearAfter {
			kept = append(kept, t)
		}
	}
	// Clear the tail so drained *actionTask pointers can be GC'd.
	for i := len(kept); i < len(q.tasks); i++ {
		q.tasks[i] = nil
	}
	q.tasks = kept
}

// active reports whether any task is running or still within its auto-clear
// window — i.e. whether the statusline has something to show and the motion loop
// should keep ticking (to advance the spinner and reach the auto-clear moment).
func (q *taskQueue) active(now time.Time) bool {
	for _, t := range q.tasks {
		if t.state == taskRunning || now.Sub(t.settledAt) < taskClearAfter {
			return true
		}
	}
	return false
}

// render produces the statusline right-segment string for the queue, or "" when
// nothing is active. spinner is the (already reduce-motion-resolved) spinner
// glyph for the running state — pass theme.SpinnerFrame(frame), which collapses
// to a static frame under SANDBOX_REDUCE_MOTION.
func (q *taskQueue) render(now time.Time, spinner string) string {
	var running, settled []*actionTask
	for _, t := range q.tasks {
		switch {
		case t.state == taskRunning:
			running = append(running, t)
		case now.Sub(t.settledAt) < taskClearAfter:
			settled = append(settled, t)
		}
	}
	if len(running) == 0 && len(settled) == 0 {
		return ""
	}

	// While anything is running, the running state wins: spinner + the most
	// recent running action's text, with a [⟳ N] badge when several overlap.
	if len(running) > 0 {
		cur := running[len(running)-1]
		seg := spinner + " " + lipgloss.NewStyle().Foreground(theme.TextBody).Render(cur.startText)
		if len(running) > 1 {
			badge := lipgloss.NewStyle().Foreground(theme.Malibu).
				Render(fmt.Sprintf("[⟳ %d] ", len(running)))
			seg = badge + seg
		}
		return seg
	}

	// Only settled tasks remain within the clear window: show the most recent
	// result (✓ finished / ✗ failed).
	cur := settled[len(settled)-1]
	if cur.state == taskFailed {
		msg := "failed"
		if cur.err != nil {
			msg = "failed: " + cur.err.Error()
		}
		return lipgloss.NewStyle().Foreground(theme.Coral).Render("✗ " + msg)
	}
	return lipgloss.NewStyle().Foreground(theme.Success).Render("✓ " + cur.finishedText)
}

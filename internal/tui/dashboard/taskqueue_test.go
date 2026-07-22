package dashboard

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// t0 is a fixed clock base for the task-queue tests.
var t0 = time.Date(2030, 6, 21, 12, 0, 0, 0, time.UTC)

// TestTaskQueueRunningShowsText: a running task renders the spinner + start text.
func TestTaskQueueRunningShowsText(t *testing.T) {
	var q taskQueue
	q.start("suspend:s1", "Suspending alpha…", "alpha suspended")

	if !q.active(t0) {
		t.Fatal("queue should be active with a running task")
	}
	out := q.render(t0, "SPIN")
	if !strings.Contains(out, "SPIN") || !strings.Contains(out, "Suspending alpha…") {
		t.Fatalf("render = %q, want spinner + start text", out)
	}
}

// TestTaskQueueConcurrentBadge: two concurrent running tasks show the [⟳ N] badge.
func TestTaskQueueConcurrentBadge(t *testing.T) {
	var q taskQueue
	q.start("suspend:s1", "Suspending alpha…", "alpha suspended")
	q.start("destroy:s2", "Destroying beta…", "beta destroyed")

	out := q.render(t0, "SPIN")
	if !strings.Contains(out, "[⟳ 2]") {
		t.Fatalf("render = %q, want [⟳ 2] badge", out)
	}
	// The most-recently-started running task's text is the current line.
	if !strings.Contains(out, "Destroying beta…") {
		t.Fatalf("render = %q, want most-recent running text", out)
	}
}

// TestTaskQueueFinishThenClear: a finished task shows ✓ within the window, then
// auto-clears (goes inactive and renders empty) once the window elapses.
func TestTaskQueueFinishThenClear(t *testing.T) {
	var q taskQueue
	q.start("suspend:s1", "Suspending alpha…", "alpha suspended")
	q.finish("suspend:s1", t0)

	within := t0.Add(taskClearAfter - time.Millisecond)
	out := q.render(within, "SPIN")
	if !strings.Contains(out, "✓") || !strings.Contains(out, "alpha suspended") {
		t.Fatalf("render = %q, want ✓ + finished text within window", out)
	}
	if !q.active(within) {
		t.Fatal("task should still be active within the clear window")
	}

	after := t0.Add(taskClearAfter + time.Millisecond)
	if q.active(after) {
		t.Fatal("task should be inactive past the clear window")
	}
	if out := q.render(after, "SPIN"); out != "" {
		t.Fatalf("render after clear = %q, want empty", out)
	}
}

// TestTaskQueueFailShowsError: a failed task shows ✗ and the error text.
func TestTaskQueueFailShowsError(t *testing.T) {
	var q taskQueue
	q.start("resume:s1", "Resuming alpha…", "alpha resumed")
	q.fail("resume:s1", errors.New("pod stuck"), t0)

	out := q.render(t0, "SPIN")
	if !strings.Contains(out, "✗") || !strings.Contains(out, "failed: pod stuck") {
		t.Fatalf("render = %q, want ✗ failed: <err>", out)
	}
}

// TestTaskQueuePruneDropsSettled: prune removes settled tasks past the window
// but keeps running ones.
func TestTaskQueuePruneDropsSettled(t *testing.T) {
	var q taskQueue
	q.start("suspend:s1", "Suspending alpha…", "alpha suspended")
	q.start("destroy:s2", "Destroying beta…", "beta destroyed")
	q.finish("suspend:s1", t0) // settles s1

	q.prune(t0.Add(taskClearAfter + time.Millisecond))
	if len(q.tasks) != 1 {
		t.Fatalf("after prune len = %d, want 1 (running kept, settled dropped)", len(q.tasks))
	}
	if q.tasks[0].id != "destroy:s2" {
		t.Fatalf("kept task = %q, want the still-running destroy:s2", q.tasks[0].id)
	}
}

// TestTaskQueueRestartReusesSlot: restarting a same-id task resets it in place
// rather than stacking a second entry.
func TestTaskQueueRestartReusesSlot(t *testing.T) {
	var q taskQueue
	q.start("resume:s1", "Resuming alpha…", "alpha resumed")
	q.fail("resume:s1", errors.New("boom"), t0)
	q.start("resume:s1", "Resuming alpha…", "alpha resumed") // retry

	if len(q.tasks) != 1 {
		t.Fatalf("len = %d, want 1 (restart reuses the slot)", len(q.tasks))
	}
	if q.tasks[0].state != taskRunning || q.tasks[0].err != nil {
		t.Fatalf("restarted task not reset: state=%v err=%v", q.tasks[0].state, q.tasks[0].err)
	}
}

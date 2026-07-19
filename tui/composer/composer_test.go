package composer

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// clock is a controllable time source for the grace-gate tests.
type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

func typeRune(m *Model, r rune) *Model {
	m, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	return m
}

func typeStr(m *Model, s string) *Model {
	for _, r := range s {
		m = typeRune(m, r)
	}
	return m
}

func press(m *Model, code rune) *Model {
	m, _ = m.Update(tea.KeyPressMsg{Code: code})
	return m
}

func TestSubmitWhenReady(t *testing.T) {
	var got string
	m := New(WithSubmit(func(s string) { got = s }))
	m.Focus()
	m = typeStr(m, "hello world")
	if m.Value() != "hello world" {
		t.Fatalf("draft not accumulated: %q", m.Value())
	}
	m = press(m, tea.KeyEnter)
	if got != "hello world" {
		t.Errorf("onSubmit got %q, want %q", got, "hello world")
	}
	if m.Value() != "" {
		t.Errorf("draft not cleared after submit: %q", m.Value())
	}
}

func TestSubmitIgnoresBlank(t *testing.T) {
	called := false
	m := New(WithSubmit(func(string) { called = true }))
	m.Focus()
	m = typeStr(m, "   ")
	press(m, tea.KeyEnter)
	if called {
		t.Error("blank prompt was submitted")
	}
}

// Queue-while-busy: Enter while busy arms an editable steer (does not submit);
// the draft is retained and flushes as the next turn when the turn ends.
func TestQueueWhileBusy(t *testing.T) {
	var submitted []string
	m := New(WithSubmit(func(s string) { submitted = append(submitted, s) }))
	m.Focus()
	m.SetState(StateBusy)
	m = typeStr(m, "next task")
	m = press(m, tea.KeyEnter)
	if len(submitted) != 0 {
		t.Fatalf("Enter while busy submitted immediately: %v", submitted)
	}
	if !m.Queued() {
		t.Fatal("Enter while busy did not arm a queued steer")
	}
	if m.Value() != "next task" {
		t.Errorf("queued draft not retained/editable: %q", m.Value())
	}
	// The turn ends → the queued prompt flushes as the next turn.
	m.SetState(StateReady)
	if len(submitted) != 1 || submitted[0] != "next task" {
		t.Errorf("queued prompt did not flush on turn end: %v", submitted)
	}
	if m.Queued() || m.Value() != "" {
		t.Errorf("queue not cleared after flush (queued=%v value=%q)", m.Queued(), m.Value())
	}
}

// The escape cascade: steer a queued prompt → interrupt a running turn → detach
// when idle. First applicable wins.
func TestEscapeCascade(t *testing.T) {
	t.Run("queued steers", func(t *testing.T) {
		var steered string
		interrupted := false
		m := New(WithSteer(func(s string) { steered = s }), WithInterrupt(func() { interrupted = true }))
		m.Focus()
		m.SetState(StateBusy)
		m = typeStr(m, "urgent")
		m = press(m, tea.KeyEnter) // arm the steer
		m = press(m, tea.KeyEscape)
		if steered != "urgent" {
			t.Errorf("esc did not steer the queued prompt: got %q", steered)
		}
		if interrupted {
			t.Error("esc interrupted instead of steering (cascade order wrong)")
		}
		if m.Queued() || m.Value() != "" {
			t.Error("steer did not clear the queue/draft")
		}
	})

	t.Run("busy interrupts", func(t *testing.T) {
		interrupted := false
		m := New(WithInterrupt(func() { interrupted = true }))
		m.Focus()
		m.SetState(StateBusy)
		press(m, tea.KeyEscape)
		if !interrupted {
			t.Error("esc while busy (no queue) did not interrupt")
		}
	})

	t.Run("idle detaches", func(t *testing.T) {
		detached := false
		m := New(WithDetach(func() { detached = true }))
		m.Focus()
		press(m, tea.KeyEscape)
		if !detached {
			t.Error("esc while idle did not detach")
		}
	})
}

// An asynchronously-arriving permission request must not consume the text the
// user was already entering: answer keys typed within the type-ahead grace go to
// the draft, not to approve/deny. Only a deliberate key after a pause answers.
func TestAsyncPermissionDoesNotStealPrompt(t *testing.T) {
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	approved, denied := false, false
	m := New(
		WithNow(clk.now),
		WithApprove(func(string) { approved = true }),
		WithDeny(func() { denied = true }),
	)
	m.Focus()

	// The user is mid-word (note "a" and "d" are answer keys).
	for _, r := range "he" {
		clk.add(15 * time.Millisecond)
		m = typeRune(m, r)
	}
	// A permission request pops asynchronously.
	m.SetPermissionPending(true)
	// The user keeps typing WITHOUT pausing — including 'a' and 'd'.
	for _, r := range "adland" {
		clk.add(15 * time.Millisecond)
		m = typeRune(m, r)
	}
	if approved || denied {
		t.Fatalf("type-ahead answered the permission (approved=%v denied=%v)", approved, denied)
	}
	if m.Value() != "headland" {
		t.Fatalf("permission stole keystrokes; draft = %q, want %q", m.Value(), "headland")
	}

	// After a deliberate pause, the answer keys resolve the permission.
	clk.add(400 * time.Millisecond)
	m = typeRune(m, 'a')
	if !approved {
		t.Error("a deliberate 'a' after a pause did not approve")
	}
	if m.Value() != "headland" {
		t.Errorf("the answering key leaked into the draft: %q", m.Value())
	}
}

func TestPermissionCapAnswers(t *testing.T) {
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	denied := false
	m := New(WithNow(clk.now), WithDeny(func() { denied = true }))
	m.Focus()
	m.SetPermissionPending(true)
	// Even under a held/repeating key, the hard cap makes it answerable.
	for i := 0; i < 20; i++ {
		clk.add(100 * time.Millisecond) // < quiet each step, but accumulates past cap
		m = typeRune(m, 'd')
	}
	if !denied {
		t.Error("permission never became answerable under the hard cap")
	}
}

func TestDisabledIgnoresKeys(t *testing.T) {
	submitted := false
	m := New(WithSubmit(func(string) { submitted = true }))
	m.Focus()
	m.SetState(StateDisabled)
	m = typeStr(m, "hello")
	m = press(m, tea.KeyEnter)
	if m.Value() != "" {
		t.Errorf("disabled composer accepted text: %q", m.Value())
	}
	if submitted {
		t.Error("disabled composer submitted")
	}
}

func TestHistoryRecallWithDraftPreservation(t *testing.T) {
	m := New(WithSubmit(func(string) {}))
	m.Focus()
	m = typeStr(m, "one")
	m = press(m, tea.KeyEnter)
	m = typeStr(m, "two")
	m = press(m, tea.KeyEnter)

	// A half-typed draft is preserved when entering recall.
	m = typeStr(m, "dr")
	m = press(m, tea.KeyUp)
	if m.Value() != "two" {
		t.Fatalf("↑ did not recall newest: %q", m.Value())
	}
	m = press(m, tea.KeyUp)
	if m.Value() != "one" {
		t.Fatalf("↑↑ did not recall oldest: %q", m.Value())
	}
	m = press(m, tea.KeyDown)
	if m.Value() != "two" {
		t.Fatalf("↓ did not step newer: %q", m.Value())
	}
	m = press(m, tea.KeyDown)
	if m.Value() != "dr" {
		t.Fatalf("↓ past newest did not restore the draft: %q", m.Value())
	}
}

func TestResponsiveGrowthCaps(t *testing.T) {
	m := New(WithMaxRows(6))
	m.Focus()
	m.SetWidth(40)
	m.SetValue(strings.Repeat("line\n", 20))
	// The box grows with content but caps at maxRows; Height is rows + hint row.
	if h := m.Height(); h != 7 {
		t.Errorf("growth not capped: Height()=%d, want 7 (6 rows + hint)", h)
	}
	m.SetValue("one line")
	if h := m.Height(); h != 2 {
		t.Errorf("single-line height wrong: %d, want 2", h)
	}
}

func TestViewReflectsState(t *testing.T) {
	m := New()
	m.Focus()
	m.SetWidth(60)
	if !strings.Contains(m.View(), "enter to send") {
		t.Error("ready hint missing from View")
	}
	m.SetState(StateBusy)
	if !strings.Contains(m.View(), "interrupt") {
		t.Error("busy hint missing from View")
	}
	m.SetPermissionPending(true)
	if !strings.Contains(m.View(), "permission pending") {
		t.Error("permission hint missing from View")
	}
}

package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/runner"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// fakeDriverStore is an in-memory dashboard.DriverStore for the §1e re-arm tests.
type fakeDriverStore struct {
	specs map[session.ID]session.AutopilotRequest
	saved int
}

func newFakeDriverStore() *fakeDriverStore {
	return &fakeDriverStore{specs: map[session.ID]session.AutopilotRequest{}}
}

func (f *fakeDriverStore) LoadDriver(id session.ID) (session.AutopilotRequest, bool) {
	s, ok := f.specs[id]
	return s, ok
}
func (f *fakeDriverStore) SaveDriver(id session.ID, spec session.AutopilotRequest) {
	f.specs[id] = spec
	f.saved++
}

// TestCapabilityArmsRunnerDriver: with the backend reporting
// capabilities.autopilot, /loop and /goal arm the RUNNER driver (PUT /autopilot)
// and do NOT start the local tea.Tick loop (ADR §Q3 precedence).
func TestCapabilityArmsRunnerDriver(t *testing.T) {
	fc := &fakeRunnerClient{autopilotCap: true}
	m := newAutopilotTranscript(fc)
	m.autopilotCapable = true
	m.mode = modeAcceptEdits
	m.modelOverride = "opus"

	execCmd(m.cmdLoop([]string{"5m", "work", "the", "backlog"}))

	if m.autopilot.active() {
		t.Error("capable /loop started the LOCAL driver; it must arm the runner instead")
	}
	if m.turnActive {
		t.Error("capable /loop started a local turn; the runner self-submits")
	}
	if len(fc.armReqs) != 1 {
		t.Fatalf("ArmAutopilot called %d times, want 1", len(fc.armReqs))
	}
	req := fc.armReqs[0]
	if req.Kind != session.AutopilotKindLoop || req.Prompt != "work the backlog" {
		t.Errorf("arm req kind/prompt: %+v", req)
	}
	if req.Sentinel != goalSentinel {
		t.Errorf("arm req sentinel = %q, want %q", req.Sentinel, goalSentinel)
	}
	if req.IntervalMs != (5 * time.Minute).Milliseconds() {
		t.Errorf("arm req intervalMs = %d, want %d", req.IntervalMs, (5 * time.Minute).Milliseconds())
	}
	if req.MaxIterations != autopilotMaxIterations {
		t.Errorf("arm req maxIterations = %d, want %d", req.MaxIterations, autopilotMaxIterations)
	}
	if req.Overrides.Model != "opus" || req.Overrides.Mode != modeAcceptEdits.apiValue() {
		t.Errorf("arm req overrides did not carry the current model/mode: %+v", req.Overrides)
	}

	// /goal arms a goal-kind driver whose prompt frames the sentinel.
	execCmd(m.cmdGoal("all tests pass"))
	if len(fc.armReqs) != 2 || fc.armReqs[1].Kind != session.AutopilotKindGoal {
		t.Fatalf("/goal did not arm a goal driver: %+v", fc.armReqs)
	}
	if !strings.Contains(fc.armReqs[1].Prompt, "all tests pass") ||
		!strings.Contains(fc.armReqs[1].Prompt, goalSentinel) {
		t.Errorf("goal arm prompt missing condition/sentinel: %q", fc.armReqs[1].Prompt)
	}
}

// TestNoCapabilityUsesLocalDriver: without the capability bit, /loop keeps EXACTLY
// today's local tea.Tick driver (arms locally, fires the first iteration, never
// touches the runner autopilot endpoint).
func TestNoCapabilityUsesLocalDriver(t *testing.T) {
	fc := &fakeRunnerClient{} // autopilotCap false
	m := newAutopilotTranscript(fc)
	m.autopilotCapable = false

	// Do NOT execCmd the returned batch: it carries the interval tea.Tick, which
	// a synchronous test execution would SLEEP for (5 minutes here). The local
	// arm is synchronous; drive the first iteration explicitly like
	// TestLoopSubmitAndStop does.
	_ = m.cmdLoop([]string{"5m", "ping"})
	if m.autopilot.kind != autopilotLoop {
		t.Fatalf("no-capability /loop did not start the local driver: kind=%v", m.autopilot.kind)
	}
	if len(fc.armReqs) != 0 {
		t.Errorf("no-capability /loop hit the runner autopilot endpoint: %+v", fc.armReqs)
	}
	execCmd(m.autopilotSubmit())
	if !m.turnActive {
		t.Error("local /loop should fire the first iteration immediately")
	}
}

// TestAutopilotStateRendersChip: the armed chip, iteration counter, and terminal
// scrollback line all derive PURELY from autopilot.state events (ADR §3).
func TestAutopilotStateRendersChip(t *testing.T) {
	m := newAutopilotTranscript(&fakeRunnerClient{})

	m.handleEvent(mkEventSeq(1, session.EventAutopilotState,
		session.AutopilotStatePayload{State: "armed", Kind: "loop", Iteration: 0, Gen: 1}))
	if !m.runnerDriver.active {
		t.Fatal("armed event did not activate the runner-driver chip")
	}
	if chip := m.autopilotChip(); !strings.Contains(chip, "loop") || !strings.Contains(chip, "esc to stop") {
		t.Errorf("armed chip = %q, want a loop chip with the esc hint", chip)
	}

	m.handleEvent(mkEventSeq(2, session.EventAutopilotState,
		session.AutopilotStatePayload{State: "ticked", Kind: "loop", Iteration: 3, Gen: 1}))
	if m.runnerDriver.iteration != 3 {
		t.Errorf("iteration = %d, want 3 (from the ticked event)", m.runnerDriver.iteration)
	}
	if chip := m.autopilotChip(); !strings.Contains(chip, "3/") {
		t.Errorf("chip should surface iteration/budget, got %q", chip)
	}

	m.handleEvent(mkEventSeq(3, session.EventAutopilotState,
		session.AutopilotStatePayload{State: "stopped", Kind: "loop", Reason: "sentinel", Iteration: 3, Gen: 1}))
	if m.runnerDriver.active {
		t.Error("stopped event did not clear the runner-driver chip")
	}
	if got, ok := lastBlockOfKind(m, blockInfo); !ok || !strings.Contains(got, "finished") {
		t.Errorf("stopped(sentinel) did not drop a scrollback line: %q", got)
	}
}

// TestRunnerDriverStoppedToastsBackgroundLiveNotReplay is the replay/live boundary
// (ADR §3, §1a): a LIVE stopped autopilot.state for a background session raises a
// toast + OS notification; a REPLAYED one (catchingUp) must not.
func TestRunnerDriverStoppedToastsBackgroundLiveNotReplay(t *testing.T) {
	stopped := func(seq uint64) session.Event {
		return mkEventSeq(seq, session.EventAutopilotState,
			session.AutopilotStatePayload{State: "stopped", Kind: "loop", Reason: "sentinel", Iteration: 5, Gen: 1})
	}

	// Replayed (mid catch-up burst): no toast.
	m := New(nil)
	id := session.ID("sess-ap")
	sess := transcriptSession()
	sess.State.ID = id
	sess.catchingUp = true
	m.sessions = []Session{sess}
	if _, cmd := m.handleRunnerEvent(RunnerEventMsg{ID: id, Event: stopped(10)}); cmd != nil {
		t.Errorf("a replayed stopped must NOT toast/notify, got a cmd (%T)", cmd())
	}

	// Live (past the replay boundary): toast.
	m2 := New(nil)
	sess2 := transcriptSession()
	sess2.State.ID = id
	sess2.catchingUp = false
	m2.sessions = []Session{sess2}
	_, cmd := m2.handleRunnerEvent(RunnerEventMsg{ID: id, Event: stopped(10)})
	if cmd == nil {
		t.Fatal("a live stopped for a background session must toast")
	}
	if _, ok := cmd().(toastMsg); !ok {
		t.Errorf("live stopped cmd = %T, want toastMsg", cmd())
	}
}

// TestRearmFromIndex is the §1e follow-up: a bare `/loop` re-arms the last recorded
// driver spec (restored from the store) without retyping, and arming persists the
// spec to the store.
func TestRearmFromIndex(t *testing.T) {
	fc := &fakeRunnerClient{autopilotCap: true}
	store := newFakeDriverStore()
	m := newAutopilotTranscript(fc)
	m.autopilotCapable = true
	m.driverStore = store
	m.mode = modeAcceptEdits

	// First arm records the spec to the store.
	execCmd(m.cmdLoop([]string{"5m", "burn down the backlog"}))
	if store.saved == 0 {
		t.Fatal("arming did not persist the driver spec (§1e)")
	}

	// A fresh transcript (simulating a re-attach) with only the stored spec.
	m2 := newAutopilotTranscript(fc)
	m2.autopilotCapable = true
	m2.driverStore = store
	if spec, ok := store.LoadDriver(m2.ref.ID); ok {
		s := spec
		m2.lastDriverSpec = &s
	}

	fc.armReqs = nil
	execCmd(m2.cmdLoop(nil)) // bare /loop → re-arm from the recorded spec
	if len(fc.armReqs) != 1 {
		t.Fatalf("bare /loop did not re-arm from the stored spec: %+v", fc.armReqs)
	}
	if fc.armReqs[0].Prompt != "burn down the backlog" {
		t.Errorf("re-armed prompt = %q, want the stored prompt", fc.armReqs[0].Prompt)
	}
}

// TestCapabilityFetch: fetchAutopilotCapabilityCmd reads capabilities.autopilot
// from /status, and the transcript Update handler records it.
func TestCapabilityFetch(t *testing.T) {
	fc := &fakeRunnerClient{autopilotCap: true}
	m := newAutopilotTranscript(fc)

	msg := fetchAutopilotCapabilityCmd(fc, m.ref)()
	cap, ok := msg.(autopilotCapabilityMsg)
	if !ok || !cap.capable {
		t.Fatalf("capability fetch = %+v, want capable=true", msg)
	}
	m.Update(cap)
	if !m.autopilotCapable {
		t.Error("Update did not record the fetched capability")
	}
}

// TestArmUnsupportedFallsBackToLocal: if the arm POST unexpectedly returns
// unsupported (capability/version skew), the command falls back to the local
// tea.Tick driver so it still does something.
func TestArmUnsupportedFallsBackToLocal(t *testing.T) {
	fc := &fakeRunnerClient{autopilotCap: true, armErr: runner.ErrAutopilotUnsupported}
	m := newAutopilotTranscript(fc)
	m.autopilotCapable = true

	// Drive the arm and feed its result back through the handler. Do NOT execCmd
	// the returned batch — it carries the local fallback's interval tea.Tick,
	// which a synchronous execution would sleep for; the fallback arm itself is
	// synchronous inside handleAutopilotArmResult.
	res := m.armRunnerAutopilot(session.AutopilotRequest{Kind: session.AutopilotKindLoop, Prompt: "ping"})
	msg := res().(autopilotArmResultMsg)
	_ = m.handleAutopilotArmResult(msg)

	if m.autopilotCapable {
		t.Error("an unsupported arm should drop the capability bit")
	}
	if m.autopilot.kind != autopilotLoop {
		t.Errorf("unsupported arm did not fall back to the local driver: kind=%v", m.autopilot.kind)
	}
}

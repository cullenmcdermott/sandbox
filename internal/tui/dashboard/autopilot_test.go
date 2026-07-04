package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// newAutopilotTranscript builds a foreground transcript (owns a live event
// stream, so autopilot continuations actually flush) for the autopilot tests.
func newAutopilotTranscript(fc *fakeRunnerClient) *TranscriptModel {
	m := NewTranscript(fc, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.events = make(chan session.Event) // foreground marker (see TestEscSteer…)
	return m
}

// TestLoopParsesIntervalAndPrompt covers the `/loop [interval] <prompt>` grammar:
// a leading duration token is the interval, the rest is the prompt, and a bare
// duration-less form defaults the interval.
func TestLoopParsesIntervalAndPrompt(t *testing.T) {
	m := newAutopilotTranscript(&fakeRunnerClient{})

	m.cmdLoop([]string{"5m", "run", "the", "tests"})
	if m.autopilot.kind != autopilotLoop {
		t.Fatalf("kind = %v, want loop", m.autopilot.kind)
	}
	if m.autopilot.interval != 5*time.Minute {
		t.Errorf("interval = %s, want 5m", m.autopilot.interval)
	}
	if m.autopilot.prompt != "run the tests" {
		t.Errorf("prompt = %q, want %q", m.autopilot.prompt, "run the tests")
	}
	if !m.turnActive {
		t.Error("first loop iteration should have started a turn")
	}

	// No parseable interval → whole remainder is the prompt, default interval.
	m2 := newAutopilotTranscript(&fakeRunnerClient{})
	m2.cmdLoop([]string{"check", "the", "deploy"})
	if m2.autopilot.interval != defaultLoopInterval {
		t.Errorf("default interval = %s, want %s", m2.autopilot.interval, defaultLoopInterval)
	}
	if m2.autopilot.prompt != "check the deploy" {
		t.Errorf("prompt = %q, want %q", m2.autopilot.prompt, "check the deploy")
	}

	// Below the floor is clamped so a stray "/loop 1s" can't hammer the runner.
	m3 := newAutopilotTranscript(&fakeRunnerClient{})
	m3.cmdLoop([]string{"1s", "spin"})
	if m3.autopilot.interval != minLoopInterval {
		t.Errorf("clamped interval = %s, want %s", m3.autopilot.interval, minLoopInterval)
	}
}

// TestLoopSubmitAndStop verifies a loop iteration POSTs the loop prompt and that
// `/loop stop` clears the driver.
func TestLoopSubmitAndStop(t *testing.T) {
	fc := &fakeRunnerClient{}
	m := newAutopilotTranscript(fc)
	m.startAutopilot(autopilotState{kind: autopilotLoop, prompt: "ping", interval: time.Minute})

	execCmd(m.autopilotSubmit()) // drives submitText → startTurnCmd → StartTurn
	if len(fc.startedPrompts) != 1 || fc.startedPrompts[0] != "ping" {
		t.Fatalf("loop iteration POSTed %v, want [ping]", fc.startedPrompts)
	}

	m.cmdLoop([]string{"stop"})
	if m.autopilot.active() {
		t.Error("/loop stop left the driver active")
	}
}

// TestLoopSkipsIterationWhileTurnActive ensures a tick that lands while a turn is
// still live does not POST a second, overlapping turn (which would 409).
func TestLoopSkipsIterationWhileTurnActive(t *testing.T) {
	fc := &fakeRunnerClient{}
	m := newAutopilotTranscript(fc)
	m.startAutopilot(autopilotState{kind: autopilotLoop, prompt: "ping", interval: time.Minute})
	m.turnActive = true // a turn is still running

	if cmd := m.autopilotSubmit(); cmd != nil {
		t.Error("autopilotSubmit returned a command while a turn was active (would 409)")
	}
	if len(fc.startedPrompts) != 0 {
		t.Errorf("POSTed a turn while one was active: %v", fc.startedPrompts)
	}
}

// TestStaleLoopTickIsDropped guards the generation counter: a tick scheduled by a
// loop that has since been stopped must be a no-op.
func TestStaleLoopTickIsDropped(t *testing.T) {
	m := newAutopilotTranscript(&fakeRunnerClient{})
	m.startAutopilot(autopilotState{kind: autopilotLoop, prompt: "ping", interval: time.Minute})
	stale := m.autopilot.gen
	m.stopAutopilot("")

	if cmd := m.autopilotTick(autopilotTickMsg{gen: stale}); cmd != nil {
		t.Error("a tick from a stopped loop should be dropped (got a command)")
	}
}

// TestGoalRunsUntilSentinel is the core /goal behavior: it opens with a goal
// prompt that carries the sentinel, auto-continues after a turn that does not
// report done, and stops the moment the agent emits the sentinel.
func TestGoalRunsUntilSentinel(t *testing.T) {
	fc := &fakeRunnerClient{}
	m := newAutopilotTranscript(fc)

	open := m.cmdGoal("all tests pass")
	if m.autopilot.kind != autopilotGoal {
		t.Fatalf("kind = %v, want goal", m.autopilot.kind)
	}
	execCmd(open)
	if len(fc.startedPrompts) != 1 {
		t.Fatalf("goal did not open a turn: %v", fc.startedPrompts)
	}
	if !strings.Contains(fc.startedPrompts[0], "all tests pass") ||
		!strings.Contains(fc.startedPrompts[0], goalSentinel) {
		t.Errorf("goal opening prompt missing condition/sentinel: %q", fc.startedPrompts[0])
	}

	// Turn 1 ends without the sentinel → a continuation turn is auto-sent.
	m.lastAssistantText = "made progress but two tests still fail"
	cont := m.handleEvent(mkEvent(session.EventTurnCompleted, nil))
	if !m.autopilot.active() {
		t.Fatal("goal stopped even though the sentinel was absent")
	}
	if cont == nil {
		t.Fatal("goal produced no continuation turn after an unfinished turn")
	}
	execCmd(cont)
	if len(fc.startedPrompts) != 2 || !strings.Contains(fc.startedPrompts[1], goalSentinel) {
		t.Fatalf("continuation not POSTed as expected: %v", fc.startedPrompts)
	}

	// Turn 2 reports done → the driver stops and sends no further turn.
	m.lastAssistantText = "all green now\n✅ GOAL_MET"
	done := m.handleEvent(mkEvent(session.EventTurnCompleted, nil))
	if m.autopilot.active() {
		t.Error("goal did not stop after the sentinel was emitted")
	}
	if done != nil {
		t.Error("goal sent another turn after reaching the sentinel")
	}
}

// TestGoalReached checks sentinel detection tolerates decoration but rejects an
// incidental mention.
func TestGoalReached(t *testing.T) {
	yes := []string{
		"GOAL_MET",
		"✅ GOAL_MET",
		"work done.\n**GOAL_MET**",
		"  goal_met  ", // case/space insensitive
	}
	for _, s := range yes {
		if !goalReached(s) {
			t.Errorf("goalReached(%q) = false, want true", s)
		}
	}
	no := []string{
		"",
		"still working",
		"I will print GOAL_MET when I'm finished", // extra words on the line
		"the GOAL_MET marker is what I'll emit",
	}
	for _, s := range no {
		if goalReached(s) {
			t.Errorf("goalReached(%q) = true, want false", s)
		}
	}
}

// TestAdvisorTogglesAndThreads verifies /advisor flips the flag and that the flag
// rides TurnInput.Advisor on the next turn.
func TestAdvisorTogglesAndThreads(t *testing.T) {
	fc := &fakeRunnerClient{}
	m := newAutopilotTranscript(fc)

	m.cmdAdvisor(nil)
	if !m.advisorEnabled {
		t.Fatal("first /advisor did not enable it")
	}
	execCmd(m.submitText("do a thing"))
	if len(fc.startedAdvisor) != 1 || !fc.startedAdvisor[0] {
		t.Fatalf("advisor flag not threaded to StartTurn: %v", fc.startedAdvisor)
	}

	m.cmdAdvisor(nil) // toggle back off
	if m.advisorEnabled {
		t.Fatal("second /advisor did not disable it")
	}
	m.turnActive = false
	execCmd(m.submitText("another thing"))
	if len(fc.startedAdvisor) != 2 || fc.startedAdvisor[1] {
		t.Fatalf("advisor should be off on the second turn: %v", fc.startedAdvisor)
	}
}

// TestManualSubmitStopsAutopilot: a hand-typed prompt is a takeover and must stop
// a running driver so it stops firing turns underneath the user.
func TestManualSubmitStopsAutopilot(t *testing.T) {
	m := newAutopilotTranscript(&fakeRunnerClient{})
	m.startAutopilot(autopilotState{kind: autopilotLoop, prompt: "ping", interval: time.Minute})

	m.input.SetValue("take over")
	m.submit()
	if m.autopilot.active() {
		t.Error("manual submit did not stop the running loop")
	}
}

// TestInterruptStopsAutopilot: an esc interrupt reclaims control and stops the
// driver.
func TestInterruptStopsAutopilot(t *testing.T) {
	m := newAutopilotTranscript(&fakeRunnerClient{})
	m.startAutopilot(autopilotState{kind: autopilotGoal, prompt: "ship it"})
	m.turnActive = true

	m.interruptTurn()
	if m.autopilot.active() {
		t.Error("interrupt did not stop the running goal")
	}
}

// TestDispatchAutopilotRouting: the arg-taking commands are consumed by the
// dispatcher, while ordinary palette commands fall through.
func TestDispatchAutopilotRouting(t *testing.T) {
	m := newAutopilotTranscript(&fakeRunnerClient{})

	if _, ok := m.dispatchAutopilot("/loop 5m foo"); !ok {
		t.Error("/loop with args was not consumed by the dispatcher")
	}
	if m.autopilot.kind != autopilotLoop {
		t.Errorf("dispatch did not start a loop: kind=%v", m.autopilot.kind)
	}

	if _, ok := m.dispatchAutopilot("/clear"); ok {
		t.Error("/clear should fall through to the palette, not the dispatcher")
	}
}

// lastBlockOfKind returns the text of the last block with the given kind.
func lastBlockOfKind(m *TranscriptModel, kind tblockKind) (string, bool) {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].kind == kind {
			return m.blocks[i].text, true
		}
	}
	return "", false
}

// TestLoopKeepsFiringWhileDetached is the detach-survival guarantee: after the
// user detaches, a /loop tick is routed by the App to the retained (warm) model
// and POSTs through the dashboard's live background client — the foreground
// transcript pointer is nil, yet the loop still fires.
func TestLoopKeepsFiringWhileDetached(t *testing.T) {
	app := NewApp(nil, nil, nil)
	id := session.ID("sess-loop")
	sess := transcriptSession()
	sess.State.ID = id
	sess.State.Status = session.StatusRunning
	app.dashboard.sessions = []Session{sess}

	// Attach, then arm a loop on the foreground model.
	_, _ = app.Update(attachReadyMsg{sess: sess, client: &fakeRunnerClient{}})
	fg := app.transcript
	if fg == nil {
		t.Fatal("no foreground transcript after attach")
	}
	fg.startAutopilot(autopilotState{kind: autopilotLoop, prompt: "ping", interval: time.Minute})
	gen := fg.autopilot.gen

	// Detach: the model goes warm (retained), foreground pointer clears.
	_, _ = app.Update(detachMsg{})
	if app.transcript != nil {
		t.Fatal("detach should clear the foreground transcript")
	}
	warm, ok := app.dashboard.retainedTranscript(id)
	if !ok || warm != fg {
		t.Fatal("detached loop model must stay warm (same instance) in retained")
	}

	// A background client becomes available for the session.
	bg := &fakeRunnerClient{}
	app.dashboard.liveSSEClients = map[session.ID]RunnerClient{id: bg}

	// The loop tick fires while detached. The App routes it to the warm model and
	// rewires it to the live background client, driving a turn.
	_, _ = app.Update(autopilotTickMsg{sess: id, gen: gen})
	if warm.client != bg {
		t.Error("detached loop tick did not rewire the model to the live background client")
	}
	if !warm.turnActive {
		t.Error("detached loop tick did not start a turn on the warm model")
	}
	if got, _ := lastBlockOfKind(warm, blockUser); got != "ping" {
		t.Errorf("detached loop did not enqueue the loop prompt: last user block = %q", got)
	}
}

// TestDetachedLoopSkipsWhenNoLiveClient: with no live client this cycle the loop
// must not POST, but it must reschedule so a transient blip doesn't kill it.
func TestDetachedLoopSkipsWhenNoLiveClient(t *testing.T) {
	app := NewApp(nil, nil, nil)
	id := session.ID("sess-loop2")
	sess := transcriptSession()
	sess.State.ID = id
	sess.State.Status = session.StatusRunning
	app.dashboard.sessions = []Session{sess}

	_, _ = app.Update(attachReadyMsg{sess: sess, client: &fakeRunnerClient{}})
	fg := app.transcript
	fg.startAutopilot(autopilotState{kind: autopilotLoop, prompt: "ping", interval: time.Minute})
	gen := fg.autopilot.gen
	_, _ = app.Update(detachMsg{})

	// No liveSSEClients entry for the session → the tick can't POST.
	_, cmd := app.Update(autopilotTickMsg{sess: id, gen: gen})
	if cmd == nil {
		t.Error("a live-client-less tick should still reschedule to keep the loop alive")
	}
	warm, _ := app.dashboard.retainedTranscript(id)
	if warm.turnActive {
		t.Error("loop must not start a turn when no live client is available")
	}
}

// TestGoalContinuesWhileDetached is the §1e item-1 headline fix: a /goal keeps
// chaining turns after the user detaches. A turn.completed arriving on the
// dashboard's background stream (handleRunnerEvent) — where the foreground
// continuation path is dead because its Cmds are discarded — drives the warm
// goal model's next turn, POSTing through the live background client.
func TestGoalContinuesWhileDetached(t *testing.T) {
	m := New(nil)
	id := session.ID("sess-goal")
	sess := transcriptSession()
	sess.State.ID = id
	sess.State.Status = session.StatusRunning
	m.sessions = []Session{sess}

	// A live background client; the warm model's own client is a stale parked one.
	bg := &fakeRunnerClient{}
	m.liveSSEClients[id] = bg
	tr := m.ensureRetained(sess, &fakeRunnerClient{})
	tr.startAutopilot(autopilotState{kind: autopilotGoal, prompt: "all tests pass"})
	tr.autopilot.iter = 1 // opening turn already sent

	// Turn completes without the sentinel while detached → a continuation is sent.
	tr.lastAssistantText = "two tests still fail"
	_, _ = m.handleRunnerEvent(RunnerEventMsg{ID: id, Event: mkEventSeq(5, session.EventTurnCompleted, nil)})

	if tr.client != bg {
		t.Error("detached goal continuation did not rewire the warm model to the live background client")
	}
	if !tr.autopilot.active() {
		t.Error("goal stopped even though the sentinel was absent")
	}
	if !tr.turnActive {
		t.Error("detached goal did not start a continuation turn")
	}
	if got, _ := lastBlockOfKind(tr, blockUser); !strings.Contains(got, "Continue toward the goal") {
		t.Errorf("continuation prompt not enqueued: last user block = %q", got)
	}
}

// TestDetachedGoalReachedStopsAndToasts: when a detached goal emits the sentinel,
// the driver stops and the completion surfaces as a toast + OS notification
// (§1e item 1 acceptance) instead of dying silently in a parked transcript.
func TestDetachedGoalReachedStopsAndToasts(t *testing.T) {
	m := New(nil)
	id := session.ID("sess-goaldone")
	sess := transcriptSession()
	sess.State.ID = id
	sess.State.Status = session.StatusRunning
	m.sessions = []Session{sess}
	m.liveSSEClients[id] = &fakeRunnerClient{}
	tr := m.ensureRetained(sess, &fakeRunnerClient{})
	tr.startAutopilot(autopilotState{kind: autopilotGoal, prompt: "ship it"})
	tr.autopilot.iter = 1

	tr.lastAssistantText = "shipped\n✅ GOAL_MET"
	// No liveSSEChannel registered → handleRunnerEvent returns just the toast Cmd.
	_, cmd := m.handleRunnerEvent(RunnerEventMsg{ID: id, Event: mkEventSeq(4, session.EventTurnCompleted, nil)})

	if tr.autopilot.active() {
		t.Error("detached goal did not stop on the sentinel")
	}
	if cmd == nil {
		t.Fatal("goal-reached while detached produced no toast cmd")
	}
	if _, ok := cmd().(toastMsg); !ok {
		t.Errorf("goal-reached cmd = %T, want toastMsg", cmd())
	}
	if !m.notifiedAttention[id] {
		t.Error("autopilot toast should pre-mark the session notified so the generic pass doesn't clobber it")
	}
}

// TestLoopStopsOnSentinelForeground is §1e item 2: a /loop whose turn reports the
// sentinel stops instead of burning a turn every interval forever.
func TestLoopStopsOnSentinelForeground(t *testing.T) {
	m := newAutopilotTranscript(&fakeRunnerClient{})
	m.startAutopilot(autopilotState{kind: autopilotLoop, prompt: "work the backlog", interval: time.Minute})

	m.lastAssistantText = "backlog empty\nGOAL_MET"
	m.handleEvent(mkEvent(session.EventTurnCompleted, nil))
	if m.autopilot.active() {
		t.Error("foreground loop did not stop after the sentinel")
	}
}

// TestDetachedLoopStopsOnSentinel is §1e item 2 for the detached case: the loop's
// turn.completed on the background stream terminates the driver and toasts.
func TestDetachedLoopStopsOnSentinel(t *testing.T) {
	m := New(nil)
	id := session.ID("sess-loopdone")
	sess := transcriptSession()
	sess.State.ID = id
	sess.State.Status = session.StatusRunning
	m.sessions = []Session{sess}
	m.liveSSEClients[id] = &fakeRunnerClient{}
	tr := m.ensureRetained(sess, &fakeRunnerClient{})
	tr.startAutopilot(autopilotState{kind: autopilotLoop, prompt: "work the backlog", interval: time.Minute})

	tr.lastAssistantText = "nothing left\nGOAL_MET"
	_, cmd := m.handleRunnerEvent(RunnerEventMsg{ID: id, Event: mkEventSeq(6, session.EventTurnCompleted, nil)})

	if tr.autopilot.active() {
		t.Error("detached loop did not stop after the sentinel")
	}
	if cmd == nil {
		t.Fatal("loop termination while detached produced no toast cmd")
	}
	if _, ok := cmd().(toastMsg); !ok {
		t.Errorf("loop-finished cmd = %T, want toastMsg", cmd())
	}
}

// TestLoopWarnsWhenIntervalExceedsIdleTimeout is §1e item 4: a /loop interval at
// or beyond the reaper idle timeout warns that the pod may suspend mid-loop.
func TestLoopWarnsWhenIntervalExceedsIdleTimeout(t *testing.T) {
	warned := func(m *TranscriptModel) bool {
		for _, b := range m.blocks {
			if b.kind == blockInfo && strings.Contains(b.text, "idle timeout") {
				return true
			}
		}
		return false
	}

	m := newAutopilotTranscript(&fakeRunnerClient{})
	m.idleTimeout = 5 * time.Minute
	m.cmdLoop([]string{"10m", "keep", "working"})
	if !warned(m) {
		t.Error("interval ≥ idle timeout should warn about a mid-loop suspend")
	}

	// A safely-short interval must not warn.
	m2 := newAutopilotTranscript(&fakeRunnerClient{})
	m2.idleTimeout = 30 * time.Minute
	m2.cmdLoop([]string{"5m", "keep", "working"})
	if warned(m2) {
		t.Error("interval well under the idle timeout should not warn")
	}
}

// TestEscStopsIdleAutopilot is §1e item 5: with a driver armed but no live turn to
// interrupt (idle between ticks), esc stops the driver rather than detaching with
// it still running. escapeConsumes must report the local meaning too.
func TestEscStopsIdleAutopilot(t *testing.T) {
	m := newAutopilotTranscript(&fakeRunnerClient{})
	m.layout()
	m.startAutopilot(autopilotState{kind: autopilotLoop, prompt: "ping", interval: time.Minute})

	if !m.escapeConsumes() {
		t.Fatal("an armed loop must consume esc so the App delegates it (esc-to-stop), not detach")
	}
	m.handleKey(keyMsg("esc"))
	if m.autopilot.active() {
		t.Error("esc did not stop the idle loop driver")
	}
}

// TestAutopilotLapseToastWhenModelGone is §1e item 3: when a loop tick finds its
// warm model gone (pod suspended / stream exhausted), the lapse surfaces as a
// toast instead of the loop vanishing without a trace.
func TestAutopilotLapseToastWhenModelGone(t *testing.T) {
	app := NewApp(nil, nil, nil)
	id := session.ID("sess-lapse")
	sess := transcriptSession()
	sess.State.ID = id
	sess.State.Status = session.StatusSuspended
	app.dashboard.sessions = []Session{sess}
	// No retained model for id (dropRetained already fired on suspend).

	cmd := app.autopilotTick(autopilotTickMsg{sess: id, gen: 1})
	if cmd == nil {
		t.Fatal("a lapsed loop (warm model gone) must surface a toast, not vanish silently")
	}
	if _, ok := cmd().(toastMsg); !ok {
		t.Errorf("lapse cmd = %T, want toastMsg", cmd())
	}
}

// TestHumanInterval guards the compact duration formatting (the naive
// suffix-trim bug turned "10m0s" into "1").
func TestHumanInterval(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{10 * time.Minute, "10m"},
		{5 * time.Minute, "5m"},
		{time.Hour + 30*time.Minute, "1h30m"},
		{45 * time.Second, "45s"},
		{time.Hour, "1h"},
		{90 * time.Second, "1m30s"},
	}
	for _, c := range cases {
		if got := humanInterval(c.d); got != c.want {
			t.Errorf("humanInterval(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

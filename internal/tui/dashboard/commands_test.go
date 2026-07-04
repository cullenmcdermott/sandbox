package dashboard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// TestModelSwitchUpdatesStatuslineImmediately guards T8: a /model switch must
// reflect in the status-line model name + ctx window right away, not only after
// the next turn's session.started.
func TestModelSwitchUpdatesStatuslineImmediately(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24

	// First session.started establishes the account default (Haiku, 200k window).
	startPayload, _ := json.Marshal(session.SessionStartedPayload{Model: "claude-haiku-4-5"})
	m.handleEvent(session.Event{Type: session.EventSessionStarted, Payload: startPayload})
	if m.defaultModel != "claude-haiku-4-5" {
		t.Fatalf("defaultModel = %q, want claude-haiku-4-5", m.defaultModel)
	}
	if m.ctxLimit != 200_000 {
		t.Fatalf("default ctxLimit = %d, want 200000", m.ctxLimit)
	}

	// /opus updates the displayed model + ctx window immediately (200k → 1M).
	m.input.SetValue("/opus")
	m.handleKey(keyMsg("enter"))
	if m.model != "opus" {
		t.Errorf("after /opus, model = %q, want opus", m.model)
	}
	if m.ctxLimit != 1_000_000 {
		t.Errorf("after /opus, ctxLimit = %d, want 1000000 (window must track the switch)", m.ctxLimit)
	}

	// /model-default restores the captured default and its window (1M → 200k).
	m.input.SetValue("/model-default")
	m.handleKey(keyMsg("enter"))
	if m.model != "claude-haiku-4-5" {
		t.Errorf("after /model-default, model = %q, want claude-haiku-4-5", m.model)
	}
	if m.ctxLimit != 200_000 {
		t.Errorf("after /model-default, ctxLimit = %d, want 200000", m.ctxLimit)
	}
}

func TestSlashFilter(t *testing.T) {
	// Fresh model: no models.available yet, so the Model group uses the alias
	// fallback (/opus /sonnet /haiku /model-default).
	mf := &TranscriptModel{}
	if got := len(filteredGroups(mf, "")); got != 7 {
		t.Errorf("empty query groups = %d, want 7 (Session/Mode/Autopilot/Model/Effort/Tools/Help)", got)
	}
	cmds := flatCmds(mf, "plan")
	if len(cmds) != 1 || cmds[0].name != "/plan" {
		t.Errorf("filter 'plan' = %v, want [/plan]", cmds)
	}
	// The Model group adds a /sonnet switch (and matches by name substring).
	if c := flatCmds(mf, "/sonnet"); len(c) != 1 || c[0].name != "/sonnet" {
		t.Errorf("filter '/sonnet' = %v, want [/sonnet]", c)
	}
	// Typing "model" matches the "Model" group name, so all four model commands
	// (/opus, /sonnet, /haiku, /model-default) should appear — not just
	// /model-default (the only one with "model" in its own name/desc). This is
	// the bug that made /model appear broken: the palette collapsed to one entry
	// and pressing Enter accidentally ran /model-default.
	if c := flatCmds(mf, "model"); len(c) != 4 {
		t.Errorf("filter 'model' (group-name match) = %d cmds, want 4 (/opus /sonnet /haiku /model-default)", len(c))
	}
	// Likewise typing "effort" matches the "Effort" group name, surfacing all six
	// static levels (low/medium/high/xhigh/ultracode/auto), not just those with
	// "effort" in their own name.
	if c := flatCmds(mf, "effort"); len(c) != 6 {
		t.Errorf("filter 'effort' (group-name match) = %d cmds, want 6", len(c))
	}
	// Filtering matches descriptions too ("/clear" desc has "transcript").
	if len(flatCmds(mf, "transcript")) == 0 {
		t.Errorf("description filter found nothing for 'transcript'")
	}
	if len(flatCmds(mf, "zzznope")) != 0 {
		t.Errorf("nonsense query should match nothing")
	}
}

func TestSlashPaletteNavigation(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.input.SetValue("/")
	if !m.paletteOpen() {
		t.Fatal("palette should be open for '/'")
	}
	// down advances the selection but is clamped to the flat list length.
	n := len(flatCmds(m, ""))
	for i := 0; i < n+5; i++ {
		m.handleKey(keyMsg("down"))
	}
	if m.cmdSel != n-1 {
		t.Errorf("cmdSel = %d, want clamped to %d", m.cmdSel, n-1)
	}
	m.handleKey(keyMsg("up"))
	if m.cmdSel != n-2 {
		t.Errorf("cmdSel after up = %d, want %d", m.cmdSel, n-2)
	}
}

// ORACLE: a models.available event populates m.availableModels.
func TestModelsAvailableEventPopulatesModel(t *testing.T) {
	m := &TranscriptModel{}
	payload, err := json.Marshal(session.ModelsAvailablePayload{Models: []session.ModelInfo{
		{Value: "claude-opus-4-8", DisplayName: "Opus 4.8", Description: "most capable"},
		{Value: "claude-sonnet-4-6", DisplayName: "Sonnet 4.6"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	m.handleEvent(session.Event{Type: session.EventModelsAvailable, Payload: payload})
	if len(m.availableModels) != 2 || m.availableModels[0].Value != "claude-opus-4-8" {
		t.Fatalf("availableModels = %+v, want 2 entries led by claude-opus-4-8", m.availableModels)
	}
}

// COUNTER: once models.available has landed, the /model palette lists the real
// account models by slug (not the opus/sonnet/haiku aliases), keeps
// /model-default last, and each entry's handler selects the full model id.
func TestModelPaletteUsesDynamicListWhenPresent(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.availableModels = []session.ModelInfo{
		{Value: "claude-opus-4-8", DisplayName: "Opus 4.8", Description: "most capable"},
		{Value: "claude-haiku-4-5", DisplayName: "Haiku 4.5"},
	}
	cmds := flatCmds(m, "model") // group-name match → whole Model group
	var names []string
	for _, c := range cmds {
		names = append(names, c.name)
	}
	want := []string{"/opus-4-8", "/haiku-4-5", "/model-default"}
	if len(names) != len(want) {
		t.Fatalf("dynamic model palette = %v, want %v", names, want)
	}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("model cmd[%d] = %q, want %q", i, names[i], w)
		}
	}
	// No bare aliases when the dynamic list is present.
	for _, c := range cmds {
		if c.name == "/opus" || c.name == "/sonnet" || c.name == "/haiku" {
			t.Errorf("dynamic palette should not include bare alias %q", c.name)
		}
	}
	// The first entry's handler selects the FULL model id, not the slug.
	cmds[0].run(m)
	if m.modelOverride != "claude-opus-4-8" {
		t.Errorf("dynamic /opus-4-8 set modelOverride=%q, want claude-opus-4-8", m.modelOverride)
	}
}

// COUNTER: with no models.available yet, the palette falls back to the stable
// opus/sonnet/haiku aliases (nil model also yields the fallback).
func TestModelPaletteFallsBackToAliases(t *testing.T) {
	for _, m := range []*TranscriptModel{nil, {}} {
		got := modelGroupCmds(m)
		want := []string{"/opus", "/sonnet", "/haiku", "/model-default"}
		if len(got) != len(want) {
			t.Fatalf("fallback model cmds = %d, want %d", len(got), len(want))
		}
		for i, w := range want {
			if got[i].name != w {
				t.Errorf("fallback cmd[%d] = %q, want %q", i, got[i].name, w)
			}
		}
	}
}

// ORACLE (in-session /effort): the static Effort palette records the SDK wire
// value as the per-turn override and threads it onto the next turn. The crux is
// the label→wire mapping — /effort-ultracode stores "max", not "ultracode" — and
// that /effort-auto clears the override back to the SDK adaptive default.
func TestEffortOverrideThreadedToTurn(t *testing.T) {
	fc := &fakeRunnerClient{}
	m := NewTranscript(fc, transcriptSession(), nil)
	m.width, m.height = 80, 24

	// A prompt before any /effort selection sends an empty effort.
	startTurnCmd(fc, m.ref, "first", m.mode.apiValue(), m.modelOverride, m.effortOverride, false)()
	if len(fc.startedEfforts) != 1 || fc.startedEfforts[0] != "" {
		t.Fatalf("default effort = %v, want one empty entry", fc.startedEfforts)
	}

	// /effort-high sets the override to the verbatim SDK level.
	m.input.SetValue("/effort-high")
	m.handleKey(keyMsg("enter"))
	if m.effortOverride != "high" {
		t.Fatalf("effortOverride after /effort-high = %q, want high", m.effortOverride)
	}

	// /effort-ultracode stores the WIRE value "max" (ultracode is only the label).
	m.input.SetValue("/effort-ultracode")
	m.handleKey(keyMsg("enter"))
	if m.effortOverride != "max" {
		t.Fatalf("effortOverride after /effort-ultracode = %q, want max", m.effortOverride)
	}

	// The next turn carries the selected effort.
	startTurnCmd(fc, m.ref, "second", m.mode.apiValue(), m.modelOverride, m.effortOverride, false)()
	if got := fc.startedEfforts[len(fc.startedEfforts)-1]; got != "max" {
		t.Errorf("turn effort = %q, want max", got)
	}

	// /effort-auto clears the override (SDK adaptive default).
	m.input.SetValue("/effort-auto")
	m.handleKey(keyMsg("enter"))
	if m.effortOverride != "" {
		t.Errorf("effortOverride after /effort-auto = %q, want empty", m.effortOverride)
	}
}

func TestModelSlug(t *testing.T) {
	cases := map[string]string{
		"claude-opus-4-8":           "opus-4-8",
		"claude-3-5-haiku-20241022": "3-5-haiku-20241022",
		"some-other-model":          "some-other-model",
		"Opus 4":                    "opus-4",
	}
	for in, want := range cases {
		if got := modelSlug(in); got != want {
			t.Errorf("modelSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlashDispatchSwitchesMode(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.input.SetValue("/plan")
	if m.mode == modePlan {
		t.Fatal("precondition: default mode should not be plan")
	}
	m.handleKey(keyMsg("enter"))
	if m.mode != modePlan {
		t.Errorf("/plan did not switch mode: %v", m.mode)
	}
	if m.input.Value() != "" {
		t.Errorf("input not reset after running a command: %q", m.input.Value())
	}
}

func TestSlashUnknownCommandHint(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.input.SetValue("/zzznope")
	m.handleKey(keyMsg("enter"))
	found := false
	for _, b := range m.blocks {
		if b.kind == blockInfo && strings.Contains(b.text, "unknown command") {
			found = true
		}
	}
	if !found {
		t.Errorf("unknown command did not produce a hint block")
	}
}

func TestShellPassthrough(t *testing.T) {
	fc := &fakeRunnerClient{execResult: &session.ExecResult{Stdout: "clean\n", ExitCode: 0}}
	m := NewTranscript(fc, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.imode = modeInsert // typing a prompt happens in INSERT mode
	m.input.SetValue("!git status")

	_, cmd := m.handleKey(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("shell command returned no tea.Cmd")
	}
	if m.input.Value() != "" {
		t.Errorf("input not reset after !cmd")
	}
	// Running the cmd hits the runner exec endpoint and yields a shellResultMsg.
	msg := cmd()
	sr, ok := msg.(shellResultMsg)
	if !ok {
		t.Fatalf("expected shellResultMsg, got %T", msg)
	}
	if sr.command != "git status" {
		t.Errorf("exec command = %q, want 'git status'", sr.command)
	}
	if len(fc.execCommands) != 1 || fc.execCommands[0] != "git status" {
		t.Errorf("Exec not called with the command: %v", fc.execCommands)
	}
	// Delivering the result renders a distinct shell block.
	m.Update(sr)
	found := false
	for _, b := range m.blocks {
		if b.kind == blockShell && strings.Contains(b.text, "git status") {
			found = true
		}
	}
	if !found {
		t.Errorf("no shell block rendered for !git status: %+v", m.blocks)
	}
}

func TestSlashDiffUsesExec(t *testing.T) {
	fc := &fakeRunnerClient{execResult: &session.ExecResult{Stdout: "diff --git ...\n", ExitCode: 0}}
	m := NewTranscript(fc, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.input.SetValue("/diff")
	_, cmd := m.handleKey(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("/diff returned no command")
	}
	if msg, ok := cmd().(shellResultMsg); !ok {
		t.Fatalf("/diff should exec, got %T", msg)
	}
	if len(fc.execCommands) != 1 || !strings.Contains(fc.execCommands[0], "git") {
		t.Errorf("/diff did not run git diff: %v", fc.execCommands)
	}
}

// ORACLE: /clear resets unreadIndex to 0 so the divider doesn't land at a
// stale position after the block list shrinks to nil. [B16]
func TestSlashClearResetsUnreadIndex(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	// Seed some blocks and a stale unreadIndex.
	m.appendBlock(blockInfo, "one")
	m.appendBlock(blockInfo, "two")
	m.appendBlock(blockInfo, "three")
	m.unreadIndex = 2 // stale: points past the end after /clear

	// Open the palette and select /clear by entering the full command.
	m.input.SetValue("/clear")
	if !m.paletteOpen() {
		t.Fatal("palette not open after SetValue('/clear')")
	}
	// Run /clear via paletteKey directly with the enter key message.
	m.paletteKey(keyMsg("enter"))

	if m.unreadIndex != 0 {
		t.Errorf("unreadIndex after /clear = %d, want 0", m.unreadIndex)
	}
	if len(m.blocks) != 0 {
		t.Errorf("blocks after /clear = %d, want 0", len(m.blocks))
	}
}

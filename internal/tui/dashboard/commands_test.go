package dashboard

import (
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

func TestSlashFilter(t *testing.T) {
	if got := len(filteredGroups("")); got != 5 {
		t.Errorf("empty query groups = %d, want 5 (Session/Mode/Model/Tools/Help)", got)
	}
	cmds := flatCmds("plan")
	if len(cmds) != 1 || cmds[0].name != "/plan" {
		t.Errorf("filter 'plan' = %v, want [/plan]", cmds)
	}
	// The Model group adds a /sonnet switch (and matches by name substring).
	if c := flatCmds("/sonnet"); len(c) != 1 || c[0].name != "/sonnet" {
		t.Errorf("filter '/sonnet' = %v, want [/sonnet]", c)
	}
	// Filtering matches descriptions too ("/clear" desc has "transcript").
	if len(flatCmds("transcript")) == 0 {
		t.Errorf("description filter found nothing for 'transcript'")
	}
	if len(flatCmds("zzznope")) != 0 {
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
	n := len(flatCmds(""))
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

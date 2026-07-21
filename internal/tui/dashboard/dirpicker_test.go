package dashboard

// dirpicker_test.go — the create overlay's project-directory stage (T10):
// stage order and navigation, recents assembly (dedup/cap), fail-closed
// validation with inline errors, free-text entry with ~-expansion and
// Tab-completion, and ProjectPath threading into CreateParams. Style mirrors
// account_picker_test.go (headless App + creator params channel).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cullenmcdermott/sandbox/internal/projpath"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// newDirPickerApp builds a headless App whose directory-picker environment is
// pinned to temp dirs (deterministic, no real cwd/home dependence) and whose
// Creator reports its CreateParams over the returned channel. workDir is a
// fresh existing directory; homeDir a separate one.
func newDirPickerApp(t *testing.T, recents func() []string) (*App, chan CreateParams) {
	t.Helper()
	ch := make(chan CreateParams, 1)
	creator := func(_ context.Context, params CreateParams, _ func(ConnectStage, string)) (CreateResult, error) {
		ch <- params
		return CreateResult{
			State:  session.State{ID: "new1", Backend: params.Backend},
			Client: &fakeRunnerClient{},
		}, nil
	}
	app := NewApp(nil, nil, creator)
	app.workDir = mustCanon(t, t.TempDir())
	app.homeDir = t.TempDir()
	app.recentProjects = recents
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	return app, ch
}

// mustCanon canonicalizes a test dir the way the picker will (macOS's temp
// root is a symlink, so raw t.TempDir() strings never round-trip).
func mustCanon(t *testing.T, dir string) string {
	t.Helper()
	p, err := projpath.Canonicalize(dir)
	if err != nil {
		t.Fatalf("canonicalize %s: %v", dir, err)
	}
	return p
}

// createOpencode drives the backend stage to an opencode create (no account
// stage) and returns the params the Creator saw.
func createOpencode(t *testing.T, app *App, ch chan CreateParams) CreateParams {
	t.Helper()
	if app.picker.stage != stageBackend {
		t.Fatalf("not on the backend stage: %v", app.picker.stage)
	}
	app.Update(keyMsg("down")) // opencode
	app.Update(keyMsg("enter"))
	return waitParams(t, ch)
}

func TestDirPickerOpensFirstAndDefaultsToCwd(t *testing.T) {
	app, _ := newDirPickerApp(t, nil)

	app.Update(createSessionMsg{})
	if !app.picker.open || app.picker.stage != stageDir {
		t.Fatalf("createSessionMsg should open on the directory stage: open=%v stage=%v", app.picker.open, app.picker.stage)
	}
	rows := app.picker.dirRows
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (cwd + other)", len(rows))
	}
	if rows[0].kind != dirRowCwd || rows[0].path != app.workDir {
		t.Errorf("row 0 = %+v, want the cwd row for %q", rows[0], app.workDir)
	}
	if rows[1].kind != dirRowOther {
		t.Errorf("last row = %+v, want the free-text row", rows[1])
	}
	if app.picker.sel != 0 {
		t.Errorf("sel = %d, want 0 (cwd preselected)", app.picker.sel)
	}
	content := app.View().Content
	for _, want := range []string{"project directory", "current dir", "other path"} {
		if !strings.Contains(content, want) {
			t.Errorf("directory stage missing %q", want)
		}
	}
}

func TestDirPickerCwdRowThreadsProjectPath(t *testing.T) {
	app, ch := newDirPickerApp(t, nil)

	app.Update(createSessionMsg{})
	app.Update(keyMsg("enter")) // accept cwd → backend stage
	p := createOpencode(t, app, ch)
	if p.Backend != session.BackendOpenCode {
		t.Errorf("backend = %q, want opencode", p.Backend)
	}
	if p.ProjectPath != app.workDir {
		t.Errorf("ProjectPath = %q, want the cwd %q", p.ProjectPath, app.workDir)
	}
}

func TestDirPickerRecentRowsDedupedAndThreaded(t *testing.T) {
	dirB := t.TempDir()
	dirC := t.TempDir()
	app, ch := newDirPickerApp(t, nil)
	// Recents repeat the cwd and each other; rows must dedup, order preserved.
	app.recentProjects = func() []string { return []string{app.workDir, dirB, dirB, dirC} }

	app.Update(createSessionMsg{})
	rows := app.picker.dirRows
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4 (cwd + B + C + other): %+v", len(rows), rows)
	}
	if rows[1].kind != dirRowRecent || rows[1].path != dirB {
		t.Errorf("row 1 = %+v, want recent %q", rows[1], dirB)
	}
	if rows[2].kind != dirRowRecent || rows[2].path != dirC {
		t.Errorf("row 2 = %+v, want recent %q", rows[2], dirC)
	}

	// Select dirB: down, enter → backend stage; the create threads its canonical form.
	app.Update(keyMsg("down"))
	app.Update(keyMsg("enter"))
	p := createOpencode(t, app, ch)
	if want := mustCanon(t, dirB); p.ProjectPath != want {
		t.Errorf("ProjectPath = %q, want the recent %q", p.ProjectPath, want)
	}
}

func TestDirPickerRecentsCapped(t *testing.T) {
	var many []string
	for i := 0; i < 9; i++ {
		many = append(many, filepath.Join("/proj", string(rune('a'+i))))
	}
	app, _ := newDirPickerApp(t, func() []string { return many })

	app.Update(createSessionMsg{})
	// cwd + maxRecentDirRows recents + other.
	if got, want := len(app.picker.dirRows), 1+maxRecentDirRows+1; got != want {
		t.Errorf("rows = %d, want %d (recents capped at %d)", got, want, maxRecentDirRows)
	}
}

func TestDirPickerMissingRecentInlineError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	app, ch := newDirPickerApp(t, func() []string { return []string{missing} })

	app.Update(createSessionMsg{})
	app.Update(keyMsg("down")) // the missing recent
	app.Update(keyMsg("enter"))
	if app.picker.stage != stageDir {
		t.Fatalf("a missing dir must keep the directory stage open: stage=%v", app.picker.stage)
	}
	if app.picker.formErr == nil {
		t.Fatal("missing-dir validation error was not recorded for inline display")
	}
	if !strings.Contains(app.View().Content, "does not exist") {
		t.Error("missing-dir error is not rendered inline")
	}
	select {
	case p := <-ch:
		t.Fatalf("missing dir still created a session: %+v", p)
	case <-time.After(50 * time.Millisecond):
	}

	// Recovering: pick the cwd row instead — the error clears and the flow proceeds.
	app.Update(keyMsg("up"))
	app.Update(keyMsg("enter"))
	if app.picker.stage != stageBackend || app.picker.formErr != nil {
		t.Errorf("cwd after error: stage=%v formErr=%v, want stageBackend and nil", app.picker.stage, app.picker.formErr)
	}
}

func TestDirPickerFreeTextValidatesAndThreads(t *testing.T) {
	app, ch := newDirPickerApp(t, nil)
	target := t.TempDir()

	app.Update(createSessionMsg{})
	app.Update(keyMsg("down")) // other path
	app.Update(keyMsg("enter"))
	if app.picker.stage != stageDirInput {
		t.Fatalf("other-path row did not open the input: stage=%v", app.picker.stage)
	}

	// Invalid path: inline error, stage stays.
	app.picker.input.SetValue(filepath.Join(target, "nope"))
	app.Update(keyMsg("enter"))
	if app.picker.stage != stageDirInput || app.picker.formErr == nil {
		t.Fatalf("invalid path: stage=%v formErr=%v, want stageDirInput and an error", app.picker.stage, app.picker.formErr)
	}
	if !strings.Contains(app.View().Content, "does not exist") {
		t.Error("invalid-path error is not rendered inline")
	}

	// Valid path: advances to the backend stage and threads through the create.
	app.picker.input.SetValue(target)
	app.Update(keyMsg("enter"))
	if app.picker.stage != stageBackend {
		t.Fatalf("valid path did not advance: stage=%v", app.picker.stage)
	}
	p := createOpencode(t, app, ch)
	if want := mustCanon(t, target); p.ProjectPath != want {
		t.Errorf("ProjectPath = %q, want the typed %q", p.ProjectPath, want)
	}
}

func TestDirPickerFreeTextTildeExpands(t *testing.T) {
	app, ch := newDirPickerApp(t, nil)
	proj := filepath.Join(app.homeDir, "proj")
	if err := os.Mkdir(proj, 0o755); err != nil {
		t.Fatal(err)
	}

	app.Update(createSessionMsg{})
	app.Update(keyMsg("down"))
	app.Update(keyMsg("enter")) // → input
	app.picker.input.SetValue("~/proj")
	app.Update(keyMsg("enter"))
	if app.picker.stage != stageBackend {
		t.Fatalf("~ path did not validate: stage=%v formErr=%v", app.picker.stage, app.picker.formErr)
	}
	p := createOpencode(t, app, ch)
	if want := mustCanon(t, proj); p.ProjectPath != want {
		t.Errorf("ProjectPath = %q, want the expanded %q", p.ProjectPath, want)
	}
}

func TestDirPickerTabCompletes(t *testing.T) {
	app, _ := newDirPickerApp(t, nil)
	base := app.workDir
	for _, d := range []string{"alpha", "alphabet", "beta"} {
		if err := os.Mkdir(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	app.Update(createSessionMsg{})
	app.Update(keyMsg("down"))
	app.Update(keyMsg("enter")) // → input

	// Ambiguous: extends to the longest common prefix.
	app.picker.input.SetValue(filepath.Join(base, "al"))
	app.Update(keyMsg("tab"))
	if got, want := app.picker.input.Value(), filepath.Join(base, "alpha"); got != want {
		t.Errorf("tab(al) = %q, want the common prefix %q", got, want)
	}

	// Unique: completes fully with a drill-in separator.
	app.picker.input.SetValue(filepath.Join(base, "be"))
	app.Update(keyMsg("tab"))
	if got, want := app.picker.input.Value(), filepath.Join(base, "beta")+string(filepath.Separator); got != want {
		t.Errorf("tab(be) = %q, want %q", got, want)
	}

	// No match: unchanged.
	before := filepath.Join(base, "zz")
	app.picker.input.SetValue(before)
	app.Update(keyMsg("tab"))
	if got := app.picker.input.Value(); got != before {
		t.Errorf("tab(zz) = %q, want unchanged %q", got, before)
	}
}

func TestDirPickerFreeTextAcceptsPaste(t *testing.T) {
	app, ch := newDirPickerApp(t, nil)
	target := t.TempDir()

	app.Update(createSessionMsg{})
	app.Update(keyMsg("down"))
	app.Update(keyMsg("enter")) // → input
	app.picker.input.SetValue("")
	app.Update(tea.PasteMsg{Content: target})
	app.Update(keyMsg("enter"))
	if app.picker.stage != stageBackend {
		t.Fatalf("pasted path did not validate: stage=%v formErr=%v", app.picker.stage, app.picker.formErr)
	}
	p := createOpencode(t, app, ch)
	if want := mustCanon(t, target); p.ProjectPath != want {
		t.Errorf("ProjectPath = %q, want the pasted %q", p.ProjectPath, want)
	}
}

func TestDirPickerEscChain(t *testing.T) {
	app, _ := newDirPickerApp(t, nil)

	// input → esc → directory rows.
	app.Update(createSessionMsg{})
	app.Update(keyMsg("down"))
	app.Update(keyMsg("enter")) // → input
	app.Update(keyMsg("esc"))
	if app.picker.stage != stageDir {
		t.Fatalf("esc from input: stage=%v, want stageDir", app.picker.stage)
	}

	// backend → esc → directory rows with the accepted selection restored.
	app.Update(keyMsg("enter")) // accept cwd (sel back on row 0)
	if app.picker.stage != stageBackend {
		t.Fatalf("did not reach the backend stage: %v", app.picker.stage)
	}
	app.Update(keyMsg("esc"))
	if app.picker.stage != stageDir {
		t.Fatalf("esc from backend: stage=%v, want stageDir", app.picker.stage)
	}
	if app.picker.sel != 0 {
		t.Errorf("esc from backend: sel=%d, want 0 (the accepted cwd row)", app.picker.sel)
	}

	// directory rows → esc → closed, nothing provisioned.
	app.Update(keyMsg("esc"))
	if app.picker.open {
		t.Error("esc from the directory stage did not close the overlay")
	}
	if app.screen != ScreenDashboard {
		t.Errorf("screen = %v, want ScreenDashboard", app.screen)
	}
}

// TestDirPickerFreeTextEscFromBackendSelectsOtherRow: after accepting a
// free-text path (not a listed row), esc from the backend stage lands the
// selection on the free-text row — where the user actually was.
func TestDirPickerFreeTextEscFromBackendSelectsOtherRow(t *testing.T) {
	app, _ := newDirPickerApp(t, nil)
	target := t.TempDir()

	app.Update(createSessionMsg{})
	app.Update(keyMsg("down"))
	app.Update(keyMsg("enter")) // → input
	app.picker.input.SetValue(target)
	app.Update(keyMsg("enter")) // → backend
	app.Update(keyMsg("esc"))   // → back to rows
	if app.picker.stage != stageDir {
		t.Fatalf("stage=%v, want stageDir", app.picker.stage)
	}
	if want := len(app.picker.dirRows) - 1; app.picker.sel != want {
		t.Errorf("sel=%d, want the free-text row %d", app.picker.sel, want)
	}
}

// TestDirPickerProjectPathSurvivesAccountStage: the accepted directory threads
// through the deeper claude path (backend → account → host login) via the
// beginCreate funnel, not just the immediate opencode create.
func TestDirPickerProjectPathSurvivesAccountStage(t *testing.T) {
	app, ch := newDirPickerApp(t, nil)
	app.accountStore = &fakeAccountStore{accounts: nil}

	app.Update(createSessionMsg{})
	app.Update(keyMsg("enter")) // cwd → backend
	app.Update(keyMsg("enter")) // claude → account stage
	if app.picker.stage != stageAccount {
		t.Fatalf("did not reach the account stage: %v", app.picker.stage)
	}
	app.Update(keyMsg("enter")) // host claude login → create
	p := waitParams(t, ch)
	if p.Backend != session.BackendClaudePane || p.AnthropicAccountID != "" {
		t.Errorf("params = %+v, want {claude-pane, \"\"}", p)
	}
	if p.ProjectPath != app.workDir {
		t.Errorf("ProjectPath = %q, want %q (must survive the account stage)", p.ProjectPath, app.workDir)
	}
}

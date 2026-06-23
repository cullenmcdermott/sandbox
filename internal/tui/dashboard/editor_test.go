package dashboard

import (
	"errors"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

func TestApplyEditorResult(t *testing.T) {
	cases := []struct {
		name    string
		initial string
		msg     editorResultMsg
		want    string
	}{
		{
			name:    "success sets text",
			initial: "old prompt",
			msg:     editorResultMsg{text: "edited prompt"},
			want:    "edited prompt",
		},
		{
			name:    "success can clear text",
			initial: "old prompt",
			msg:     editorResultMsg{text: ""},
			want:    "",
		},
		{
			name:    "error leaves input unchanged",
			initial: "keep me",
			msg:     editorResultMsg{err: errors.New("editor exited"), text: "ignored"},
			want:    "keep me",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
			m.input.SetValue(tc.initial)
			m.applyEditorResult(tc.msg)
			if got := m.input.Value(); got != tc.want {
				t.Fatalf("input = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderTodos(t *testing.T) {
	out := renderTodos([]session.TodoItem{
		{Content: "write the code", Status: "completed"},
		{Content: "run the tests", Status: "in_progress", ActiveForm: "running the tests"},
		{Content: "open a PR", Status: "pending"},
	})
	for _, want := range []string{"✓ write the code", "▸ running the tests", "○ open a PR"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderTodos output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "run the tests") {
		t.Errorf("in_progress item should prefer ActiveForm, got:\n%s", out)
	}

	if got := renderTodos(nil); !strings.Contains(got, "cleared") {
		t.Errorf("empty list should note cleared, got %q", got)
	}
}

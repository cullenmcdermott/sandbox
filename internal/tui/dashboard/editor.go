package dashboard

// editor.go — $EDITOR prompt composition helper (slice 5g). Pressing ctrl+o in
// the chat opens the user's preferred editor with the current prompt buffer;
// on save the content is read back into the input.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// editorResultMsg carries the edited prompt text (or an error) back to the model.
type editorResultMsg struct {
	text string
	err  error
}

// openEditorPrompt writes the current input to a temp file and opens $EDITOR
// via tea.ExecProcess, which suspends Bubble Tea's terminal ownership for the
// duration and restores it afterwards. Running the editor inside a plain
// tea.Cmd would fight the program for the raw-mode terminal and garble both.
func (m *TranscriptModel) openEditorPrompt() tea.Cmd {
	tmp, err := os.CreateTemp("", "sandbox-prompt-*.md")
	if err != nil {
		return func() tea.Msg { return editorResultMsg{err: fmt.Errorf("create temp file: %w", err)} }
	}
	if _, err := tmp.WriteString(m.input.Value()); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return func() tea.Msg { return editorResultMsg{err: fmt.Errorf("write temp file: %w", err)} }
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return func() tea.Msg { return editorResultMsg{err: fmt.Errorf("close temp file: %w", err)} }
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	path := tmp.Name()
	c := exec.Command("sh", "-c", fmt.Sprintf("%s %s", editor, path))
	return tea.ExecProcess(c, func(err error) tea.Msg {
		defer os.Remove(path)
		if err != nil {
			return editorResultMsg{err: fmt.Errorf("editor exited: %w", err)}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return editorResultMsg{err: fmt.Errorf("read temp file: %w", err)}
		}
		return editorResultMsg{text: strings.TrimSpace(string(data))}
	})
}

// applyEditorResult updates the input from an editorResultMsg. A failure is
// surfaced as a transcript error block instead of being silently swallowed.
func (m *TranscriptModel) applyEditorResult(msg editorResultMsg) {
	if msg.err != nil {
		m.appendBlock(blockError, "✗ editor: "+msg.err.Error())
		return
	}
	m.input.SetValue(msg.text)
	m.input.CursorEnd()
}

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

// openEditorPrompt writes the current input to a temp file and opens $EDITOR.
func (m *TranscriptModel) openEditorPrompt() tea.Cmd {
	text := m.input.Value()
	return func() tea.Msg {
		tmp, err := os.CreateTemp("", "sandbox-prompt-*.md")
		if err != nil {
			return editorResultMsg{err: fmt.Errorf("create temp file: %w", err)}
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.WriteString(text); err != nil {
			tmp.Close()
			return editorResultMsg{err: fmt.Errorf("write temp file: %w", err)}
		}
		if err := tmp.Close(); err != nil {
			return editorResultMsg{err: fmt.Errorf("close temp file: %w", err)}
		}

		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}

		cmd := exec.Command("sh", "-c", fmt.Sprintf("%s %s", editor, tmp.Name()))
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return editorResultMsg{err: fmt.Errorf("editor exited: %w", err)}
		}

		data, err := os.ReadFile(tmp.Name())
		if err != nil {
			return editorResultMsg{err: fmt.Errorf("read temp file: %w", err)}
		}
		return editorResultMsg{text: strings.TrimSpace(string(data))}
	}
}

// applyEditorResult updates the input from an editorResultMsg.
func (m *TranscriptModel) applyEditorResult(msg editorResultMsg) {
	if msg.err != nil {
		return
	}
	m.input.SetValue(msg.text)
	m.input.SetCursor(len(msg.text))
}

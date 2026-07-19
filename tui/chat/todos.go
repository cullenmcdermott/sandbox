package chat

// todos.go — the pinned todo checklist, rendered as a calm checkbox progression:
// completed items strike through in dim green, the in-progress item is bright
// (its present-tense ActiveForm preferred when set), pending items are dim, and
// an empty list collapses to a single "todos cleared" line. This is the §2b/§2c
// todo grammar from the production transcript, made self-contained. The list is
// typically mutated in place on each update and re-rendered.

import (
	"strings"

	"github.com/cullenmcdermott/sandbox/tui/list"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// TodoStatus is a checklist item's state.
type TodoStatus int

const (
	// TodoPending is a not-yet-started item (dim "○").
	TodoPending TodoStatus = iota
	// TodoInProgress is the active item (bright "▸", ActiveForm preferred).
	TodoInProgress
	// TodoCompleted is a finished item (strikethrough "✓").
	TodoCompleted
)

// Todo is one checklist item.
type Todo struct {
	// Content is the imperative task description ("Add the parser").
	Content string
	// ActiveForm is the present-tense form shown while in progress ("Adding the
	// parser"); falls back to Content when empty.
	ActiveForm string
	// Status is the item's state.
	Status TodoStatus
}

// TodosItem is the pinned checklist block.
type TodosItem struct {
	*list.Versioned

	Todos   []Todo
	focused bool

	cache section
}

// NewTodosItem builds a checklist block.
func NewTodosItem(todos []Todo) *TodosItem {
	return &TodosItem{Versioned: list.NewVersioned(), Todos: todos}
}

// SetTodos replaces the checklist (a todo.updated event).
func (t *TodosItem) SetTodos(todos []Todo) {
	t.Todos = todos
	t.cache.valid = false
	t.Bump()
}

// SetFocused marks the block focused (a left gutter bar).
func (t *TodosItem) SetFocused(b bool) {
	if t.focused == b {
		return
	}
	t.focused = b
	t.cache.valid = false
	t.Bump()
}

// Focused reports the focus state.
func (t *TodosItem) Focused() bool { return t.focused }

// Render draws the checklist within width columns.
func (t *TodosItem) Render(width int) string {
	if width < 1 {
		width = 1
	}
	fields := make([][]byte, 0, len(t.Todos)*3+1)
	for _, td := range t.Todos {
		fields = append(fields, []byte(td.Content), []byte(td.ActiveForm), u64b(uint64(td.Status)))
	}
	srcHash := fnvFields(fields...)
	extra := extraKey(theme.Epoch(), flagBits(t.focused))
	if t.cache.hit(width, srcHash, extra) {
		return t.cache.out
	}
	out := clampFocus(t.render(focusWidth(width, t.focused)), t.focused)
	t.cache.store(width, srcHash, extra, out)
	return out
}

func (t *TodosItem) render(width int) string {
	if len(t.Todos) == 0 {
		return truncate(styTodoCleared.Render("todos cleared"), width)
	}
	lines := make([]string, 0, len(t.Todos))
	for _, td := range t.Todos {
		var s string
		switch td.Status {
		case TodoCompleted:
			s = styTodoDone.Render(truncate("✓ "+td.Content, width))
		case TodoInProgress:
			text := td.Content
			if td.ActiveForm != "" {
				text = td.ActiveForm
			}
			s = styTodoActive.Render(truncate("▸ "+text, width))
		default: // pending
			s = styTodoPending.Render(truncate("○ "+td.Content, width))
		}
		lines = append(lines, s)
	}
	return strings.Join(lines, "\n")
}

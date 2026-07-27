package client

import (
	"errors"
	"os"
	"strings"

	"github.com/cullenmcdermott/sandbox/internal/index"
)

// Title is a session's display label. Name is the user-chosen override
// (`sandbox rename`, or CreateOptions' name); Auto is the agent-generated
// conversation summary the runner emits as a session.title event. Name wins
// whenever it is set — see Display.
//
// Titles are stored in the local session index, NOT in the pod: a title must be
// settable for a SUSPENDED session, whose runner is not running, and renaming is
// most often exactly what you do to a session you have parked. That makes titles
// per-install rather than cross-machine; the session ID is the identity that
// travels.
type Title struct {
	Name string
	Auto string
}

// Display returns the label to show for a session: Name if set, else Auto, else "".
func (t Title) Display() string {
	if t.Name != "" {
		return t.Name
	}
	return t.Auto
}

// ErrEmptyTitle rejects a blank rename, so a user-chosen label can never be
// silently cleared by a stray call.
var ErrEmptyTitle = errors.New("sandbox: title must not be empty")

// Title reads a session's persisted display label. A session with no index
// entry yields the zero Title and a nil error — not knowing a label is not a
// failure.
func (c *Client) Title(id ID) (Title, error) {
	entry, err := c.index.Load(string(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Title{}, nil
		}
		return Title{}, err
	}
	return Title{Name: entry.RenamedTitle, Auto: entry.AutoTitle}, nil
}

// SetTitle sets the user-chosen display label, overriding any agent-generated
// Auto summary. Whitespace is trimmed; a blank name is ErrEmptyTitle.
func (c *Client) SetTitle(id ID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrEmptyTitle
	}
	// [V7] Save only a PARTIAL entry — the identity fields Save needs plus the
	// one field this call owns — and let Save's locked load-merge fill the rest.
	// Loading the full on-disk entry here, mutating it, and re-Saving would race
	// a concurrent snapshot/driver writer: this call would hold a stale in-memory
	// copy of (say) LastEventSeq/Snapshot from before that writer's update, and
	// writing it back would clobber the newer value the other writer just
	// persisted. A partial entry can only ever advance the field it owns.
	return c.index.Save(string(id), index.Entry{
		SandboxSessionID: string(id),
		SandboxName:      string(id),
		RenamedTitle:     name,
	})
}

// SetAutoTitle records the agent-generated summary. It never touches a
// user-chosen Name, so a rename always wins. A blank summary is a no-op.
func (c *Client) SetAutoTitle(id ID, auto string) error {
	auto = strings.TrimSpace(auto)
	if auto == "" {
		return nil
	}
	// [V7] Partial entry + Save's locked merge — see SetTitle.
	return c.index.Save(string(id), index.Entry{
		SandboxSessionID: string(id),
		SandboxName:      string(id),
		AutoTitle:        auto,
	})
}

// Package index manages the local session index at
// ~/.local/share/sandbox/remote-sessions/<session-id>/. This is a local
// mirror/cache of remote session state. The remote PVC is authoritative
// while the session exists.
package index

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// Index manages local session index files.
type Index struct {
	root string // ~/.local/share/sandbox/remote-sessions
}

// New creates an Index rooted at the given directory (typically
// ~/.local/share/sandbox/remote-sessions).
func New(root string) *Index {
	return &Index{root: root}
}

// DefaultRoot returns the default root path for the session index.
func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "sandbox", "remote-sessions"), nil
}

// NewDefault creates an Index at the default path.
func NewDefault() (*Index, error) {
	root, err := DefaultRoot()
	if err != nil {
		return nil, err
	}
	return New(root), nil
}

// Entry is the local index entry for a remote session.
type Entry struct {
	SandboxSessionID string    `json:"sandboxSessionId"`
	Backend          string    `json:"backend"`
	ProjectPath      string    `json:"projectPath"`
	Namespace        string    `json:"namespace"`
	SandboxName      string    `json:"sandboxName"`
	RunnerToken      string    `json:"-"`          // stored separately, not in JSON
	CreatedAt        time.Time `json:"createdAt"`
	LastActivity     time.Time `json:"lastActivity"`
	LastEventSeq     uint64    `json:"lastEventSeq"`
	MutagenSessions  []string  `json:"mutagenSessions,omitempty"`
	ForwardHTTPPort  int       `json:"forwardHttpPort,omitempty"`
	ForwardSSHPort   int       `json:"forwardSshPort,omitempty"`
}

// Save writes the session index entry to disk.
func (i *Index) Save(id string, entry Entry) error {
	dir := filepath.Join(i.root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("index: mkdir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "session.json")
	return os.WriteFile(path, data, 0o600)
}

// Load reads a session index entry.
func (i *Index) Load(id string) (Entry, error) {
	path := filepath.Join(i.root, id, "session.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, fmt.Errorf("index: load %s: %w", id, err)
	}
	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return Entry{}, fmt.Errorf("index: decode %s: %w", id, err)
	}
	return entry, nil
}

// Delete removes a session index entry.
func (i *Index) Delete(id string) error {
	dir := filepath.Join(i.root, id)
	return os.RemoveAll(dir)
}

// List returns all local session index entries.
func (i *Index) List() ([]Entry, error) {
	entries, err := os.ReadDir(i.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("index: list %s: %w", i.root, err)
	}
	var result []Entry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		entry, err := i.Load(e.Name())
		if err != nil {
			continue
		}
		result = append(result, entry)
	}
	return result, nil
}

// SaveFromState converts a session.State to an Entry and saves it.
func (i *Index) SaveFromState(st session.State) error {
	return i.Save(string(st.ID), Entry{
		SandboxSessionID: string(st.ID),
		Backend:          st.Backend,
		ProjectPath:      st.ProjectPath,
		SandboxName:      st.SandboxName,
		LastActivity:     st.LastActivity,
	})
}

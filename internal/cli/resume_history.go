package cli

// Local resume support: make a k8s-started session resumable from the laptop's
// interactive `claude --resume` picker.
//
// The picker is populated from ~/.claude/history.jsonl (a per-machine prompt
// log), NOT by scanning the projects dir — so a transcript that syncs down from
// the pod is resumable by id but doesn't appear in the picker. The Claude Agent
// SDK in the pod never writes history.jsonl, so we synthesize the entry locally
// on TUI shutdown for every session whose transcript has actually synced down.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/index"
)

// afterTUI runs a dashboard entry point (fn) and, once the TUI exits, best-effort
// refreshes the laptop's `claude --resume` picker for any sessions whose
// transcripts have synced down. The picker refresh is non-fatal: its failure is
// logged but the TUI's own exit error is what propagates.
func afterTUI(fn func() error) error {
	err := fn()
	if herr := syncResumeHistory(); herr != nil {
		fmt.Fprintf(os.Stderr, "warning: refresh resume history: %v\n", herr)
	}
	return err
}

// claudeConfigDir resolves the local Claude Code config directory: CLAUDE_CONFIG_DIR
// if set, else ~/.claude. This is where the picker's history.jsonl and the
// projects/<encoded-cwd>/<uuid>.jsonl transcripts live.
func claudeConfigDir() (string, error) {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

// historyEntry mirrors one line of ~/.claude/history.jsonl.
type historyEntry struct {
	Display        string         `json:"display"`
	PastedContents map[string]any `json:"pastedContents"`
	Project        string         `json:"project"`
	SessionID      string         `json:"sessionId"`
	Timestamp      int64          `json:"timestamp"`
}

// transcriptExistsFor reports whether a Claude transcript for the given SDK
// session id has synced down locally (projects/*/<id>.jsonl). We only add a
// picker entry when the transcript is present, so the picker never lists a
// session that would fail to resume ("no conversation found"). Globbing by the
// unique session uuid avoids re-implementing Claude's cwd→dir encoding.
func transcriptExistsFor(cfgDir, claudeID string) bool {
	matches, _ := filepath.Glob(filepath.Join(cfgDir, "projects", "*", claudeID+".jsonl"))
	return len(matches) > 0
}

// historyHasSession reports whether history.jsonl already has an entry for the
// session id (dedup across runs). A missing file means "no".
func historyHasSession(path, claudeID string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	needle := `"sessionId":"` + claudeID + `"`
	for sc.Scan() {
		if strings.Contains(sc.Text(), needle) {
			return true, nil
		}
	}
	return false, sc.Err()
}

// appendHistoryEntry appends one JSONL line (append-only, single write) so it
// doesn't clobber entries Claude Code may be writing concurrently.
func appendHistoryEntry(path string, e historyEntry) error {
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// resumeDisplay is the picker label for a synthesized entry: the user rename,
// else the runner auto title, else the project basename — tagged so a synced k8s
// session is recognizable in the list.
func resumeDisplay(e index.Entry) string {
	label := e.RenamedTitle
	if label == "" {
		label = e.AutoTitle
	}
	if label == "" {
		label = filepath.Base(e.ProjectPath)
	}
	return "[sandbox] " + label
}

// syncResumeHistory makes every local sandbox session whose transcript has synced
// down resumable from the laptop's interactive `claude --resume` picker: for each
// index entry carrying a Claude SDK session id whose transcript is present
// locally, it appends a history.jsonl entry if one isn't already there.
//
// Idempotent and self-healing (safe on every TUI shutdown) and best-effort: any
// error is returned for logging but is non-fatal — resume by id
// (`claude --resume <id>`) works regardless.
func syncResumeHistory() error {
	cfgDir, err := claudeConfigDir()
	if err != nil {
		return err
	}
	idx, err := newIndex()
	if err != nil {
		return err
	}
	entries, err := idx.List()
	if err != nil {
		return err
	}
	historyPath := filepath.Join(cfgDir, "history.jsonl")
	var firstErr error
	for _, e := range entries {
		if e.AgentSessionID == "" || e.ProjectPath == "" {
			continue
		}
		if !transcriptExistsFor(cfgDir, e.AgentSessionID) {
			continue // not synced yet — a later shutdown will pick it up
		}
		has, herr := historyHasSession(historyPath, e.AgentSessionID)
		if herr != nil {
			if firstErr == nil {
				firstErr = herr
			}
			continue
		}
		if has {
			continue
		}
		if werr := appendHistoryEntry(historyPath, historyEntry{
			Display:        resumeDisplay(e),
			PastedContents: map[string]any{},
			Project:        e.ProjectPath,
			SessionID:      e.AgentSessionID,
			Timestamp:      time.Now().UnixMilli(),
		}); werr != nil && firstErr == nil {
			firstErr = werr
		}
	}
	return firstErr
}

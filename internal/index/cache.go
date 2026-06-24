package index

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// Host-side transcript cache (Workstream C). Each session keeps an append-only
// events.ndjson next to its session.json — one normalized event per line. On
// re-attach the CLI loads it to rebuild the conversation instantly and resumes
// the runner SSE stream from the max cached seq (Entry.LastEventSeq), streaming
// only the delta instead of replaying the whole history from seq 0. The runner's
// events.db remains authoritative; this cache is a discardable local mirror.

// EventCachePath returns the path to a session's transcript cache file.
func (i *Index) EventCachePath(id string) string {
	return filepath.Join(i.root, id, "events.ndjson")
}

// AppendCachedEvent appends one event to the session's transcript cache. Best
// effort and append-only: the runner's events.db is authoritative, so a cache
// write failure must never break the live stream — callers log and continue.
// High-volume incremental events (message/reasoning/tool .delta) should NOT be
// cached: replay rebuilds final state from the completed events, and the deltas
// only drive the live streaming preview.
func (i *Index) AppendCachedEvent(id string, ev session.Event) error {
	if err := validateID(i.root, id); err != nil {
		return err
	}
	dir := filepath.Join(i.root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("index: mkdir %s: %w", dir, err)
	}
	f, err := os.OpenFile(i.EventCachePath(id), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("index: open event cache %s: %w", id, err)
	}
	defer f.Close()
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("index: append event %s: %w", id, err)
	}
	return nil
}

// LoadCachedEvents reads a session's cached transcript events in append order.
// A missing cache returns nil (a cold first attach). A corrupt line is skipped
// rather than failing the whole replay, so one bad/partial write can't wedge the
// attach — the delta stream backfills anything missing.
func (i *Index) LoadCachedEvents(id string) ([]session.Event, error) {
	f, err := os.Open(i.EventCachePath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("index: open event cache %s: %w", id, err)
	}
	defer f.Close()

	var out []session.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev session.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue // skip a corrupt/partial line
		}
		out = append(out, ev)
	}
	return out, sc.Err()
}

// DeleteEventCache removes a session's transcript cache (used when an entry is
// deleted). A missing file is not an error.
func (i *Index) DeleteEventCache(id string) error {
	if err := os.Remove(i.EventCachePath(id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

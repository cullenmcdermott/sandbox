package index

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// maxCacheBytes caps the host-side transcript cache: a long-lived session's
// events.ndjson can otherwise grow to hundreds of MB and make cold attach
// memory-bound (§4 E10). We keep only the most recent maxCacheBytes — the runner's
// events.db stays authoritative and the transcript only needs a recent tail to
// render instantly. The cap is enforced on load (read only the tail) and lazily on
// disk (compact when a writer opens an oversized file).
const maxCacheBytes = 8 << 20 // 8 MiB

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
	w, err := i.OpenCacheWriter(id)
	if err != nil {
		return err
	}
	defer w.Close()
	return w.Append(ev)
}

// CacheWriter is a persistent append handle for one session's transcript cache.
// Holding it open across appends avoids the per-event open/close (~5 syscalls) the
// old one-shot AppendCachedEvent paid on every cached event, from the foreground
// stream AND every warm feed (§4 E10). It is NOT safe for concurrent use by callers
// that build lines themselves, but each Append issues a single O_APPEND write of a
// whole line, so appends from a session's (at most) foreground+background handoff
// stay record-aligned. Durability is unchanged from the old path: os.File has no
// user-space buffer, so a plain Write (no fsync, as before) reaches the OS the same
// way Write-then-Close did.
type CacheWriter struct {
	f *os.File
}

// OpenCacheWriter opens (creating dirs/file as needed) a session's transcript cache
// for appending. If the file has grown past maxCacheBytes it is first compacted to
// its recent tail, so a long-lived session's cache can't grow without bound across
// re-attaches (§4 E10). Best effort — a compaction failure just leaves the file.
func (i *Index) OpenCacheWriter(id string) (*CacheWriter, error) {
	if err := validateID(i.root, id); err != nil {
		return nil, err
	}
	dir := filepath.Join(i.root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("index: mkdir %s: %w", dir, err)
	}
	path := i.EventCachePath(id)
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxCacheBytes {
		_ = compactCacheTail(path, maxCacheBytes)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("index: open event cache %s: %w", id, err)
	}
	return &CacheWriter{f: f}, nil
}

// Append writes one event as an ndjson line. Best effort and append-only: the
// runner's events.db is authoritative, so callers log and continue on error.
func (w *CacheWriter) Append(ev session.Event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := w.f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("index: append event: %w", err)
	}
	return nil
}

// Close releases the underlying append handle.
func (w *CacheWriter) Close() error { return w.f.Close() }

// compactCacheTail rewrites path in place keeping only its final keepBytes (rounded
// up to the next whole line), bounding a months-old session's cache. It stages the
// tail in a sibling temp file and renames it into place, so a crash mid-compaction
// leaves the original file intact (the rename is atomic).
func compactCacheTail(path string, keepBytes int64) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	fi, err := src.Stat()
	if err != nil {
		return err
	}
	start := fi.Size() - keepBytes
	if start <= 0 {
		return nil // already within the cap
	}
	if _, err := src.Seek(start, io.SeekStart); err != nil {
		return err
	}
	r := bufio.NewReader(src)
	if _, err := r.ReadBytes('\n'); err != nil {
		return err // no line boundary in the tail window — nothing safely retainable
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "events-*.ndjson.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once the rename succeeds
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// LoadCachedEvents reads a session's cached transcript events in append order.
// A missing cache returns nil (a cold first attach). A corrupt line is skipped
// rather than failing the whole replay, so one bad/partial write can't wedge the
// attach — the delta stream backfills anything missing.
func (i *Index) LoadCachedEvents(id string) ([]session.Event, error) {
	if err := validateID(i.root, id); err != nil { // [V30] uniform C5 traversal guard
		return nil, err
	}
	f, err := os.Open(i.EventCachePath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("index: open event cache %s: %w", id, err)
	}
	defer f.Close()

	// Cap the read at the final maxCacheBytes: a long-lived session's cache can be
	// hundreds of MB and slurping it whole makes cold attach memory-bound (§4 E10).
	// Seeking into mid-file lands mid-line, so discard the partial leading line —
	// the scanner already tolerates corrupt/partial lines below. Older history stays
	// in the runner's authoritative events.db; the transcript renders the tail.
	var rd io.Reader = f
	if fi, err := f.Stat(); err == nil && fi.Size() > maxCacheBytes {
		if _, err := f.Seek(fi.Size()-maxCacheBytes, io.SeekStart); err == nil {
			br := bufio.NewReader(f)
			_, _ = br.ReadBytes('\n') // drop the partial first line
			rd = br
		}
	}

	var out []session.Event
	sc := bufio.NewScanner(rd)
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
	if err := validateID(i.root, id); err != nil { // [V30] uniform C5 traversal guard
		return err
	}
	if err := os.Remove(i.EventCachePath(id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

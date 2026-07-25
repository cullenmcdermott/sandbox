package terminal

import (
	"encoding/base64"
	"strings"
)

// This file emits OSC ("Operating System Command") control strings as plain Go
// strings. They are zero-width when parsed by a terminal (and by Bubble Tea's
// cellbuf renderer), so callers splice them into the composed View frame and
// they cost no layout cells. Nothing here performs I/O.
//
// ParseOSC52 is the one parse-side helper: an app embedding a child terminal
// receives OSC from the child as well as emitting it to the host.

const (
	// bel terminates an OSC string (the ST/string-terminator; BEL is the widely
	// accepted short form Ghostty understands).
	bel = "\x07"
	// esc is the escape that introduces a control string.
	esc = "\x1b"
)

// Progress is the OSC 9;4 taskbar/tab progress state Ghostty paints on its tab.
type Progress int

const (
	// ProgressNone clears any progress indicator.
	ProgressNone Progress = iota
	// ProgressBusy shows an indeterminate "pulse" (a turn is running).
	ProgressBusy
	// ProgressError shows the error state (red) — used for a pending permission
	// so it surfaces on the tab even when the window is unfocused.
	ProgressError
)

// OSCProgress returns the OSC 9;4 control string for the given state.
// ProgressNone (and any unrecognized state) yields the "remove" sequence, so
// emitting it clears a previously-set indicator; callers that want to skip
// emission entirely must do so themselves. The returned string is zero-width.
//
// OSC 9;4 form: ESC ] 9 ; 4 ; <state> ; <pct> ST
//
//	state 0 = remove, 1 = set (determinate), 2 = error, 3 = indeterminate.
func OSCProgress(p Progress) string {
	switch p {
	case ProgressBusy:
		return esc + "]9;4;3;0" + bel
	case ProgressError:
		return esc + "]9;4;2;100" + bel
	default:
		return esc + "]9;4;0;0" + bel
	}
}

// OSCNotify returns an OSC 777 desktop-notification control string carrying a
// title and body. OSC 777 (notify) is supported by Ghostty (and rxvt/others);
// it raises a real OS notification distinct from the in-TUI toast. The returned
// string is zero-width. An empty title yields "" (nothing to notify).
//
// OSC 777 form: ESC ] 777 ; notify ; <title> ; <body> ST
func OSCNotify(title, body string) string {
	title = sanitizeOSC(title)
	if title == "" {
		return ""
	}
	body = sanitizeOSC(body)
	return esc + "]777;notify;" + title + ";" + body + bel
}

// OSC9Notify returns an OSC 9 desktop-notification escape carrying a single
// message line — the form iTerm2, WezTerm and kitty understand. Unlike OSC 777
// it has no separate title/body field, so callers join them. Form:
// ESC ] 9 ; <msg> ST. An empty message yields "". The returned string is
// zero-width but, like all of these, MUST be written out-of-band (e.g. tea.Raw)
// — a Bubble Tea v2 View drops control strings spliced into its content.
func OSC9Notify(msg string) string {
	msg = sanitizeOSC(msg)
	if msg == "" {
		return ""
	}
	return esc + "]9;" + msg + bel
}

// NotifyString returns the desktop-notification escape appropriate for the host
// terminal, or "" when it isn't one we can target. Ghostty (and rxvt) take the
// OSC 777 form with a title + body; iTerm2 and WezTerm take OSC 9 with a single
// message, so title and body are joined with an em dash. Centralising the choice
// here keeps the notify gate from being Ghostty-exclusive.
func NotifyString(c Caps, title, body string) string {
	switch {
	case c.IsGhostty:
		return OSCNotify(title, body)
	case c.IsITerm2 || c.IsWezTerm:
		msg := title
		if body != "" {
			if msg != "" {
				msg += " — "
			}
			msg += body
		}
		return OSC9Notify(msg)
	default:
		return ""
	}
}

// ParseOSC52 parses an OSC 52 clipboard-write payload — `52;<selection>;<base64>`,
// the whole OSC data field including the leading command number, which is what a
// terminal-emulator library hands an OSC handler.
//
// It returns the decoded text, whether the child asked for the X11/Wayland
// PRIMARY selection rather than the system clipboard, and ok=false for anything
// that is not an actionable write. In particular a clipboard *read* query
// (`52;c;?`) is not actionable: answering it means an async round-trip to the
// host terminal, so it reports ok=false and the caller drops it.
//
// This is the parse an app embedding a child terminal needs: a virtual-terminal
// emulator has no clipboard of its own, so unless the host relays the child's
// OSC 52 outward (e.g. via tea.SetClipboard) an in-pane copy silently does
// nothing and the user is left with the host terminal's own shift-drag
// selection.
//
// Emitters differ on base64 padding and some wrap long payloads, so whitespace
// is stripped and both the padded and raw alphabets are accepted.
func ParseOSC52(data []byte) (text string, primary, ok bool) {
	parts := strings.SplitN(string(data), ";", 3)
	if len(parts) != 3 {
		return "", false, false
	}
	s := strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', ' ', '\t':
			return -1
		default:
			return r
		}
	}, parts[2])
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		if b, err = base64.RawStdEncoding.DecodeString(s); err != nil {
			return "", false, false
		}
	}
	// Per the XTerm spec the selection field may name several targets; "p"
	// alone is the only one that means PRIMARY rather than the clipboard.
	return string(b), parts[1] == "p", true
}

// sanitizeOSC strips bytes that would prematurely terminate or corrupt an OSC
// string: ESC, BEL, the ST sequence, semicolons (the OSC field separator), and
// newlines/carriage returns. This keeps an attacker- or data-controlled title
// from injecting extra OSC fields or escapes.
func sanitizeOSC(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\x1b', '\x07', ';', '\n', '\r', '\x9c':
			return -1
		default:
			return r
		}
	}, s)
}

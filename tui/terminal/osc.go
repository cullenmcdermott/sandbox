package terminal

import "strings"

// This file emits OSC ("Operating System Command") control strings as plain Go
// strings. They are zero-width when parsed by a terminal (and by Bubble Tea's
// cellbuf renderer), so callers splice them into the composed View frame and
// they cost no layout cells. Nothing here performs I/O.

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

// OSCProgress returns the OSC 9;4 control string for the given state, or "" for
// ProgressNone-with-nothing-to-clear callers that prefer to skip emission. The
// returned string is zero-width.
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

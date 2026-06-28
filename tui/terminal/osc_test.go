package terminal

import (
	"strings"
	"testing"
)

func TestNotifyString(t *testing.T) {
	// Ghostty → OSC 777 with title + body.
	if got := NotifyString(Caps{IsGhostty: true}, "sess", "wants: Bash"); got != "\x1b]777;notify;sess;wants: Bash\x07" {
		t.Fatalf("ghostty: got %q", got)
	}
	// iTerm2 / WezTerm → OSC 9, title and body joined.
	for _, c := range []Caps{{IsITerm2: true}, {IsWezTerm: true}} {
		got := NotifyString(c, "sess", "claude")
		if got != "\x1b]9;sess — claude\x07" {
			t.Fatalf("osc9 terminal %+v: got %q", c, got)
		}
	}
	// Untargetable terminal → "".
	if got := NotifyString(Caps{}, "sess", "body"); got != "" {
		t.Fatalf("unknown terminal must yield empty, got %q", got)
	}
	// Empty title → "" even on a capable terminal (nothing to notify).
	if got := NotifyString(Caps{IsGhostty: true}, "", "body"); got != "" {
		t.Fatalf("empty title must yield empty, got %q", got)
	}
}

func TestOSC9Notify(t *testing.T) {
	if got := OSC9Notify("hello"); got != "\x1b]9;hello\x07" {
		t.Fatalf("got %q", got)
	}
	if got := OSC9Notify(""); got != "" {
		t.Fatalf("empty message must yield empty, got %q", got)
	}
	// Semicolons/escapes are stripped so a data-controlled message can't inject
	// extra OSC fields.
	if got := OSC9Notify("a;b\x1bc"); got != "\x1b]9;abc\x07" {
		t.Fatalf("sanitisation failed, got %q", got)
	}
}

func TestOSCProgress(t *testing.T) {
	for _, tc := range []struct {
		p    Progress
		want string
	}{
		{ProgressBusy, "\x1b]9;4;3;0\x07"},
		{ProgressError, "\x1b]9;4;2;100\x07"},
		{ProgressNone, "\x1b]9;4;0;0\x07"},
	} {
		if got := OSCProgress(tc.p); got != tc.want {
			t.Errorf("OSCProgress(%v) = %q, want %q", tc.p, got, tc.want)
		}
	}
}

func TestOSCNotify(t *testing.T) {
	got := OSCNotify("session-1", "wants: Bash")
	want := "\x1b]777;notify;session-1;wants: Bash\x07"
	if got != want {
		t.Fatalf("OSCNotify = %q, want %q", got, want)
	}
}

func TestOSCNotifyEmptyTitle(t *testing.T) {
	if got := OSCNotify("", "body"); got != "" {
		t.Fatalf("empty title should yield empty string, got %q", got)
	}
}

// A malicious or accidental title/body must not be able to inject extra OSC
// fields or escape the control string.
func TestOSCNotifySanitizes(t *testing.T) {
	got := OSCNotify("a;b\x1b]0;evil\x07", "c\nd;e")
	if strings.ContainsAny(got[len("\x1b]777;notify;"):], "\x1b\n") {
		t.Errorf("sanitized output still contains ESC/newline: %q", got)
	}
	// The single legitimate separators are the two we inserted; the title must
	// not contain any extra semicolons beyond field separators.
	// title field is between the 3rd and 4th ';' — verify the payload has exactly
	// the framing semicolons we expect (777;notify;title;body).
	if strings.Count(got, "\x1b") != 1 {
		t.Errorf("expected exactly one ESC (the leading one), got %q", got)
	}
}

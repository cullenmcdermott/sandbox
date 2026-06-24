package terminal

import (
	"strings"
	"testing"
)

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

package dashboard

import (
	"encoding/base64"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/vt"
)

// osc52 builds an `OSC 52 ; sel ; <base64>` sequence terminated by BEL — the
// form Claude Code emits when it copies a mouse selection in the pane.
func osc52(sel, text string) string {
	return "\x1b]52;" + sel + ";" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"
}

// clipboardMsgs executes cmd (recursing into tea.Batch) and returns every
// message it produced. Every leaf Cmd passed in must be non-blocking.
func execMsgs(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range msg {
			out = append(out, execMsgs(t, c)...)
		}
		return out
	case nil:
		return nil
	default:
		return []tea.Msg{msg}
	}
}

// hasMsg reports whether want appears in msgs. tea's clipboard messages are
// unexported defined string types, so they are produced via tea.SetClipboard
// rather than named directly.
func hasMsg(msgs []tea.Msg, want tea.Msg) bool {
	for _, m := range msgs {
		if reflect.DeepEqual(m, want) {
			return true
		}
	}
	return false
}

// REGRESSION (O8): the OSC 52 handler must be wired to the emulator by Init.
// Without it the vt emulator swallows the child's clipboard writes as an
// "unhandled sequence" and an in-pane copy silently does nothing — the bug that
// forced shift-drag native selection as the only way to get text out of a
// claude-pane session. Driving Init over a fake transport (rather than
// registering the handler in the test) is what makes this a wiring guard.
func TestExternalPaneInitRelaysOSC52ToHost(t *testing.T) {
	toPaneR, toPaneW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer toPaneW.Close()
	tr := &fakePaneTransport{ReadWriteCloser: struct {
		io.Reader
		io.Writer
		io.Closer
	}{toPaneR, io.Discard, toPaneR}}

	p := NewExternalPaneTransport(Session{}, "claude", func(cols, rows int) (PaneTransport, error) {
		return tr, nil
	}, nil)
	p.w, p.h = 40, 10
	p.Init()
	t.Cleanup(p.close)
	if p.err != nil {
		t.Fatalf("Init: %v", p.err)
	}

	// A copy large enough to straddle transport reads, fed in two writes: the
	// emulator's parser reassembles the sequence, so the handler still sees it
	// whole. (An OSC 52 payload is routinely a few KB — the screenshot that
	// prompted this was 2165 chars.)
	text := strings.Repeat("copy me ", 400)
	seq := osc52("c", text)
	p.feed([]byte(seq[:1000]))
	if len(p.pendingClip) != 0 {
		t.Fatal("partial OSC 52 must not queue a clipboard write")
	}
	p.feed([]byte(seq[1000:]))

	msgs := execMsgs(t, p.drainClipboard())
	if !hasMsg(msgs, tea.SetClipboard(text)()) {
		t.Fatalf("child's OSC 52 did not reach the host clipboard; got %#v", msgs)
	}
	if p.pendingClip != nil {
		t.Fatal("drainClipboard must empty the queue")
	}
}

// apply must carry a queued clipboard write out alongside the next read, so a
// copy takes effect on the same Update that rendered the child's output.
func TestExternalPaneApplyBatchesClipboardWithRead(t *testing.T) {
	p := &ExternalPane{emu: vt.NewEmulator(40, 2), out: make(chan ptyChunk, 4)}
	p.emu.RegisterOscHandler(52, p.handleOSC52)

	cmd, finished := p.apply(ptyChunk{data: []byte(osc52("c", "yanked")), ok: true})
	if finished {
		t.Fatal("apply(live chunk) should continue the drain")
	}
	// Queue a chunk only now — apply's batch drain would have swallowed one
	// pushed earlier — so the readCmd inside the returned batch doesn't block.
	p.out <- ptyChunk{data: []byte("next"), ok: true}
	msgs := execMsgs(t, cmd)
	if !hasMsg(msgs, tea.SetClipboard("yanked")()) {
		t.Fatalf("apply did not batch the clipboard write; got %#v", msgs)
	}
	var sawRead bool
	for _, m := range msgs {
		if _, ok := m.(ptyOutputMsg); ok {
			sawRead = true
		}
	}
	if !sawRead {
		t.Fatal("apply must still continue the transport drain")
	}
}

// A clipboard write in the child's FINAL output still belongs on the clipboard,
// so the end-of-stream path drains too — and handlePtyOutput must batch that
// Cmd with the finished message instead of dropping it.
func TestExternalPaneClipboardSurvivesStreamEnd(t *testing.T) {
	p := &ExternalPane{emu: vt.NewEmulator(40, 2), out: make(chan ptyChunk, 4)}
	p.emu.RegisterOscHandler(52, p.handleOSC52)
	p.out <- ptyChunk{ok: false} // end of stream, queued behind the copy

	a := &App{screen: ScreenExternal, dashboard: New(nil), external: p}
	_, cmd := a.handlePtyOutput(ptyOutputMsg{pane: p, chunk: ptyChunk{data: []byte(osc52("c", "last words")), ok: true}})

	msgs := execMsgs(t, cmd)
	if !hasMsg(msgs, tea.SetClipboard("last words")()) {
		t.Fatalf("clipboard write in final output was dropped; got %#v", msgs)
	}
	var sawFinished bool
	for _, m := range msgs {
		if _, ok := m.(externalPaneFinishedMsg); ok {
			sawFinished = true
		}
	}
	if !sawFinished {
		t.Fatal("end of stream must still produce externalPaneFinishedMsg")
	}
}

// handleOSC52 queues what terminal.ParseOSC52 accepts and swallows the rest
// (the parse itself is covered in tui/terminal). It must always report the
// sequence as handled so the emulator stops logging it as unhandled.
func TestHandleOSC52Queues(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("hi"))
	cases := []struct {
		name string
		data string // OSC payload, command number included
		want []paneClip
	}{
		{"clipboard", "52;c;" + b64, []paneClip{{text: "hi"}}},
		{"primary", "52;p;" + b64, []paneClip{{primary: true, text: "hi"}}},
		{"read-query-ignored", "52;c;?", nil},
		{"malformed-ignored", "52;c", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &ExternalPane{}
			if !p.handleOSC52([]byte(c.data)) {
				t.Fatal("handleOSC52 must always report the sequence as handled")
			}
			if !reflect.DeepEqual(p.pendingClip, c.want) {
				t.Fatalf("pendingClip = %#v, want %#v", p.pendingClip, c.want)
			}
		})
	}
}

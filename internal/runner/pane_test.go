package runner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// panePeer is one accepted server-side pane socket plus the frames it received.
type panePeer struct {
	conn *websocket.Conn
	// binary receives each binary frame's bytes; text each text frame's payload.
	binary chan []byte
	text   chan string
	// closed receives the client's close code (or -1 on a non-close read error).
	closed chan int
}

// paneTestServer upgrades pane requests like the runner does: bearer-token
// check before anything else, then hands the accepted socket to accept.
func paneTestServer(t *testing.T, token string, accept chan<- *panePeer) *httptest.Server {
	t.Helper()
	up := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/pane") {
			http.NotFound(w, r)
			return
		}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		p := &panePeer{conn: conn, binary: make(chan []byte, 8), text: make(chan string, 8), closed: make(chan int, 1)}
		go func() {
			for {
				mt, data, rerr := conn.ReadMessage()
				if rerr != nil {
					var ce *websocket.CloseError
					if errors.As(rerr, &ce) {
						p.closed <- ce.Code
					} else {
						p.closed <- -1
					}
					return
				}
				switch mt {
				case websocket.BinaryMessage:
					p.binary <- data
				case websocket.TextMessage:
					p.text <- string(data)
				}
			}
		}()
		accept <- p
	}))
}

func waitPeer(t *testing.T, accept <-chan *panePeer) *panePeer {
	t.Helper()
	select {
	case p := <-accept:
		return p
	case <-time.After(5 * time.Second):
		t.Fatal("server never accepted the pane socket")
		return nil
	}
}

// TestAttachPaneReplayAndWrite covers the core byte path: the server's replay
// frame comes out of Read, and client Writes arrive server-side as binary
// frames (raw PTY input).
func TestAttachPaneReplayAndWrite(t *testing.T) {
	accept := make(chan *panePeer, 1)
	srv := paneTestServer(t, "tok", accept)
	defer srv.Close()

	c := New(srv.URL, "tok")
	ps, err := c.AttachPane(context.Background(), session.Ref{ID: "s1"}, 0, 0)
	if err != nil {
		t.Fatalf("AttachPane: %v", err)
	}
	defer ps.Close()
	peer := waitPeer(t, accept)

	// Server → client: a replay frame followed by a live frame must read back
	// in order (frame boundaries need not survive; content must).
	if err := peer.conn.WriteMessage(websocket.BinaryMessage, []byte("replay:")); err != nil {
		t.Fatalf("server write: %v", err)
	}
	if err := peer.conn.WriteMessage(websocket.BinaryMessage, []byte("live")); err != nil {
		t.Fatalf("server write: %v", err)
	}
	got := make([]byte, 0, 11)
	buf := make([]byte, 4)
	for len(got) < 11 {
		n, rerr := ps.Read(buf)
		if rerr != nil {
			t.Fatalf("Read after %q: %v", got, rerr)
		}
		got = append(got, buf[:n]...)
	}
	if string(got) != "replay:live" {
		t.Errorf("Read = %q, want %q", got, "replay:live")
	}

	// Client → server: Write must arrive as one binary frame.
	if _, err := ps.Write([]byte("ls\r")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	select {
	case b := <-peer.binary:
		if string(b) != "ls\r" {
			t.Errorf("server got binary %q, want %q", b, "ls\r")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server never received the input frame")
	}
}

// TestAttachPaneInitialResize covers the attach-time geometry hint: a positive
// cols/rows must arrive as the resize JSON control frame before any input.
func TestAttachPaneInitialResize(t *testing.T) {
	accept := make(chan *panePeer, 1)
	srv := paneTestServer(t, "tok", accept)
	defer srv.Close()

	c := New(srv.URL, "tok")
	ps, err := c.AttachPane(context.Background(), session.Ref{ID: "s1"}, 120, 40)
	if err != nil {
		t.Fatalf("AttachPane: %v", err)
	}
	defer ps.Close()
	peer := waitPeer(t, accept)

	select {
	case txt := <-peer.text:
		var msg struct {
			Type string `json:"type"`
			Cols int    `json:"cols"`
			Rows int    `json:"rows"`
		}
		if err := json.Unmarshal([]byte(txt), &msg); err != nil {
			t.Fatalf("control frame not JSON: %q: %v", txt, err)
		}
		if msg.Type != "resize" || msg.Cols != 120 || msg.Rows != 40 {
			t.Errorf("control frame = %+v, want resize 120x40", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server never received the initial resize control frame")
	}
}

// TestPaneStreamResize covers a later Resize (window change while attached).
func TestPaneStreamResize(t *testing.T) {
	accept := make(chan *panePeer, 1)
	srv := paneTestServer(t, "tok", accept)
	defer srv.Close()

	c := New(srv.URL, "tok")
	ps, err := c.AttachPane(context.Background(), session.Ref{ID: "s1"}, 0, 0)
	if err != nil {
		t.Fatalf("AttachPane: %v", err)
	}
	defer ps.Close()
	peer := waitPeer(t, accept)

	if err := ps.Resize(81, 25); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	select {
	case txt := <-peer.text:
		if want := `{"type":"resize","cols":81,"rows":25}`; txt != want {
			t.Errorf("control frame = %q, want %q", txt, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server never received the resize control frame")
	}
}

// TestPaneReadCloseCodes covers the sentinel mapping: the runner's application
// close codes must surface as ErrPanePreempted / ErrPaneChildExited, and a
// normal close as io.EOF, so the dashboard can branch on the detach reason.
func TestPaneReadCloseCodes(t *testing.T) {
	cases := []struct {
		name string
		code int
		want error
	}{
		{"preempted", 4001, ErrPanePreempted},
		{"child exited", 4002, ErrPaneChildExited},
		{"normal close", websocket.CloseNormalClosure, io.EOF},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			accept := make(chan *panePeer, 1)
			srv := paneTestServer(t, "tok", accept)
			defer srv.Close()

			c := New(srv.URL, "tok")
			ps, err := c.AttachPane(context.Background(), session.Ref{ID: "s1"}, 0, 0)
			if err != nil {
				t.Fatalf("AttachPane: %v", err)
			}
			defer ps.Close()
			peer := waitPeer(t, accept)

			msg := websocket.FormatCloseMessage(tc.code, "test")
			if err := peer.conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(time.Second)); err != nil {
				t.Fatalf("server close: %v", err)
			}
			buf := make([]byte, 16)
			_, rerr := ps.Read(buf)
			if !errors.Is(rerr, tc.want) {
				t.Errorf("Read error = %v, want %v", rerr, tc.want)
			}
		})
	}
}

// TestPaneReadSkipsTextFrames: an unexpected server text frame must never leak
// into the PTY byte stream (it would corrupt the terminal).
func TestPaneReadSkipsTextFrames(t *testing.T) {
	accept := make(chan *panePeer, 1)
	srv := paneTestServer(t, "tok", accept)
	defer srv.Close()

	c := New(srv.URL, "tok")
	ps, err := c.AttachPane(context.Background(), session.Ref{ID: "s1"}, 0, 0)
	if err != nil {
		t.Fatalf("AttachPane: %v", err)
	}
	defer ps.Close()
	peer := waitPeer(t, accept)

	if err := peer.conn.WriteMessage(websocket.TextMessage, []byte(`{"noise":true}`)); err != nil {
		t.Fatalf("server write: %v", err)
	}
	if err := peer.conn.WriteMessage(websocket.BinaryMessage, []byte("pty")); err != nil {
		t.Fatalf("server write: %v", err)
	}
	buf := make([]byte, 16)
	n, rerr := ps.Read(buf)
	if rerr != nil {
		t.Fatalf("Read: %v", rerr)
	}
	if string(buf[:n]) != "pty" {
		t.Errorf("Read = %q, want %q (text frame leaked into the byte stream)", buf[:n], "pty")
	}
}

// TestAttachPaneRejected covers a pre-upgrade rejection: the plain HTTP status
// must fold into the returned error (same statusError shape as other calls).
func TestAttachPaneRejected(t *testing.T) {
	accept := make(chan *panePeer, 1)
	srv := paneTestServer(t, "tok", accept)
	defer srv.Close()

	c := New(srv.URL, "wrong-token")
	_, err := c.AttachPane(context.Background(), session.Ref{ID: "s1"}, 0, 0)
	if err == nil {
		t.Fatal("AttachPane with a bad token succeeded, want error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error %q does not carry the 401 status", err)
	}
}

// TestPaneCloseSendsCloseFrame: Close must perform a closing handshake so the
// runner sees a clean detach (leaving the child running), not a dropped socket.
func TestPaneCloseSendsCloseFrame(t *testing.T) {
	accept := make(chan *panePeer, 1)
	srv := paneTestServer(t, "tok", accept)
	defer srv.Close()

	c := New(srv.URL, "tok")
	ps, err := c.AttachPane(context.Background(), session.Ref{ID: "s1"}, 0, 0)
	if err != nil {
		t.Fatalf("AttachPane: %v", err)
	}
	peer := waitPeer(t, accept)

	if err := ps.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case code := <-peer.closed:
		if code != websocket.CloseNormalClosure {
			t.Errorf("server observed close code %d, want %d", code, websocket.CloseNormalClosure)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server never observed the close")
	}
}

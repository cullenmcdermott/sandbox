// Command chatmock is a throwaway design lab for the chat transcript. It renders
// the SAME sample conversation several ways so layout/styling decisions can be
// compared live, then thrown away. It is NOT wired into the real `sandbox`
// binary — it only imports the shared tui/theme + internal/terminal packages so
// the colors and the Kitty-graphics path are byte-identical to production.
//
//	go run ./cmd/chatmock
//
// Keys:
//
//	1..5         pick a design variant
//	tab / S-tab  cycle variants
//	t            cycle theme (Midnight / Daylight / Ember)
//	i            toggle a real inline image (Kitty graphics; Ghostty only)
//	l            cycle the header phase (working / loading-replay / ready) —
//	             demonstrates a distinct "loading old chat" state vs "working…"
//	j/k ↑/↓      scroll the transcript
//	q / esc      quit
package main

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/internal/terminal"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// phase is the header/working-indicator state. It exists to make the point of
// requirement #1 concrete: replaying an old transcript should read as LOADING,
// visually distinct from the model actively WORKING.
type phase int

const (
	phaseWorking phase = iota // model is producing a turn
	phaseLoading              // replaying / catching up an old transcript
	phaseReady                // idle, ready for input
)

func (p phase) String() string {
	switch p {
	case phaseWorking:
		return "working"
	case phaseLoading:
		return "loading-replay"
	default:
		return "ready"
	}
}

type model struct {
	width, height int
	variant       int // index into variants
	caps          terminal.Caps
	scroll        int
	phase         phase

	// Kitty image demo state, mirroring the production one-shot transmit pattern
	// (statusline.go): pendingImg rides exactly one frame; placeholders ride
	// every frame and scroll with the body.
	imageOn    bool
	imgID      uint32
	imgKey     string // theme name the current image was rasterized for
	pendingImg string
}

func main() {
	m := &model{
		caps:    terminal.Detect(),
		variant: idxCalm, // open on the recommended direction; press 1 for "Today"
		phase:   phaseReady,
	}

	// Headless screenshot mode: `CHATMOCK_SHOT=1 [CHATMOCK_VARIANT=3] go run ./cmd/chatmock`
	// prints one frame to stdout and exits — no TTY needed, handy for sharing.
	if os.Getenv("CHATMOCK_SHOT") != "" {
		if v := os.Getenv("CHATMOCK_VARIANT"); v != "" && v[0] >= '1' && int(v[0]-'1') < len(variants) {
			m.variant = int(v[0] - '1')
		}
		m.imageOn = os.Getenv("CHATMOCK_IMAGE") != ""
		m.width, m.height = 110, 44
		fmt.Println(m.frame())
		return
	}

	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "chatmock:", err)
		os.Exit(1)
	}
}

func (m *model) Init() tea.Cmd { return tea.RequestBackgroundColor }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.BackgroundColorMsg:
		// Adapt the default palette to the terminal background, like the real app.
		theme.ApplyForBackground(msg.IsDark())
		m.invalidateImage()
		return m, nil

	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			m.scrollBy(-3)
		case tea.MouseWheelDown:
			m.scrollBy(3)
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg.String())
	}
	return m, nil
}

func (m *model) handleKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "1", "2", "3", "4", "5":
		if i := int(k[0] - '1'); i < len(variants) {
			m.variant, m.scroll = i, 0
		}
	case "tab":
		m.variant = (m.variant + 1) % len(variants)
		m.scroll = 0
	case "shift+tab":
		m.variant = (m.variant - 1 + len(variants)) % len(variants)
		m.scroll = 0
	case "t":
		theme.Cycle()
		m.invalidateImage()
	case "i":
		m.imageOn = !m.imageOn
	case "l":
		m.phase = phase((int(m.phase) + 1) % 3)
	case "j", "down":
		m.scrollBy(1)
	case "k", "up":
		m.scrollBy(-1)
	case "ctrl+d", "pgdown", "pgdn":
		m.scrollBy(10)
	case "ctrl+u", "pgup":
		m.scrollBy(-10)
	case "g", "home":
		m.scroll = 0
	case "G", "end":
		m.scroll = 1 << 30
	}
	return m, nil
}

func (m *model) scrollBy(n int) { m.scroll += n } // clamped at render time

func (m *model) invalidateImage() {
	// Force the next frame to re-rasterize + retransmit for the new palette.
	m.imgKey = ""
}

func (m *model) View() tea.View {
	if m.width == 0 {
		v := tea.NewView("loading…")
		v.AltScreen = true
		return v
	}
	v := tea.NewView(m.frame())
	v.AltScreen = true
	return v
}

// frame builds the full-screen string for the current state (factored out of
// View so the headless screenshot mode can reuse it).
func (m *model) frame() string {
	ch := variants[m.variant]

	header := m.renderHeader(ch)
	bottom := m.renderBottom(ch)

	headerH := countLines(header)
	bottomH := countLines(bottom)
	bodyH := m.height - headerH - bottomH - 2 // 2 spacer rows
	if bodyH < 1 {
		bodyH = 1
	}

	bodyLines := m.transcriptLines(ch)
	// Clamp scroll.
	maxScroll := len(bodyLines) - bodyH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
	window := bodyLines[m.scroll:min(m.scroll+bodyH, len(bodyLines))]
	for len(window) < bodyH {
		window = append(window, "")
	}

	frame := strings.Join([]string{
		header,
		"",
		strings.Join(window, "\n"),
		"",
		bottom,
	}, "\n")

	// Prepend any one-shot Kitty transmission, exactly like the production gauge.
	if m.pendingImg != "" {
		frame = m.pendingImg + frame
		m.pendingImg = ""
	}
	return frame
}

func countLines(s string) int { return strings.Count(s, "\n") + 1 }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// rightAlign places left and right on one w-wide line with a gap between.
func rightAlign(left, right string, w int) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

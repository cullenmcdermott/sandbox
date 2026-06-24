package dashboard

// states.go — Tier-4 state design (design-system-and-states.md §2): deliberate
// first-run / loading / no-match states built from the kit, plus the injectable
// clock for golden determinism (§4.2).
//
// Also: ConnectStage enum (U1, spec 04-ux-responsiveness §U1.1) — the coarse
// user-facing phases of establishing a session connection, used by connectingView
// and threaded through Connector so the CLI layer can report progress.

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// ConnectStage is a coarse, user-facing phase of establishing a session
// connection. Ordered; the stepper checks off everything below the current
// stage. The numeric order MUST match the emission order in
// sessionConnector.connect (internal/cli/connect.go): the stepper marks a stage
// done when its value is below the current one, so a stage emitted later that
// sorted earlier would make the checklist visibly regress (and, since the
// current stage would then sit between two displayed stages, render no spinner —
// the screen looks frozen). connect emits Sync *before* Opencode, so StageSync
// must precede StageOpencode here.
type ConnectStage int

const (
	StageCheck    ConnectStage = iota // querying session state
	StageResume                       // scaling the pod up / waiting for it to schedule+pull+ready
	StageForward                      // establishing the port-forward
	StageRunner                       // waiting for the runner /healthz
	StageSync                         // (re)starting file sync
	StageOpencode                     // (opencode only) waiting for opencode serve to listen
	StageAttach                       // done — handing off to the transcript/pane
)

// opencodeConnectStages is the stepper's displayed lifecycle for opencode-server
// sessions: the same stages as the default plus StageOpencode (the wait for
// `opencode serve` to bind), which the default list omits. Threading this into
// connectingStepper for opencode connects keeps the current stage in the
// displayed set so it always renders a live spinner instead of looking stuck.
var opencodeConnectStages = []ConnectStage{
	StageCheck, StageResume, StageForward, StageRunner, StageSync, StageOpencode, StageAttach,
}

// connectStageLabel returns the user-facing label for a connect stage.
func connectStageLabel(s ConnectStage) string {
	switch s {
	case StageCheck:
		return "Checking session"
	case StageResume:
		return "Starting pod"
	case StageForward:
		return "Port-forwarding"
	case StageRunner:
		return "Waiting for runner"
	case StageOpencode:
		return "Starting opencode"
	case StageSync:
		return "Syncing files"
	case StageAttach:
		return "Attaching"
	default:
		return fmt.Sprintf("Stage %d", int(s))
	}
}

// nowFunc is the injectable clock for time-derived rendered strings (relative
// times, elapsed) so golden snapshots stay byte-stable (§4.2).
var nowFunc = time.Now

// firstRunView renders the first-run welcome shown when there are no sessions
// and never have been: what this is, the primary CTA (n — new session), and a
// couple of example next steps, centered in the viewport.
func (m *Model) firstRunView(width, height int) string {
	title := theme.GradientText("sandbox", true, theme.Charple, theme.Dolly)
	tagline := lipgloss.NewStyle().Foreground(theme.TextBody).Render("a command center for your coding agents")
	cta := kit.KbdRow([2]string{"n", "new session"})
	steps := lipgloss.NewStyle().Foreground(theme.TextMuted).Render("then: type to chat · ") +
		kit.KbdRow([2]string{"⌃K", "switch sessions"}, [2]string{"q", "detach"})
	body := strings.Join([]string{title, "", tagline, "", cta, steps}, "\n")
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, body, pageWhitespace())
}

// noMatchCopy is the distinct empty copy shown when a filter matches nothing,
// naming the query and the way out — so an over-narrow filter doesn't look like
// an empty cluster.
func noMatchCopy(query string) string {
	return lipgloss.NewStyle().Foreground(theme.TextMuted).Render(fmt.Sprintf("No sessions match %q  ", query)) +
		kit.Kbd("esc", "clear")
}

// connectingStepper renders the connect lifecycle as a checklist: stages before
// the current one are checked (✓), the current one shows an animated spinner,
// later ones are dim placeholders (○).
//
// stage is the current ConnectStage (int cast); frame is the spinner frame index
// for the animated current-stage mark (U1.5). detail is an optional live
// sub-status appended to the current stage's label (e.g. "Syncing files —
// uploading"); "" shows none. If applicable is non-empty, only those stages are
// shown; pass nil to show all stages.
func connectingStepper(stage ConnectStage, frame int, detail string, applicable []ConnectStage) string {
	stages := applicable
	if len(stages) == 0 {
		stages = []ConnectStage{StageCheck, StageResume, StageForward, StageRunner, StageSync, StageAttach}
	}
	lines := make([]string, len(stages))
	for i, s := range stages {
		var mark string
		switch {
		case s < stage:
			mark = lipgloss.NewStyle().Foreground(theme.Guac).Render("✓")
		case s == stage:
			mark = theme.SpinnerFrame(frame)
		default:
			mark = lipgloss.NewStyle().Foreground(theme.TextDim).Render("○")
		}
		label := connectStageLabel(s)
		if s == stage && detail != "" {
			label += lipgloss.NewStyle().Foreground(theme.TextMuted).Render(" — " + detail)
		}
		lines[i] = mark + " " + lipgloss.NewStyle().Foreground(theme.TextBody).Render(label)
	}
	return strings.Join(lines, "\n")
}

// skeletonRows renders n dim placeholder bars at the list rhythm so the layout
// does not jump when real rows arrive.
func skeletonRows(n, width int) string {
	bar := strings.Repeat("░", max(1, width-4))
	row := "  " + lipgloss.NewStyle().Foreground(theme.TextDim).Render(bar)
	lines := make([]string, n)
	for i := range lines {
		lines[i] = row
	}
	return strings.Join(lines, "\n")
}

package main

import "strings"

// toolSpec is a mocked tool call shown as a card before the assistant text.
type toolSpec struct {
	name, arg, result string
}

// reply is one canned assistant turn: an optional tool call followed by streamed
// text.
type reply struct {
	tool *toolSpec
	text string
}

// scriptedReply returns a canned response, lightly keyed off the user's input so
// the conversation feels responsive even though nothing is real.
func scriptedReply(turn int, input string) *reply {
	in := strings.ToLower(input)
	switch {
	case strings.Contains(in, "test"):
		return &reply{
			tool: &toolSpec{name: "Bash", arg: "go test ./...", result: "ok  4 packages  (0.91s)"},
			text: "All four packages pass. The suite is green — the list, kit, anim, and " +
				"terminal packages each have their own table-driven tests, so a regression " +
				"in one won't hide behind another.",
		}
	case strings.Contains(in, "kitty") || strings.Contains(in, "image"):
		return &reply{
			text: "Look up at the context gauge in the header — on a Kitty-graphics terminal " +
				"(Ghostty) that bar is a REAL inline image: an RGBA bitmap transmitted over the " +
				"APC _G protocol and placed with Unicode placeholder cells, so it stays width-correct " +
				"inside the alt-screen. On any other terminal it falls back to a block bar.",
		}
	case strings.Contains(in, "theme"):
		return &reply{
			text: "Press ctrl+t to cycle themes — Midnight, Daylight, Ember. Every color you see " +
				"comes from semantic tokens, so the whole UI re-skins with zero layout change. The " +
				"OnChange hook rebuilds derived styles on each swap.",
		}
	case strings.Contains(in, "read") || strings.Contains(in, "file") || strings.Contains(in, "code"):
		return &reply{
			tool: &toolSpec{name: "Read", arg: "main.go", result: "→ 247 lines, 6.1 KB"},
			text: "Read it. The whole demo is built only from the public tui/ packages — it " +
				"imports nothing from internal/, which is the point: any other Bubble Tea app can " +
				"lift these components wholesale.",
		}
	}

	// Default rotation for anything else.
	defaults := []*reply{
		{text: "Good question. This response is streaming in word by word on a single gated " +
			"~30fps tick — the same loop the spinner rides. When the turn finishes, the tick " +
			"loop stops entirely, so an idle chat costs nothing."},
		{
			tool: &toolSpec{name: "Grep", arg: "\"func \" tui/", result: "84 matches in 14 files"},
			text: "The component surface is small but composable: stateless render helpers in kit, " +
				"a virtualized list, a motion engine, and a theme registry. That's the whole kit.",
		},
		{text: "Try ctrl+g to see what the terminal-capability detector found on your host, " +
			"ctrl+n to reopen the model picker, or ctrl+t to change the theme."},
	}
	return defaults[turn%len(defaults)]
}

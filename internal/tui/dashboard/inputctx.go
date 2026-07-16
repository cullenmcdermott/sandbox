package dashboard

// inputctx.go — the shared machinery for context-resolved key dispatch (§2a
// input contexts): precedence is table order (not if-chain nesting), each key
// carries a key.Binding so its help text can never drift from the handler, and
// a when-gate decides both dispatch and (later) help rendering. The transcript's
// concrete tables and sub-context resolver live in transcript_input.go, next to
// their use.

import (
	"sort"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// boundAction is one entry in an input context's ordered binding table:
// precedence is table order, help text lives on the binding, and `when` gates
// BOTH dispatch and (later) help rendering. run reports handled=false to keep
// walking (try-entries like space-toggle-subagents that only act sometimes).
// footerRank places the entry in the footer: 0 = not in the footer; >0 = footer
// position, ascending.
type boundAction[M any] struct {
	binding    key.Binding
	when       func(m M) bool // nil = always
	run        func(m M, msg tea.KeyPressMsg) (tea.Cmd, bool)
	footerRank int
}

// dispatchKey walks a table and runs the first entry whose binding matches,
// whose when-gate passes, and whose run reports handled. It returns (nil, false)
// when nothing claimed the key so the caller can fall through.
func dispatchKey[M any](m M, table []boundAction[M], msg tea.KeyPressMsg) (tea.Cmd, bool) {
	for _, entry := range table {
		if !key.Matches(msg, entry.binding) {
			continue
		}
		if entry.when != nil && !entry.when(m) {
			continue
		}
		if cmd, handled := entry.run(m, msg); handled {
			return cmd, true
		}
	}
	return nil, false
}

// footerBindings returns the table's footer entries — footerRank > 0, when-gate
// passing — ordered by rank. The footer renders from the SAME entries that
// dispatch, so its advertising can never lie (§2d q/g).
func footerBindings[M any](m M, table []boundAction[M]) []key.Binding {
	type ranked struct {
		rank int
		bind key.Binding
	}
	var rs []ranked
	for _, entry := range table {
		if entry.footerRank <= 0 {
			continue
		}
		if entry.when != nil && !entry.when(m) {
			continue
		}
		rs = append(rs, ranked{rank: entry.footerRank, bind: entry.binding})
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].rank < rs[j].rank })
	out := make([]key.Binding, len(rs))
	for i, r := range rs {
		out[i] = r.bind
	}
	return out
}

// CANONICAL TEST — do not weaken.
package chat

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/tui/list"
)

// ORACLE: appending while at bottom preserves the bottom pin when caller GotoBottoms.
func TestBottomPinOnAppend(t *testing.T) {
	items := []list.Item{
		NewUserItem("first"),
		NewUserItem("second"),
		NewUserItem("third"),
	}
	l := list.New(items...)
	l.SetSize(40, 2)
	l.GotoBottom()
	if !l.AtBottom() {
		t.Fatalf("expected AtBottom after GotoBottom")
	}
	wasBottom := l.AtBottom()
	l.AppendItems(NewUserItem("fourth"))
	if wasBottom {
		l.GotoBottom()
	}
	if !l.AtBottom() {
		t.Fatalf("appending while at bottom should stay at bottom after GotoBottom")
	}
}

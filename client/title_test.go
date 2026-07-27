package client

import (
	"errors"
	"testing"
)

func offlineTitleClient(t *testing.T) *Client {
	t.Helper()
	c, err := Offline(WithStateDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Offline: %v", err)
	}
	return c
}

func TestTitleUnknownIDIsZeroNoError(t *testing.T) {
	c := offlineTitleClient(t)
	title, err := c.Title("does-not-exist")
	if err != nil {
		t.Fatalf("Title on unknown id: %v", err)
	}
	if title != (Title{}) {
		t.Errorf("Title on unknown id = %+v, want zero value", title)
	}
}

func TestSetTitleRoundTrip(t *testing.T) {
	c := offlineTitleClient(t)
	const id = "claude-pane-abc"

	if err := c.SetTitle(id, "My Session"); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}
	got, err := c.Title(id)
	if err != nil {
		t.Fatalf("Title: %v", err)
	}
	if got.Name != "My Session" {
		t.Errorf("Name = %q, want %q", got.Name, "My Session")
	}
	if got.Display() != "My Session" {
		t.Errorf("Display() = %q, want %q", got.Display(), "My Session")
	}
}

func TestSetTitleRejectsBlank(t *testing.T) {
	c := offlineTitleClient(t)
	const id = "claude-pane-blank"

	cases := []string{"", "   ", "\t\n"}
	for _, name := range cases {
		if err := c.SetTitle(id, name); !errors.Is(err, ErrEmptyTitle) {
			t.Errorf("SetTitle(%q) = %v, want ErrEmptyTitle", name, err)
		}
	}
	// No entry should have been written.
	got, err := c.Title(id)
	if err != nil {
		t.Fatalf("Title: %v", err)
	}
	if got != (Title{}) {
		t.Errorf("Title after rejected SetTitle = %+v, want zero value", got)
	}
}

func TestSetAutoTitleDoesNotClobberName(t *testing.T) {
	c := offlineTitleClient(t)
	const id = "claude-pane-auto"

	if err := c.SetTitle(id, "Chosen Name"); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}
	if err := c.SetAutoTitle(id, "agent generated summary"); err != nil {
		t.Fatalf("SetAutoTitle: %v", err)
	}
	got, err := c.Title(id)
	if err != nil {
		t.Fatalf("Title: %v", err)
	}
	if got.Name != "Chosen Name" {
		t.Errorf("Name = %q, want %q (SetAutoTitle must not clobber a chosen Name)", got.Name, "Chosen Name")
	}
	if got.Auto != "agent generated summary" {
		t.Errorf("Auto = %q, want %q", got.Auto, "agent generated summary")
	}
	if got.Display() != "Chosen Name" {
		t.Errorf("Display() = %q, want %q (Name wins)", got.Display(), "Chosen Name")
	}
}

func TestSetAutoTitleBlankIsNoOp(t *testing.T) {
	c := offlineTitleClient(t)
	const id = "claude-pane-auto-blank"

	if err := c.SetAutoTitle(id, ""); err != nil {
		t.Fatalf("SetAutoTitle(blank): %v", err)
	}
	if err := c.SetAutoTitle(id, "   "); err != nil {
		t.Fatalf("SetAutoTitle(whitespace): %v", err)
	}
	got, err := c.Title(id)
	if err != nil {
		t.Fatalf("Title: %v", err)
	}
	if got != (Title{}) {
		t.Errorf("Title after blank SetAutoTitle = %+v, want zero value (no entry created)", got)
	}
}

func TestTitleDisplayPrecedence(t *testing.T) {
	cases := []struct {
		name string
		t    Title
		want string
	}{
		{"both set: Name wins", Title{Name: "chosen", Auto: "auto"}, "chosen"},
		{"only Auto", Title{Auto: "auto"}, "auto"},
		{"neither set", Title{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.t.Display(); got != c.want {
				t.Errorf("Display() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestSetAutoTitleThenSetTitleDisplaysName(t *testing.T) {
	// Auto set first, then a rename — Name must win.
	c := offlineTitleClient(t)
	const id = "claude-pane-order"

	if err := c.SetAutoTitle(id, "auto summary"); err != nil {
		t.Fatalf("SetAutoTitle: %v", err)
	}
	if err := c.SetTitle(id, "renamed"); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}
	got, err := c.Title(id)
	if err != nil {
		t.Fatalf("Title: %v", err)
	}
	if got.Display() != "renamed" {
		t.Errorf("Display() = %q, want %q", got.Display(), "renamed")
	}
	if got.Auto != "auto summary" {
		t.Errorf("Auto = %q, want preserved %q", got.Auto, "auto summary")
	}
}

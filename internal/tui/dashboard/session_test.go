package dashboard

import (
	"testing"
)

// REGRESSION (D5): formatInt must handle negative input (clock skew can make
// relative-time calculations negative). Prior code returned "" for negatives.
func TestFormatIntNegative(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{-42, "-42"},
		{-1, "-1"},
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{12345, "12345"},
	}
	for _, c := range cases {
		if got := formatInt(c.n); got != c.want {
			t.Errorf("formatInt(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

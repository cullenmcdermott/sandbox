package dashboard

// dirpicker_path_test.go — table tests for the directory picker's pure path
// helpers (Tab-completion, longest common prefix, home abbreviation). The
// shared normalization itself (~-expansion, missing-dir rejection) is table-
// tested where it lives, in internal/projpath.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLongestCommonPrefix(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"single", []string{"alpha"}, "alpha"},
		{"shared prefix", []string{"alpha", "alphabet"}, "alpha"},
		{"no common", []string{"alpha", "beta"}, ""},
		{"identical", []string{"same", "same"}, "same"},
		{"prefix is a member", []string{"al", "alpha"}, "al"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := longestCommonPrefix(tc.in); got != tc.want {
				t.Errorf("longestCommonPrefix(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestAbbreviateHome(t *testing.T) {
	home := filepath.Join("/home", "u")
	cases := []struct {
		name, path, home, want string
	}{
		{"inside home", filepath.Join(home, "git", "p"), home, "~" + string(filepath.Separator) + filepath.Join("git", "p")},
		{"home itself", home, home, "~"},
		{"outside home", "/srv/p", home, "/srv/p"},
		{"sibling of home not abbreviated", "/home/u2/p", home, "/home/u2/p"},
		{"no home", "/srv/p", "", "/srv/p"},
		{"empty path", "", home, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := abbreviateHome(tc.path, tc.home); got != tc.want {
				t.Errorf("abbreviateHome(%q, %q) = %q, want %q", tc.path, tc.home, got, tc.want)
			}
		})
	}
}

func TestCompleteDirPath(t *testing.T) {
	base := t.TempDir()
	for _, d := range []string{"alpha", "alphabet", "beta", ".hidden", ".hidden2"} {
		if err := os.Mkdir(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A regular file must never complete (a project path is a directory).
	if err := os.WriteFile(filepath.Join(base, "alfile"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	sep := string(filepath.Separator)

	cases := []struct {
		name, in, home, want string
	}{
		{"ambiguous extends to common prefix", filepath.Join(base, "al"), "", filepath.Join(base, "alpha")},
		{"unique completes with separator", filepath.Join(base, "be"), "", filepath.Join(base, "beta") + sep},
		{"no match unchanged", filepath.Join(base, "zz"), "", filepath.Join(base, "zz")},
		{"hidden skipped without dot partial", filepath.Join(base, "") + sep, "", filepath.Join(base, "") + sep},
		{"hidden offered with dot partial", filepath.Join(base, ".hi"), "", filepath.Join(base, ".hidden")},
		{"file not completed", filepath.Join(base, "alf"), "", filepath.Join(base, "alf")},
		{"tilde form preserved", "~" + sep + "al", base, "~" + sep + "alpha"},
		{"empty input unchanged", "", base, ""},
		{"unreadable parent unchanged", filepath.Join(base, "zz", "al"), "", filepath.Join(base, "zz", "al")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := completeDirPath(tc.in, tc.home); got != tc.want {
				t.Errorf("completeDirPath(%q, home=%q) = %q, want %q", tc.in, tc.home, got, tc.want)
			}
		})
	}
}

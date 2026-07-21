package projpath

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExpandUser(t *testing.T) {
	home := "/home/u"
	cases := []struct {
		name, in, home, want string
	}{
		{"bare tilde", "~", home, "/home/u"},
		{"tilde slash", "~/git/proj", home, "/home/u/git/proj"},
		{"tilde user form untouched", "~alice/proj", home, "~alice/proj"},
		{"absolute untouched", "/srv/proj", home, "/srv/proj"},
		{"relative untouched", "proj", home, "proj"},
		{"mid-string tilde untouched", "/srv/~proj", home, "/srv/~proj"},
		{"empty home disables expansion", "~/git", "", "~/git"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExpandUser(tc.in, tc.home); got != tc.want {
				t.Errorf("ExpandUser(%q, %q) = %q, want %q", tc.in, tc.home, got, tc.want)
			}
		})
	}
}

func TestCanonicalizeResolvesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows") // gate-ok: platform guard; darwin/linux (all dev + CI hosts) run the symlink path for real
	}
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	got, err := Canonicalize(link)
	if err != nil {
		t.Fatalf("Canonicalize(%q): %v", link, err)
	}
	// Canonicalize the target the same way (macOS's /tmp is itself a symlink to
	// /private/tmp, so comparing against `real` verbatim would be flaky).
	want, err := Canonicalize(real)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("Canonicalize(link) = %q, want the symlink target %q", got, want)
	}
}

func TestCanonicalizeMissingPathFallsBackToAbs(t *testing.T) {
	// A nonexistent path can't be symlink-resolved; Canonicalize returns the
	// absolute form rather than an error (existence is ValidateDir's job).
	missing := filepath.Join(t.TempDir(), "nope")
	got, err := Canonicalize(missing)
	if err != nil {
		t.Fatalf("Canonicalize(%q): %v", missing, err)
	}
	if !filepath.IsAbs(got) || !strings.HasSuffix(got, "nope") {
		t.Errorf("Canonicalize(%q) = %q, want an absolute path ending in \"nope\"", missing, got)
	}
}

func TestValidateDir(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "proj")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantSub, err := Canonicalize(sub)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("existing dir normalizes", func(t *testing.T) {
		got, err := ValidateDir(sub, "")
		if err != nil {
			t.Fatalf("ValidateDir(%q): %v", sub, err)
		}
		if got != wantSub {
			t.Errorf("ValidateDir(%q) = %q, want %q", sub, got, wantSub)
		}
	})

	t.Run("surrounding whitespace trimmed", func(t *testing.T) {
		got, err := ValidateDir("  "+sub+" ", "")
		if err != nil {
			t.Fatalf("ValidateDir with whitespace: %v", err)
		}
		if got != wantSub {
			t.Errorf("got %q, want %q", got, wantSub)
		}
	})

	t.Run("tilde expands against home", func(t *testing.T) {
		// Treat the temp dir as home: "~/proj" must land on <dir>/proj.
		got, err := ValidateDir("~/proj", dir)
		if err != nil {
			t.Fatalf("ValidateDir(~/proj): %v", err)
		}
		if got != wantSub {
			t.Errorf("ValidateDir(~/proj) = %q, want %q", got, wantSub)
		}
	})

	t.Run("missing dir rejected", func(t *testing.T) {
		if _, err := ValidateDir(filepath.Join(dir, "missing"), ""); err == nil {
			t.Error("ValidateDir accepted a nonexistent directory")
		} else if !strings.Contains(err.Error(), "does not exist") {
			t.Errorf("missing-dir error = %v, want a does-not-exist message", err)
		}
	})

	t.Run("regular file rejected", func(t *testing.T) {
		if _, err := ValidateDir(file, ""); err == nil {
			t.Error("ValidateDir accepted a regular file")
		} else if !strings.Contains(err.Error(), "not a directory") {
			t.Errorf("file error = %v, want a not-a-directory message", err)
		}
	})

	t.Run("empty rejected", func(t *testing.T) {
		for _, in := range []string{"", "   "} {
			if _, err := ValidateDir(in, ""); err == nil {
				t.Errorf("ValidateDir(%q) accepted an empty path", in)
			}
		}
	})
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExpandHomeNeverYieldsLiteralHome is the regression guard for the bug that
// created a directory literally named "$HOME" and leaked 11 personal notes into
// the repository. viper does not expand shell variables and filepath.Abs only
// resolves against the CWD, so the old code produced "<cwd>/$HOME/memoire-data".
func TestExpandHomeNeverYieldsLiteralHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot determine home dir: %v", err)
	}

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"dollar HOME prefix", "$HOME/memoire-data", filepath.Join(home, "memoire-data")},
		{"braced HOME prefix", "${HOME}/memoire-data", filepath.Join(home, "memoire-data")},
		{"tilde prefix", "~/memoire-data", filepath.Join(home, "memoire-data")},
		{"bare dollar HOME", "$HOME", home},
		{"bare tilde", "~", home},
		{"nested path", "$HOME/a/b/c", filepath.Join(home, "a", "b", "c")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := expandHome(tc.input)
			if err != nil {
				t.Fatalf("expandHome(%q): %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("expandHome(%q) = %q, want %q", tc.input, got, tc.want)
			}
			// The critical assertion: the literal token must be gone.
			if strings.Contains(got, "$HOME") || strings.Contains(got, "~") {
				t.Errorf("expandHome(%q) left an unexpanded token: %q", tc.input, got)
			}
		})
	}
}

// TestExpandHomeIsAbsoluteAfterAbs mirrors what main() does: expand, then Abs.
// The result must live under the real home directory, not under the CWD.
func TestExpandHomeIsAbsoluteAfterAbs(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot determine home dir: %v", err)
	}

	expanded, err := expandHome("$HOME/memoire-data")
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(abs, home) {
		t.Errorf("resolved data dir %q is not under the home dir %q", abs, home)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// The old bug produced "<cwd>/$HOME/memoire-data".
	if strings.HasPrefix(abs, filepath.Join(cwd, "$HOME")) {
		t.Errorf("resolved data dir is still under a literal $HOME directory: %q", abs)
	}
}

func TestExpandHomeLeavesOrdinaryPathsAlone(t *testing.T) {
	for _, p := range []string{"/var/lib/memoire", "relative/path", "C:\\data\\memoire"} {
		got, err := expandHome(p)
		if err != nil {
			t.Fatalf("expandHome(%q): %v", p, err)
		}
		if got != p {
			t.Errorf("expandHome(%q) = %q, expected it unchanged", p, got)
		}
	}
}

func TestExpandHomeRejectsEmpty(t *testing.T) {
	if _, err := expandHome(""); err == nil {
		t.Error("expected an error for an empty path")
	}
}

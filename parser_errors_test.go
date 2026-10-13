package rotini

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// An unknown short flag with a non-ASCII letter is reported whole, as valid UTF-8.
func TestParseCluster_nonASCII(t *testing.T) {
	def := Definition{Name: "app", Flags: []FlagDef{{Name: "v", Identifiers: []string{"-v"}, Type: "bool"}}}
	for argv, want := range map[string]string{"-vé": "-é", "-ü": "-ü"} {
		var in struct{}
		err := NewParser().Parse(NewContextFor(def, []string{argv}), &in)
		var pe *ParseError
		if !errors.As(err, &pe) || pe.Kind != ParseKindUnknownFlag {
			t.Fatalf("%s: %v, want an unknown flag", argv, err)
		}
		if pe.Token != want || !utf8.ValidString(pe.Msg) {
			t.Errorf("%s: token %q, msg %q; want token %q in valid UTF-8", argv, pe.Token, pe.Msg, want)
		}
	}
}

// A dependency's equals compares integers exactly, above 2^53 too.
func TestDependencyCanonical_integers(t *testing.T) {
	fd := FlagDef{Type: "int64"}
	if a, b := dependencyCanonical(fd, "9007199254740993"), dependencyCanonical(fd, "9007199254740992"); a == b {
		t.Errorf("2^53+1 and 2^53 both canonicalize to %q", a)
	}
	if got := dependencyCanonical(fd, "+007"); got != "7" {
		t.Errorf("+007 = %q, want 7", got)
	}
	if got := dependencyCanonical(FlagDef{Type: "float64"}, "1.50"); got != "1.5" {
		t.Errorf("float 1.50 = %q, want 1.5", got)
	}
}

// A bound, length, pattern, group or dependency violation and a missing required input say
// which command (and, where there is one, which flag) they concern.
func TestParseError_flagAndCommand(t *testing.T) {
	maxN, maxLen := 5.0, 3
	def := Definition{Name: "app", Flags: []FlagDef{
		{Name: "n", Identifiers: []string{"--n"}, Type: "int", Maximum: &maxN},
		{Name: "s", Identifiers: []string{"--s"}, Type: "string", MaxLength: &maxLen},
		{Name: "req", Identifiers: []string{"--req"}, Type: "string", Required: true},
		{Name: "a", Identifiers: []string{"--a"}, Type: "bool"},
		{Name: "b", Identifiers: []string{"--b"}, Type: "bool"},
	}, FlagGroups: []FlagGroup{{Kind: FlagGroupMutuallyExclusive, Flags: []string{"a", "b"}}}}
	for _, tc := range []struct {
		argv []string
		flag string
	}{
		{[]string{"--req", "x", "--n", "9"}, "--n"},
		{[]string{"--req", "x", "--s", "abcdef"}, "--s"},
		{[]string{}, "--req"},
		{[]string{"--req", "x", "--a", "--b"}, ""},
	} {
		var in struct{}
		err := NewParser().Parse(NewContextFor(def, tc.argv), &in)
		var pe *ParseError
		if !errors.As(err, &pe) {
			t.Fatalf("%q: %v, want a ParseError", tc.argv, err)
		}
		if pe.Command != "app" || pe.Flag != tc.flag {
			t.Errorf("%q: Command %q, Flag %q; want app, %q", tc.argv, pe.Command, pe.Flag, tc.flag)
		}
	}
}

// An @file value larger than the cap is a usage error, not a value.
func TestResolveAcquired_cap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big")
	if err := os.WriteFile(path, make([]byte, valueFileCap+1), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := resolveAcquired([]string{"file"}, "--data", "--data", "@"+path, argvAcq{})
	if err == nil || !strings.Contains(err.Error(), "larger than 64 MiB") {
		t.Errorf("err = %v, want the size error", err)
	}
	if err := os.WriteFile(path, []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if v, err := resolveAcquired([]string{"file"}, "--data", "--data", "@"+path, argvAcq{}); err != nil || v != "ok" {
		t.Errorf("small file: %q, %v", v, err)
	}
}

// The quiet parses never expand a glob argument, even with Windows rules, so they read no
// directory and count words as typed.
func TestQuietChain_noGlob(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	chain := []Command{{Name: "app", Arguments: []ArgDef{{Name: "files", Type: "[]string", Variadic: true, Glob: true}}}}
	pattern := filepath.Join(dir, "*.txt")
	store, err := parseArgvTokens(quietChain(chain), []string{pattern}, argvAcq{goos: "windows"})
	if err != nil {
		t.Fatal(err)
	}
	if got := store.scopes[0].args; !slices.Equal(got, []string{pattern}) {
		t.Errorf("args = %q, want the pattern as typed", got)
	}
}

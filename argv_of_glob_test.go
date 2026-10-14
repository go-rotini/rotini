package rotini

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

type argvGlobInputs struct {
	Cp struct {
		Flags     struct{}
		Arguments struct {
			Files []string `rotini:"files"`
			Dest  string   `rotini:"dest"`
		}
	}
}

func argvGlobDef() Definition {
	return Definition{
		Name: "cp", Handler: "Cp",
		Arguments: []ArgDef{
			{Name: "files", Type: "[]string", Variadic: true, Glob: true},
			{Name: "dest", Type: "string"},
		},
		Inputs: reflect.TypeFor[argvGlobInputs](),
	}
}

// argvGlobRoundTrip writes files and dest with ArgvOf for goos, parses the result as a run in
// dir on goos would, and returns argv and the words the arguments read back.
func argvGlobRoundTrip(t *testing.T, goos, dir string, files []string, dest string) (argv, got []string) {
	t.Helper()
	var in argvGlobInputs
	in.Cp.Arguments.Files, in.Cp.Arguments.Dest = files, dest
	argv, _, err := argvOf(goos, argvGlobDef(), in, Presence{"Cp.Arguments.Files": {}, "Cp.Arguments.Dest": {}})
	if err != nil {
		t.Fatalf("ArgvOf: %v", err)
	}
	chain := NewContextFor(argvGlobDef(), nil).CommandChain()
	store, err := parseArgvTokens(chain, argv, argvAcq{dir: dir, goos: goos})
	if err != nil {
		t.Fatalf("parse %q: %v", argv, err)
	}
	return argv, store.scopes[0].args
}

func TestArgvOf_globArgumentsOnWindows(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file[1].txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	files := []string{"file[1].txt", "a*b?c[d]", "-", "plain", "*.txt"}
	tests := []struct {
		goos string
		want []string
	}{
		{"windows", []string{"--", "file[[]1].txt", "a[*]b[?]c[[]d]", "-", "plain", "[*].txt", "*.dest"}},
		{"linux", []string{"--", "file[1].txt", "a*b?c[d]", "-", "plain", "*.txt", "*.dest"}},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			t.Parallel()
			argv, got := argvGlobRoundTrip(t, tt.goos, dir, files, "*.dest")
			if !slices.Equal(argv, tt.want) {
				t.Errorf("argv = %q, want %q", argv, tt.want)
			}
			if want := append(slices.Clone(files), "*.dest"); !slices.Equal(got, want) {
				t.Errorf("read back %q, want %q", got, want)
			}
		})
	}
}

func TestExpandGlob_escapes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "file[1].txt", "[[]x", "[x1"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name, word string
		want       []string
	}{
		{"an escaped existing name is that name", "file[[]1].txt", []string{"file[1].txt"}},
		{"an escaped missing name loses its escapes", "nope[*].txt", []string{"nope*.txt"}},
		{"all three escapes", "a[*]b[?]c[[]d]", []string{"a*b?c[d]"}},
		{"a pattern matching nothing loses its escapes", "no[?]*.md", []string{"no?*.md"}},
		{"an unescaped pattern matching nothing is kept", "*.md", []string{"*.md"}},
		{"a pattern with escapes still expands", "[[]x*", []string{"[x1"}},
		{"a word naming an existing path is kept as written", "file[1].txt", []string{"file[1].txt"}},
		{"escapes win over a name spelled with them", "[[]x", []string{"[x"}},
		{"an escaped word is not cleaned", "[*]/..", []string{"*/.."}},
		{"dash is kept", "-", []string{"-"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := expandGlob(dir, tt.word); !slices.Equal(got, tt.want) {
				t.Errorf("expandGlob(%q) = %q, want %q", tt.word, got, tt.want)
			}
		})
	}
}

func TestExpandGlobs_escapesOnlyOnWindows(t *testing.T) {
	t.Parallel()
	def := Definition{Name: "app", Handler: "App", Arguments: []ArgDef{{Name: "files", Type: "[]string", Variadic: true, Glob: true}}}
	chain := NewContextFor(def, nil).CommandChain()
	store, err := parseArgvTokens(chain, []string{"a[*]"}, argvAcq{dir: t.TempDir(), goos: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := store.scopes[0].args, []string{"a[*]"}; !slices.Equal(got, want) {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestUnescapeGlob(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"", "plain", "[", "]", "[]", "[*]", "[[]", "a*b?c[d]", `C:\dir\*.txt`, "[^a]", "?*?"} {
		literal, isPattern := unescapeGlob(escapeGlob(s))
		if literal != s || isPattern {
			t.Errorf("unescapeGlob(escape(%q)) = %q, %v; want %q, false", s, literal, isPattern, s)
		}
	}
	for word, want := range map[string]bool{"*.txt": true, "a[bc]": true, "a?": true, "[*]": false, "a[*": true, "]": false} {
		if _, got := unescapeGlob(word); got != want {
			t.Errorf("unescapeGlob(%q) pattern = %v, want %v", word, got, want)
		}
	}
}

// FuzzArgvOfGlob checks that a glob argument's values written by ArgvOf for Windows read back
// exactly when a run on Windows expands them.
func FuzzArgvOfGlob(f *testing.F) {
	f.Add("file[1].txt", "*.go")
	f.Add("a*b?c[d]", "[*]")
	f.Add("-", `C:\x\[a-z]?`)
	f.Add("", "[[]")
	f.Add("../*", "]")
	f.Add("*/..", "0")
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, s, k string) {
		files := []string{s, k}
		argv, got := argvGlobRoundTrip(t, "windows", dir, files, k)
		if want := append(slices.Clone(files), k); !slices.Equal(got, want) {
			t.Errorf("argv %q read back %q, want %q", argv, got, want)
		}
	})
}

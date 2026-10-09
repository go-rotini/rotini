package rotini

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse_streamPathKinds(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "input", Identifiers: []string{"-i", "--input"}, Type: "inputfile"},
			{Name: "output", Identifiers: []string{"-o", "--output"}, Type: "outputfile"},
		},
		Arguments: []ArgDef{{Name: "files", Type: "[]inputfile", Variadic: true}},
	}
	type inputs struct {
		App struct {
			Flags struct {
				Input  string `rotini:"input"`
				Output string `rotini:"output"`
			}
			Arguments struct {
				Files []string `rotini:"files"`
			}
		}
	}

	cases := []struct {
		name    string
		argv    []string
		wantErr string
	}{
		{name: "files and one dash", argv: []string{"a.txt", "-", "b.txt"}},
		{name: "a dash flag", argv: []string{"-i", "-", "a.txt"}},
		{name: "output to stdout", argv: []string{"-o", "-"}},
		{name: "a new output file", argv: []string{"-o", "new.txt"}},
		{name: "an existing output file", argv: []string{"-o", "a.txt"}},
		{name: "a missing input file", argv: []string{"missing.txt"}, wantErr: `<files>: no such file: "missing.txt"`},
		{name: "a directory as input", argv: []string{"-i", "sub"}, wantErr: `-i: "sub" is a directory, not a file`},
		{name: "a directory as output", argv: []string{"-o", "sub"}, wantErr: `-o: "sub" is a directory`},
		{name: "a missing output directory", argv: []string{"-o", filepath.Join("missing", "out.txt")}, wantErr: `-o: no such directory: "missing` + string(filepath.Separator) + `"`},
		{name: "two dashes in a list", argv: []string{"-", "a.txt", "-"}, wantErr: `<files>: "-" (stdin) can be given only once; <files> already reads it`},
		{name: "a dash flag and a dash argument", argv: []string{"--input", "-", "-"}, wantErr: `<files>: "-" (stdin) can be given only once; --input already reads it`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var in inputs
			err := NewParser().Parse(NewContextFor(def, tc.argv).WithDir(dir), &in)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("parse: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
			if CategoryOf(err) != CategoryUsage {
				t.Errorf("category = %v, want usage", CategoryOf(err))
			}
		})
	}
}

func TestCheckInputs_stdinDashOnce(t *testing.T) {
	t.Parallel()
	def := Definition{
		Name: "app", Handler: "App",
		Flags:     []FlagDef{{Name: "input", Identifiers: []string{"--input"}, Type: "inputfile"}},
		Arguments: []ArgDef{{Name: "files", Type: "[]inputfile", Variadic: true}},
	}
	type inputs struct {
		App struct {
			Flags struct {
				Input string `rotini:"input"`
			}
			Arguments struct {
				Files []string `rotini:"files"`
			}
		}
	}
	var in inputs
	in.App.Flags.Input = "-"
	if err := NewContextFor(def, nil).CheckInputs(in, PresenceOf(in)); err != nil {
		t.Fatalf("one dash: %v", err)
	}
	in.App.Arguments.Files = []string{"-"}
	err := NewContextFor(def, nil).CheckInputs(in, PresenceOf(in))
	want := `<files>: "-" (stdin) can be given only once; --input already reads it`
	if err == nil || !strings.Contains(err.Error(), want) || CategoryOf(err) != CategoryUsage {
		t.Fatalf("CheckInputs = %v, want a usage error containing %q", err, want)
	}
}

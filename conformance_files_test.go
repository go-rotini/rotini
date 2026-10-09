package rotini

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fileDef is a CLI beside the acme fixture for the input-file and output-file cases.
func fileDef() Definition {
	return Definition{
		Name: "files", Handler: "Files",
		Flags:     []FlagDef{{Name: "output", Identifiers: []string{"-o", "--output"}, Type: "outputfile", Default: "-"}},
		Arguments: []ArgDef{{Name: "files", Type: "[]inputfile", Variadic: true}},
	}
}

type fileInputs struct {
	Files struct {
		Flags struct {
			Output string `rotini:"output"`
		}
		Arguments struct {
			Files []string `rotini:"files"`
		}
	}
}

// fileDir is a directory holding a.txt and b.txt.
func fileDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"a.txt": "a\n", "b.txt": "b\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func fileParse(t *testing.T, argv ...string) (fileInputs, error) {
	t.Helper()
	var in fileInputs
	err := NewParser().Parse(NewContextFor(fileDef(), argv).WithDir(fileDir(t)), &in)
	return in, err
}

// fileCat parses argv with stdin piped and copies every file value through OpenInput.
func fileCat(t *testing.T, stdin string, argv ...string) string {
	t.Helper()
	rtx := NewContextFor(fileDef(), argv).WithDir(fileDir(t)).WithStdin(strings.NewReader(stdin))
	var in fileInputs
	if err := NewParser().Parse(rtx, &in); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for _, name := range in.Files.Arguments.Files {
		r, err := OpenInput(rtx, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(&out, r); err != nil {
			t.Fatal(err)
		}
		r.Close()
	}
	return out.String()
}

func fileOutput(t *testing.T) {
	t.Helper()
	var stdout bytes.Buffer
	rtx := NewContextFor(fileDef(), nil).WithDir(fileDir(t)).WithStdout(&stdout)
	var in fileInputs
	if err := NewParser().Parse(rtx, &in); err != nil {
		t.Fatal(err)
	}
	out, err := CreateOutput(rtx, in.Files.Flags.Output)
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(out, "to stdout")
	if err := out.Close(); err != nil || stdout.String() != "to stdout" {
		t.Errorf("-o -: stdout %q, %v", stdout.String(), err)
	}
	if _, err := CreateOutput(rtx, "a.txt"); !errors.Is(err, fs.ErrExist) {
		t.Errorf("an existing file: %v, want fs.ErrExist", err)
	}
}

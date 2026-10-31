//go:build !windows

package rotini_test

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"testing/fstest"

	"github.com/go-rotini/rotini"
)

type catHandler struct{ rotini.NoHooksE }

func (catHandler) Run(_ context.Context, rtx *rotini.Context) error {
	f, err := rotini.OpenInput(rtx, rtx.Argv[0])
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(rtx.Stdout, f)
	return err
}

// A run reads its files from the file system it is given: here an in-memory one, with the run's
// directory /work in it. Relative paths resolve against the run's directory as on disk.
func ExampleProgram_WithFS() {
	fsys := fstest.MapFS{"work/notes.txt": {Data: []byte("buy milk\n")}}
	def := rotini.Definition{Name: "cat", Handler: "Cat", Arguments: []rotini.ArgDef{{Name: "file", Type: "string"}}}
	p := rotini.NewProgramFunc(def, func(string) (rotini.Handler, bool) {
		return rotini.FromHandlerE(catHandler{}), true
	}).WithFS(fsys).WithDir("/work").WithStdout(os.Stdout).WithStderr(os.Stdout)

	p.Run([]string{"notes.txt"})
	p.Run([]string{"todo.txt"})
	// Output:
	// buy milk
	// Error: no such file: "todo.txt"
}

// A LayerFS shows a file system's files under another's at a directory: here a default
// configuration file, as it would be embedded in the binary, read where no file is on disk.
func ExampleLayerFS() {
	shipped := fstest.MapFS{"config.yaml": {Data: []byte("region: eu\n")}}
	fsys := rotini.LayerFS(rotini.OSFS(), "/nonexistent/app", shipped)

	data, err := fs.ReadFile(fsys, "nonexistent/app/config.yaml")
	fmt.Printf("%s%v\n", data, err)
	// Output:
	// region: eu
	// <nil>
}

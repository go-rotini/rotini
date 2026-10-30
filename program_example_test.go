package rotini_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/go-rotini/rotini"
)

type taskrHandler struct{ rotini.NoHooks }

func (taskrHandler) Run(context.Context, *rotini.Context) {}

type addHandler struct{ rotini.NoHooks }

func (addHandler) Run(_ context.Context, rtx *rotini.Context) {
	rtx.RecordWarning(errors.New("the cache is stale"))
	rtx.HaltWith(rotini.UsageError(errors.New("missing title")))
}

func taskrDef() rotini.Definition {
	return rotini.Definition{
		Name: "taskr", Handler: "Taskr",
		Commands: []rotini.CommandDef{{Name: "add", Handler: "TaskrAdd"}},
	}
}

func taskrLookup(name string) (rotini.Handler, bool) {
	switch name {
	case "Taskr":
		return taskrHandler{}, true
	case "TaskrAdd":
		return addHandler{}, true
	}
	return nil, false
}

// A hand-written handler set needs no reflection: the lookup returns each command's handler.
// Run returns the exit code; the default reporter writes the outcome to stderr.
func ExampleNewProgramFunc() {
	code, err := rotini.NewProgramFunc(taskrDef(), taskrLookup).
		WithStderr(os.Stdout).
		Run([]string{"add"})
	fmt.Println(code, rotini.CategoryOf(err))
	// Output:
	// Warning: the cache is stale
	// Error: missing title
	// 1 usage
}

func ExampleStructuredReporter() {
	rotini.NewProgramFunc(taskrDef(), taskrLookup).
		WithStderr(os.Stdout).
		WithExit(func(int) {}).
		WithArgs([]string{"add", "--json"}).
		WithReporter(rotini.StructuredReporter(func(rtx *rotini.Context) bool {
			return slices.Contains(rtx.Argv, "--json")
		})).
		Execute()
	// Output:
	// {"schema_version":1,"warning":{"command":"taskr add","message":"the cache is stale"}}
	// {"schema_version":1,"error":{"category":"usage","command":"taskr add","exit_code":1,"message":"missing title"}}
}

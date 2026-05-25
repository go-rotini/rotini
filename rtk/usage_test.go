package rtk

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-rotini/rotini"
)

func TestUsage_fromContext(t *testing.T) {
	rtx := rotini.NewContextFor(testDef(), []string{"run"})
	s := Usage(rtx)
	if !strings.Contains(s, "Usage:") || !strings.Contains(s, "app run") {
		t.Errorf("Usage(rtx) unexpected:\n%s", s)
	}
	if Usage(rotini.NewContext()) != "" {
		t.Errorf("Usage on an unresolved context should be empty")
	}
}

func TestWriteUsage_rich(t *testing.T) {
	def := rotini.Definition{
		Name:        "app",
		Handler:     "App",
		Summary:     "Do things.",
		Description: "A longer description of app.",
		Flags:       []rotini.FlagDef{{Name: "verbose", Identifiers: []string{"-v", "--verbose"}, Type: "bool", Description: "Be loud."}},
		Commands: []rotini.CommandDef{
			{
				Name: "run", Handler: "AppRun", Aliases: []string{"r"}, Summary: "Run it.",
				Flags:     []rotini.FlagDef{{Name: "count", Identifiers: []string{"--count"}, Type: "int", Description: "How many.", Default: "1"}},
				Arguments: []rotini.ArgDef{{Name: "name", Description: "Who to run."}},
			},
		},
	}

	var buf bytes.Buffer
	writeUsage(&buf, rotini.NewContextFor(def, nil).Chain())
	root := buf.String()
	for _, want := range []string{"Do things.", "Usage:", "A longer description of app.", "Commands:", "run, r", "Run it.", "Be loud."} {
		if !strings.Contains(root, want) {
			t.Errorf("root help missing %q:\n%s", want, root)
		}
	}

	buf.Reset()
	writeUsage(&buf, rotini.NewContextFor(def, []string{"run"}).Chain())
	leaf := buf.String()
	for _, want := range []string{"Run it.", "app run", "Arguments:", "Who to run.", "Flags:", "--count int", "How many.", `(default "1")`} {
		if !strings.Contains(leaf, want) {
			t.Errorf("run help missing %q:\n%s", want, leaf)
		}
	}
}

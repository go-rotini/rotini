package rth

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/rtk"
)

func wizardWith(in string) *rtk.Prompter {
	return rtk.NewPrompter().WithInput(strings.NewReader(in)).WithOutput(&bytes.Buffer{})
}

func TestRunInitWizard_standalone(t *testing.T) {
	// name=mycli, format=2 (json), compose=no
	name, format, into, err := runInitWizard(wizardWith("mycli\n2\nn\n"))
	if err != nil {
		t.Fatalf("wizard: %v", err)
	}
	if name != "mycli" || format != "json" || into != "" {
		t.Errorf("got (%q, %q, %q), want (mycli, json, \"\")", name, format, into)
	}
}

func TestRunInitWizard_composed(t *testing.T) {
	// name=child, format=1 (yaml), compose=yes, parent=root
	name, format, into, err := runInitWizard(wizardWith("child\n1\ny\nroot\n"))
	if err != nil {
		t.Fatalf("wizard: %v", err)
	}
	if name != "child" || format != "yaml" || into != "root" {
		t.Errorf("got (%q, %q, %q), want (child, yaml, root)", name, format, into)
	}
}

func TestRunInitWizard_repromptsEmptyName(t *testing.T) {
	// blank line, then the real name → the name prompt repeats until non-empty.
	name, _, _, err := runInitWizard(wizardWith("\nmycli\n1\nn\n"))
	if err != nil {
		t.Fatalf("wizard: %v", err)
	}
	if name != "mycli" {
		t.Errorf("name = %q, want mycli (after an empty re-prompt)", name)
	}
}

func TestRunInitWizard_eofErrors(t *testing.T) {
	if _, _, _, err := runInitWizard(wizardWith("")); err == nil {
		t.Error("wizard should error on EOF with no input")
	}
}

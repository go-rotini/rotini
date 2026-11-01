package rotini

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-rotini/rotini"
)

// A missing rotini-mcp gets the commands that install it; other plugin failures don't.
func TestPluginInstallHint(t *testing.T) {
	missing := &rotini.PluginError{Name: "mcp", Binary: "rotini-mcp", Kind: rotini.PluginNotFound, Msg: `"rotini-mcp" not found`}
	if hint := pluginInstallHint(missing); !strings.Contains(hint, "go install github.com/go-rotini/mcp/cmd/rotini-mcp@latest") || !strings.Contains(hint, "go get -tool") {
		t.Errorf("hint = %q", hint)
	}
	if hint := pluginInstallHint(errors.Join(errors.New("other"), missing)); hint == "" {
		t.Error("a wrapped plugin error got no hint")
	}
	for name, err := range map[string]error{
		"another plugin": &rotini.PluginError{Binary: "rotini-other", Kind: rotini.PluginNotFound},
		"a failed start": &rotini.PluginError{Binary: "rotini-mcp", Kind: rotini.PluginStartFailed},
		"not a plugin":   errors.New("boom"),
	} {
		if hint := pluginInstallHint(err); hint != "" {
			t.Errorf("%s: hint %q", name, hint)
		}
	}
}

// Report is the default reporter's output followed by the hint.
func TestReport_pluginHint(t *testing.T) {
	var stderr bytes.Buffer
	rtx := rotini.NewContextFor(rotini.Definition{Name: "rotini"}, nil)
	rtx.Stderr = &stderr
	missing := &rotini.PluginError{Name: "mcp", Binary: "rotini-mcp", Kind: rotini.PluginNotFound, Msg: `"rotini-mcp" not found`}
	Report(context.Background(), rtx, rotini.Outcome{Errors: []error{missing}})
	out := stderr.String()
	if !strings.Contains(out, `"rotini-mcp" not found`) || !strings.Contains(out, "go install github.com/go-rotini/mcp") {
		t.Errorf("stderr = %q", out)
	}
}

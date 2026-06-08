package internal

import (
	"strings"
	"testing"
)

func TestCompletionScript(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		script, err := completionScript("myprog", shell)
		if err != nil {
			t.Errorf("%s: %v", shell, err)
			continue
		}
		if !strings.Contains(script, "myprog") || !strings.Contains(script, "__complete") {
			t.Errorf("%s script missing prog/__complete:\n%s", shell, script)
		}
	}
	if _, err := completionScript("p", "nushell"); err == nil {
		t.Error("nushell should be unsupported")
	}
	if _, err := completionScript("p", ""); err == nil {
		t.Error("empty shell should error")
	}
}

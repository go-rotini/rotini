package rotini

import (
	"path/filepath"
	"strings"
	"testing"
)

// The path rows of the input conformance matrix: declared expansion of ~ and $VAR on every
// channel, configuration paths relative to their file, path checks on env and config inputs,
// and an argument's environment fallback read from a file.

func pathConformanceCases() []dataCase {
	return []dataCase{
		{"FLAG-26", func(t *testing.T) { // ~ in a command-line value is expanded to the injected home
			_, home, work, conf := exSetup(t)
			in, err := exBind(t, work, conf, "", []string{"HOME=" + home, "USERPROFILE=" + home}, "--file", "~/f.txt")
			if err != nil || in.App.Flags.File != filepath.Join(home, "f.txt") {
				t.Errorf("--file = %q, %v", in.App.Flags.File, err)
			}
		}},
		{"ENV-12", func(t *testing.T) { // an env existingfile that does not exist is a usage error naming the variable
			_, home, work, conf := exSetup(t)
			_, err := exBind(t, work, conf, "", []string{"HOME=" + home, "APP_LOG=/nonexistent"})
			if err == nil || err.Error() != `APP_LOG: no such file: "/nonexistent"` {
				t.Errorf("err = %v", err)
			}
		}},
		{"ENV-13", func(t *testing.T) { // an unset variable in an expanded env value is a usage error naming it
			_, home, work, conf := exSetup(t)
			_, err := exBind(t, work, conf, "", []string{"HOME=" + home, "APP_HOME=$NOPE/x"})
			if err == nil || !strings.Contains(err.Error(), "$NOPE is not set") {
				t.Errorf("err = %v", err)
			}
		}},
		{"CFG-11", func(t *testing.T) { // a config existingdir list is checked
			_, home, work, conf := exSetup(t)
			_, err := exBind(t, work, conf, "dirs: [missing]\n", []string{"HOME=" + home})
			if err == nil || !strings.Contains(err.Error(), "no such directory") {
				t.Errorf("err = %v", err)
			}
		}},
		{"CFG-12", func(t *testing.T) { // relative_to: config resolves against the file's directory
			root, home, work, conf := exSetup(t)
			in, err := exBind(t, work, conf, "data: cache\n", []string{"HOME=" + home})
			if err != nil || in.App.Config.Data != filepath.Join(root, "etc", "cache") {
				t.Errorf("data = %q, %v", in.App.Config.Data, err)
			}
		}},
		{"ARG-20", func(t *testing.T) { // an argument's fallback read from a variable_file file
			dir := t.TempDir()
			idWrite(t, dir, "target", "prod\n")
			def := Definition{Name: "app", Handler: "App", Arguments: []ArgDef{{Name: "target", Type: "string"}}}
			rtx := NewContextFor(def, nil).WithDir(dir).WithEnviron([]string{"APP_TARGET_FILE=target"})
			var in argFileInputs
			if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil || in.App.Arguments.Target != "prod" {
				t.Errorf("target = %q, %v", in.App.Arguments.Target, err)
			}
		}},
	}
}

func TestConformance_PathMatrix(t *testing.T) {
	for _, c := range pathConformanceCases() {
		t.Run(c.id, c.check)
	}
}

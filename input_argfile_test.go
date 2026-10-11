package rotini

import (
	"errors"
	"strings"
	"testing"
)

type argFileCmd struct {
	Flags     struct{}
	Arguments struct {
		Target string `rotini:"target" recon:"target" env:"APP_TARGET" envfile:"APP_TARGET_FILE"`
	}
}
type argFileInputs struct{ App argFileCmd }

// An argument's environment fallback can be read from the file a variable_file variable names.
func TestVariableFile_argument(t *testing.T) {
	dir := t.TempDir()
	idWrite(t, dir, "target", "prod\n")
	def := Definition{Name: "app", Handler: "App", Arguments: []ArgDef{{Name: "target", Type: "string"}}}
	bind := func(env []string, argv ...string) (argFileInputs, error) {
		rtx := NewContextFor(def, argv).WithDir(dir).WithEnviron(env)
		var in argFileInputs
		err := NewInputReader(InputSettings{}).Read(rtx, &in)
		return in, err
	}
	in, err := bind([]string{"APP_TARGET_FILE=target"})
	if err != nil || in.App.Arguments.Target != "prod" {
		t.Fatalf("from the file: %q, %v", in.App.Arguments.Target, err)
	}
	in, err = bind([]string{"APP_TARGET_FILE=target"}, "dev")
	if err != nil || in.App.Arguments.Target != "dev" {
		t.Errorf("argv wins: %q, %v", in.App.Arguments.Target, err)
	}
	_, err = bind([]string{"APP_TARGET=x", "APP_TARGET_FILE=target"})
	var ie *InputError
	if err == nil || err.Error() != "set APP_TARGET or APP_TARGET_FILE, not both" || !errors.As(err, &ie) || ie.Channel != channelArgument {
		t.Errorf("both set = %v", err)
	}
	if _, err = bind([]string{"APP_TARGET_FILE=missing"}); err == nil || !strings.HasPrefix(err.Error(), "APP_TARGET_FILE: ") {
		t.Errorf("unreadable = %v", err)
	}
}

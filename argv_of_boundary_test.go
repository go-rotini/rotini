package rotini

import (
	"slices"
	"testing"
)

// TestArgvOf_responseFilePrefixOnRawWords pins that a passthrough command's raw words reach
// its handler as written when response files are on: a word starting with the prefix is
// written with it doubled, so the run doesn't read it as a file.
func TestArgvOf_responseFilePrefixOnRawWords(t *testing.T) {
	def := argvDef()
	def.ResponseFiles = &ResponseFilesDef{Prefix: "@"}
	var in argvShInputs
	in.Sh.Arguments.Words = []string{"@alice", "x", "@@b", "@", "--", "@c"}
	argv, _, err := ArgvOf(def, in, PresenceOf(in))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"sh", "@@alice", "x", "@@@b", "@", "--", "@c"}; !slices.Equal(argv, want) {
		t.Fatalf("argv = %q, want %q", argv, want)
	}

	var got []string
	p := NewProgramFunc(def, func(name string) (Handler, bool) {
		return funcHandler{run: func(rtx *Context) {
			if name != "AppSh" {
				return
			}
			v, err := rtx.Inputs[argvShInputs]()
			if err != nil {
				rtx.HaltWith(err)
				return
			}
			got = v.Sh.Arguments.Words
		}}, true
	}).WithDir(t.TempDir())
	if code, err := p.Run(argv); code != 0 || err != nil {
		t.Fatalf("run %q: exit %d: %v", argv, code, err)
	}
	if !slices.Equal(got, in.Sh.Arguments.Words) {
		t.Errorf("the handler got %q, want %q", got, in.Sh.Arguments.Words)
	}

	// Without response files, the words are written as they are.
	if argv, _, _ := ArgvOf(argvDef(), in, PresenceOf(in)); !slices.Equal(argv[1:], in.Sh.Arguments.Words) {
		t.Errorf("argv = %q, want the words unchanged", argv)
	}
}

type bdSSHUserInputs struct {
	App bdRoot
	Ssh struct {
		Flags struct {
			Port int    `rotini:"port"`
			IPv4 bool   `rotini:"ipv4"`
			IPv6 bool   `rotini:"ipv6"`
			User string `rotini:"user" recon:"user" env:"APP_USER"`
		}
		Arguments struct {
			Host string   `rotini:"host"`
			Cmd  []string `rotini:"cmd"`
		}
	}
}

// bdUserDef is boundaryDef with an env-fallback --user on the options_first ssh.
func bdUserDef() Definition {
	def := boundaryDef()
	for i := range def.Commands {
		if def.Commands[i].Name == "ssh" {
			def.Commands[i].Flags = append(def.Commands[i].Flags, FlagDef{Name: "user", Identifiers: []string{"--user", "-l"}, Type: "string"})
		}
	}
	return def
}

// TestOptionsFirst_envFallbackAfterTheBoundary pins that a flag spelled after an options_first
// command's first argument is an argument, so neither ArgvInputs nor the input reader sees it
// set, and its environment fallback applies.
func TestOptionsFirst_envFallbackAfterTheBoundary(t *testing.T) {
	argv := []string{"ssh", "host", "--user", "argv", "-p", "1"}
	rtx := NewContextFor(bdUserDef(), argv).WithEnviron([]string{"APP_USER=from-env"})

	layer, err := rtx.ArgvInputs[bdSSHUserInputs]()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := layer.Set["Ssh.Flags.User"]; ok {
		t.Error("ArgvInputs reports --user set, though it follows the first argument")
	}
	if want := []string{"--user", "argv", "-p", "1"}; !slices.Equal(layer.Values.Ssh.Arguments.Cmd, want) {
		t.Errorf("ArgvInputs cmd = %q, want %q", layer.Values.Ssh.Arguments.Cmd, want)
	}

	var in bdSSHUserInputs
	if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
		t.Fatal(err)
	}
	if in.Ssh.Flags.User != "from-env" || in.Ssh.Flags.Port != 0 {
		t.Errorf("user %q, port %d; want the env value and no port", in.Ssh.Flags.User, in.Ssh.Flags.Port)
	}

	// Before the boundary the flag is set on the command line and wins.
	rtx = NewContextFor(bdUserDef(), []string{"ssh", "--user", "argv", "host"}).WithEnviron([]string{"APP_USER=from-env"})
	in = bdSSHUserInputs{}
	if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
		t.Fatal(err)
	}
	if in.Ssh.Flags.User != "argv" {
		t.Errorf("user %q, want the command line's", in.Ssh.Flags.User)
	}
}

// TestArgvOf_optionsFirst pins ArgvOf on an options_first command: its flags go before the
// "--", so positionals that look like flags, or like "--", read back as arguments.
func TestArgvOf_optionsFirst(t *testing.T) {
	var in bdSSHUserInputs
	in.App.Flags.Verbose = true
	in.Ssh.Flags.Port = 22
	in.Ssh.Flags.User = "ada"
	in.Ssh.Arguments.Host = "-host"
	in.Ssh.Arguments.Cmd = []string{"-v", "--", "--user=x"}
	set := PresenceOf(in)
	argv, env, err := ArgvOf(bdUserDef(), in, set, ArgvPath("ssh"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"--verbose", "ssh", "--port=22", "--user=ada", "--", "-host", "-v", "--", "--user=x"}; !slices.Equal(argv, want) {
		t.Fatalf("argv = %q, want %q", argv, want)
	}
	argvRoundTrip(t, bdUserDef(), in, set, argv, env)
}

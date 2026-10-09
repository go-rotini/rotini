package rotini

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// boundaryDef is a CLI whose commands stop flags in each of the ways a command can: a
// passthrough argument (exec, on), options_first (ssh), and the "--" every command honours.
// ssh also declares digit options, and cp has a variadic before a fixed argument.
func boundaryDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "help", Identifiers: []string{"--help", "-h"}, Type: "bool", ShortCircuit: true},
			{Name: "verbose", Identifiers: []string{"--verbose", "-v"}, Type: "bool"},
		},
		Commands: []CommandDef{
			{Name: "exec", Handler: "AppExec",
				Flags: []FlagDef{
					{Name: "region", Identifiers: []string{"--region", "--zone"}, Type: "string", DeprecatedIdentifiers: []string{"--zone"}, Deprecated: "use --region"},
				},
				Arguments: []ArgDef{{Name: "command", Type: "[]string", Variadic: true, Passthrough: true}}},
			{Name: "on", Handler: "AppOn",
				Arguments: []ArgDef{{Name: "host", Type: "string", Required: true}, {Name: "command", Type: "[]string", Variadic: true, Passthrough: true}}},
			{Name: "ssh", Handler: "AppSsh", OptionsFirst: true,
				Flags: []FlagDef{
					{Name: "port", Identifiers: []string{"-p", "--port"}, Type: "int"},
					{Name: "ipv4", Identifiers: []string{"-4", "--ipv4"}, Type: "bool"},
					{Name: "ipv6", Identifiers: []string{"-6", "--ipv6"}, Type: "bool"},
				},
				Arguments: []ArgDef{{Name: "host", Type: "string", Required: true}, {Name: "cmd", Type: "[]string", Variadic: true}}},
			{Name: "run", Handler: "AppRun", OptionsFirst: true,
				Flags:     []FlagDef{{Name: "all", Identifiers: []string{"-a", "--all"}, Type: "bool"}},
				Arguments: []ArgDef{{Name: "src", Type: "string"}, {Name: "dst", Type: "string"}}},
			{Name: "mv", Handler: "AppMv",
				Flags:     []FlagDef{{Name: "all", Identifiers: []string{"-a", "--all"}, Type: "bool"}},
				Arguments: []ArgDef{{Name: "src", Type: "string"}, {Name: "dst", Type: "string"}}},
			{Name: "cp", Handler: "AppCp",
				Arguments: []ArgDef{
					{Name: "src", Type: "[]string", Variadic: true, Required: true},
					{Name: "dst", Type: "string", Required: true},
				}},
		},
	}
}

type bdRoot struct {
	Flags struct {
		Help    bool `rotini:"help"`
		Verbose bool `rotini:"verbose"`
	}
	Arguments struct{}
}

type bdExecInputs struct {
	App  bdRoot
	Exec struct {
		Flags struct {
			Region string `rotini:"region" recon:"region" env:"APP_REGION"`
		}
		Arguments struct {
			Command []string `rotini:"command"`
		}
	}
}

type bdOnInputs struct {
	App bdRoot
	On  struct {
		Flags     struct{}
		Arguments struct {
			Host    string   `rotini:"host"`
			Command []string `rotini:"command"`
		}
	}
}

type bdSSHInputs struct {
	App bdRoot
	Ssh struct {
		Flags struct {
			Port int  `rotini:"port"`
			IPv4 bool `rotini:"ipv4"`
			IPv6 bool `rotini:"ipv6"`
		}
		Arguments struct {
			Host string   `rotini:"host"`
			Cmd  []string `rotini:"cmd"`
		}
	}
}

type bdCpInputs struct {
	App bdRoot
	Cp  struct {
		Flags     struct{}
		Arguments struct {
			Src []string `rotini:"src"`
			Dst string   `rotini:"dst"`
		}
	}
}

// bdContext resolves argv against boundaryDef the way a run does, so the context carries the
// chain DefaultResolver picks.
func bdContext(t *testing.T, argv ...string) *Context {
	t.Helper()
	return NewContextFor(boundaryDef(), argv)
}

func bdParse[T any](t *testing.T, argv ...string) (T, error) {
	t.Helper()
	var in T
	err := NewParser().Parse(bdContext(t, argv...), &in)
	return in, err
}

// TestWalkers_agreeOnWhereFlagsStop runs the same command lines through every argv walker:
// the parser, the resolver, short-circuit detection, the completion walk and its offers, the
// deprecation parse, the env-fallback check, DashIndex and the strict pre-plugin parse. Each
// must stop flags at the same word.
func TestWalkers_agreeOnWhereFlagsStop(t *testing.T) {
	for _, c := range []struct {
		name      string
		argv      []string
		chain     []string // resolveChain and walkContext
		raw       []string // the leaf's positionals, as the parser records them
		short     bool     // a short circuit is set
		operands  bool     // the completion walk has stopped offering flags
		dash      int      // DashIndex; -1 for none
		deprecate []string // identifiers Deprecations reports
	}{
		{name: "passthrough argument",
			argv: []string{"exec", "--region", "r", "ls", "-la", "--help", "--", "x", "--zone", "z"}, chain: []string{"app", "exec"},
			raw: []string{"ls", "-la", "--help", "--", "x", "--zone", "z"}, operands: true, dash: -1},
		{name: "flags before the boundary",
			argv: []string{"--verbose", "exec", "--zone", "eu", "ls"}, chain: []string{"app", "exec"},
			raw: []string{"ls"}, operands: true, dash: -1, deprecate: []string{"--zone"}},
		{name: "dash before the boundary",
			argv: []string{"exec", "--", "-x", "--help"}, chain: []string{"app", "exec"},
			raw: []string{"-x", "--help"}, dash: 0},
		{name: "help before the boundary",
			argv: []string{"exec", "--help", "ls"}, chain: []string{"app", "exec"},
			raw: []string{"ls"}, short: true, operands: true, dash: -1},
		{name: "a leading argument, then a dash",
			argv: []string{"on", "h", "--", "x", "--"}, chain: []string{"app", "on"},
			raw: []string{"h", "x", "--"}, dash: 1},
		{name: "a leading argument",
			argv: []string{"on", "h", "x", "--"}, chain: []string{"app", "on"},
			raw: []string{"h", "x", "--"}, operands: true, dash: -1},
		{name: "a leading argument after a dash",
			argv: []string{"on", "--", "h", "-x"}, chain: []string{"app", "on"},
			raw: []string{"h", "-x"}, dash: 0},
		{name: "options first",
			argv: []string{"ssh", "-p", "22", "host", "-v", "ls", "--help", "--", "x"}, chain: []string{"app", "ssh"},
			raw: []string{"host", "-v", "ls", "--help", "--", "x"}, operands: true, dash: -1},
		{name: "options first after a dash",
			argv: []string{"ssh", "--", "-host", "x"}, chain: []string{"app", "ssh"},
			raw: []string{"-host", "x"}, dash: 0},
		{name: "digit options",
			argv: []string{"ssh", "-46", "-4", "host", "-6"}, chain: []string{"app", "ssh"},
			raw: []string{"host", "-6"}, operands: true, dash: -1},
		{name: "a sibling without options first permutes",
			argv: []string{"mv", "a", "-a", "b"}, chain: []string{"app", "mv"},
			raw: []string{"a", "b"}, dash: -1},
	} {
		t.Run(c.name, func(t *testing.T) {
			def := boundaryDef()

			chain, plugin := resolveChain(def, c.argv)
			if got := chainNames(chain); !slices.Equal(got, c.chain) || plugin != nil {
				t.Errorf("resolveChain = %v (plugin %v), want %v", got, plugin, c.chain)
			}

			store, err := parseArgvTokens(chain, c.argv, argvAcq{})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := store.scopes[len(chain)-1].args; !slices.Equal(got, c.raw) {
				t.Errorf("parsed positionals = %q, want %q", got, c.raw)
			}
			if got := shortCircuited(chain, store); got != c.short {
				t.Errorf("shortCircuited = %v, want %v", got, c.short)
			}

			cc := walkContext(def, c.argv)
			if got := chainNames(cc.chain); !slices.Equal(got, c.chain) {
				t.Errorf("walkContext chain = %v, want %v", got, c.chain)
			}
			if cc.shortCircuit != c.short || cc.operands != c.operands {
				t.Errorf("walkContext shortCircuit=%v operands=%v, want %v and %v", cc.shortCircuit, cc.operands, c.short, c.operands)
			}
			flags := complete(def, append(slices.Clone(c.argv), "-"), nil, nil)
			if want := !c.operands && !c.short && c.dash < 0; (len(flags) > 0) != want {
				t.Errorf("completing a flag after %q offered %q; want flags offered = %v", c.argv, flags, want)
			}

			rtx := bdContext(t, c.argv...)
			idx, ok := rtx.DashIndex()
			if want := c.dash >= 0; ok != want || (ok && idx != c.dash) {
				t.Errorf("DashIndex = %d, %v; want %d", idx, ok, c.dash)
			}

			var got []string
			for _, d := range Deprecations(rtx) {
				got = append(got, d.Identifier)
			}
			if !slices.Equal(got, c.deprecate) {
				t.Errorf("Deprecations = %q, want %q", got, c.deprecate)
			}
		})
	}
}

// TestPassthroughArgument_envFallbackAmongRawWords pins that a flag spelled among the raw words
// is not set on the command line, so its environment fallback still applies.
func TestPassthroughArgument_envFallbackAmongRawWords(t *testing.T) {
	rtx := bdContext(t, "exec", "ls", "--region", "argv").WithEnviron([]string{"APP_REGION=from-env"})
	var in bdExecInputs
	if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
		t.Fatal(err)
	}
	if in.Exec.Flags.Region != "from-env" || !slices.Equal(in.Exec.Arguments.Command, []string{"ls", "--region", "argv"}) {
		t.Errorf("region %q, command %q; want the env value and the raw words", in.Exec.Flags.Region, in.Exec.Arguments.Command)
	}
}

// TestPassthroughArgument_rawWordsAreNotSplit pins that the boundary counts words: a raw word
// is kept whole even where a separator would split a value.
func TestPassthroughArgument_rawWordsAreNotSplit(t *testing.T) {
	def := boundaryDef()
	def.Commands[0].Arguments[0].Separator = ","
	var in bdExecInputs
	if err := NewParser().Parse(NewContextFor(def, []string{"exec", "a,b", "c"}), &in); err != nil {
		t.Fatal(err)
	}
	if want := []string{"a,b", "c"}; !slices.Equal(in.Exec.Arguments.Command, want) {
		t.Errorf("command = %q, want %q", in.Exec.Arguments.Command, want)
	}
}

func TestPassthroughArgument_bindsLeadingArgument(t *testing.T) {
	in, err := bdParse[bdOnInputs](t, "on", "web", "ls", "-la")
	if err != nil {
		t.Fatal(err)
	}
	if in.On.Arguments.Host != "web" || !slices.Equal(in.On.Arguments.Command, []string{"ls", "-la"}) {
		t.Errorf("host %q command %q", in.On.Arguments.Host, in.On.Arguments.Command)
	}
}

func TestOptionsFirst(t *testing.T) {
	in, err := bdParse[bdSSHInputs](t, "ssh", "-p", "22", "host", "-v", "ls")
	if err != nil {
		t.Fatal(err)
	}
	if in.Ssh.Flags.Port != 22 || in.Ssh.Arguments.Host != "host" || !slices.Equal(in.Ssh.Arguments.Cmd, []string{"-v", "ls"}) || in.App.Flags.Verbose {
		t.Errorf("got %+v", in.Ssh)
	}

	if _, err := bdParse[bdSSHInputs](t, "ssh", "-p"); err == nil || !strings.Contains(err.Error(), `flag "-p" needs a value`) {
		t.Errorf("-p without a value: err = %v", err)
	}

	// A flag after the first argument of a non-variadic leaf is an extra argument, with a hint.
	var run struct {
		App bdRoot
		Run struct {
			Flags struct {
				All bool `rotini:"all"`
			}
			Arguments struct {
				Src string `rotini:"src"`
				Dst string `rotini:"dst"`
			}
		}
	}
	err = NewParser().Parse(bdContext(t, "run", "s", "d", "-a"), &run)
	want := `"run" accepts at most 2 arguments (got 3); flags go before the first argument of "run" (it takes options first): -a`
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %s", err, want)
	}
}

// TestOptionsFirst_notInherited pins that a group's options_first does not reach its
// sub-commands, which parse flags anywhere.
func TestOptionsFirst_notInherited(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", OptionsFirst: true,
		Arguments: []ArgDef{{Name: "x", Type: "[]string", Variadic: true}},
		Commands: []CommandDef{{Name: "sub", Handler: "AppSub",
			Flags:     []FlagDef{{Name: "all", Identifiers: []string{"-a"}, Type: "bool"}},
			Arguments: []ArgDef{{Name: "x", Type: "[]string", Variadic: true}}}},
	}
	chain, _ := resolveChain(def, []string{"sub", "p", "-a"})
	store, err := parseArgvTokens(chain, []string{"sub", "p", "-a"}, argvAcq{})
	if err != nil || !store.setOnArgv(1, "all") {
		t.Errorf("sub p -a: err %v, -a set = %v; want the flag parsed", err, store != nil && store.setOnArgv(1, "all"))
	}
}

func TestDigitOptions(t *testing.T) {
	in, err := bdParse[bdSSHInputs](t, "ssh", "-46", "host")
	if err != nil || !in.Ssh.Flags.IPv4 || !in.Ssh.Flags.IPv6 {
		t.Errorf("-46: %+v, %v", in.Ssh.Flags, err)
	}
	// A value-taking flag takes the next word whatever it looks like.
	in, err = bdParse[bdSSHInputs](t, "ssh", "-p", "-4", "host")
	if err != nil || in.Ssh.Flags.Port != -4 || in.Ssh.Flags.IPv4 {
		t.Errorf("-p -4: %+v, %v; want -4 taken as -p's value", in.Ssh.Flags, err)
	}
	// Without a declared -4, -4 is a negative number.
	def := Definition{Name: "app", Handler: "App", Arguments: []ArgDef{{Name: "n", Type: "int"}}}
	var calc struct {
		App struct {
			Flags     struct{}
			Arguments struct {
				N int `rotini:"n"`
			}
		}
	}
	if err := NewParser().Parse(NewContextFor(def, []string{"-4"}), &calc); err != nil || calc.App.Arguments.N != -4 {
		t.Errorf("-4 undeclared: n = %d, err %v", calc.App.Arguments.N, err)
	}
}

func TestIsFlag_digitOptions(t *testing.T) {
	chain := []Command{rootFrame(boundaryDef())}
	ssh := append(slices.Clone(chain), cmdFrame(boundaryDef().Commands[2]))
	for _, c := range []struct {
		chain []Command
		tok   string
		want  bool
	}{
		{ssh, "-4", true}, {ssh, "-46", true}, {ssh, "-4v", true}, {ssh, "-5", false}, {ssh, "-.4", false},
		{chain, "-4", false}, // declared only on a command not reached yet
		{ssh, "-5s", false}, {ssh, "-Inf", true},
	} {
		if got := isFlag(c.chain, c.tok); got != c.want {
			t.Errorf("isFlag(%d frames, %q) = %v, want %v", len(c.chain), c.tok, got, c.want)
		}
	}
}

func TestDashIndex(t *testing.T) {
	for _, c := range []struct {
		argv []string
		idx  int
		ok   bool
	}{
		{[]string{"exec", "--", "ls"}, 0, true},
		{[]string{"exec", "ls", "--", "x"}, 0, false},
		{[]string{"on", "a", "--", "b"}, 1, true},
		{[]string{"on", "a", "b", "--", "c"}, 0, false},
		{[]string{"cp", "a", "--", "b"}, 1, true},
		{[]string{"cp", "a", "b"}, 0, false},
		{[]string{"mv", "--unknown"}, 0, false},
	} {
		idx, ok := bdContext(t, c.argv...).DashIndex()
		if idx != c.idx || ok != c.ok {
			t.Errorf("%q: DashIndex = %d, %v; want %d, %v", c.argv, idx, ok, c.idx, c.ok)
		}
	}
	if idx, ok := (*Context)(nil).DashIndex(); idx != 0 || ok {
		t.Error("nil context")
	}
}

func TestVariadicBeforeFixed(t *testing.T) {
	in, err := bdParse[bdCpInputs](t, "cp", "a", "b", "c", "dst")
	if err != nil || !slices.Equal(in.Cp.Arguments.Src, []string{"a", "b", "c"}) || in.Cp.Arguments.Dst != "dst" {
		t.Errorf("cp a b c dst: %+v, %v", in.Cp.Arguments, err)
	}
	_, err = bdParse[bdCpInputs](t, "cp", "dst")
	if err == nil || !strings.Contains(err.Error(), "missing required input: <src>") {
		t.Errorf("cp dst: err = %v, want <src> missing", err)
	}
	_, err = bdParse[bdCpInputs](t, "cp")
	if err == nil || !strings.Contains(err.Error(), "<src>, <dst>") {
		t.Errorf("cp: err = %v, want both missing", err)
	}

	for n, want := range map[int][][2]int{
		0: {{0, 0}, {0, 0}, {0, 0}, {0, 0}},
		1: {{0, 1}, {1, 1}, {1, 1}, {1, 1}},
		2: {{0, 1}, {1, 1}, {1, 1}, {1, 2}},
		3: {{0, 1}, {1, 1}, {1, 2}, {2, 3}},
		5: {{0, 1}, {1, 3}, {3, 4}, {4, 5}},
	} {
		defs := []ArgDef{{Name: "lead"}, {Name: "mid", Variadic: true}, {Name: "t1"}, {Name: "t2"}}
		if got := argSpans(defs, n); !reflect.DeepEqual(got, want) {
			t.Errorf("argSpans(n=%d) = %v, want %v", n, got, want)
		}
	}

	// Completion offers the variadic's values at and after its start.
	def := boundaryDef()
	def.Commands[5].Arguments[0].Enum = []string{"x.txt"}
	if got := complete(def, []string{"cp", "a", "b", ""}, nil, nil); !slices.Equal(got, []string{"x.txt"}) {
		t.Errorf("cp a b <TAB> = %q, want the variadic's enum", got)
	}
}

func TestFromFileEscape(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "to", Identifiers: []string{"--to", "-t"}, Type: "string", From: []string{"file"}},
		{Name: "plain", Identifiers: []string{"--plain"}, Type: "string"},
	}}
	type inputs struct {
		App struct {
			Flags struct {
				To    string `rotini:"to"`
				Plain string `rotini:"plain"`
			}
			Arguments struct{}
		}
	}
	for argv, want := range map[string]string{
		"--to @@alice": "@alice", "--to=@@alice": "@alice", "-t@@alice": "@alice",
		"--to @@": "@", "--to @@@x": "@@x", "--to @f": "from-file",
	} {
		var in inputs
		rtx := NewContextFor(def, strings.Fields(argv)).WithDir(dir)
		if err := NewParser().Parse(rtx, &in); err != nil || in.App.Flags.To != want {
			t.Errorf("%s: to = %q, err %v; want %q", argv, in.App.Flags.To, err, want)
		}
	}
	var in inputs
	if err := NewParser().Parse(NewContextFor(def, []string{"--plain", "@@x"}), &in); err != nil || in.App.Flags.Plain != "@@x" {
		t.Errorf("--plain @@x = %q, %v; want it verbatim", in.App.Flags.Plain, err)
	}
}

// bdHandler records what its Run sees.
type bdHandler struct {
	NoHooks
	argv *[]string
}

func (h bdHandler) Run(_ context.Context, rtx *Context) { *h.argv = slices.Clone(rtx.Argv) }

func bdProgram(def Definition, dir string, out *strings.Builder, seen *[]string) *Program {
	return NewProgramFunc(def, func(string) (Handler, bool) { return bdHandler{argv: seen}, true }).
		WithDir(dir).WithStdout(out).WithStderr(out).WithoutSignalHandling()
}

func TestResponseFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("args.rsp", "\xef\xbb\xbf# comment\r\n\nexec\r\n  --region r  \n   # indented comment\n@nested\n")
	write("stop.rsp", "a\n--\n@args.rsp\n")
	def := boundaryDef()
	def.ResponseFiles = &ResponseFilesDef{Prefix: "@"}

	for _, c := range []struct {
		argv []string
		want []string
	}{
		{[]string{"@args.rsp", "ls"}, []string{"exec", "  --region r  ", "@nested", "ls"}},
		{[]string{"exec", "@@x"}, []string{"exec", "@x"}},
		{[]string{"exec", "--", "@args.rsp"}, []string{"exec", "--", "@args.rsp"}},
		{[]string{"exec", "@stop.rsp", "@args.rsp"}, []string{"exec", "a", "--", "@args.rsp", "@args.rsp"}},
		{[]string{"exec", "@"}, []string{"exec", "@"}},
	} {
		got, err := expandResponseFiles(c.argv, "@", (&osView{}).withDir(dir), false)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("expand %q = %q, %v; want %q", c.argv, got, err, c.want)
		}
	}

	if err := os.Mkdir(filepath.Join(dir, "adir"), 0o700); err != nil {
		t.Fatal(err)
	}
	for word, msg := range map[string]string{
		"@missing": `could not read response file "missing": no such file`,
		"@adir":    `could not read response file "adir": not a file`,
	} {
		_, err := expandResponseFiles([]string{word}, "@", (&osView{}).withDir(dir), false)
		if pe, ok := errors.AsType[*ParseError](err); !ok || pe.Msg != msg || CategoryOf(err) != CategoryUsage {
			t.Errorf("%s: err = %v, want %q", word, err, msg)
		}
	}
	write("big.rsp", strings.Repeat("x", responseFileCap+1))
	if _, err := expandResponseFiles([]string{"@big.rsp"}, "@", (&osView{}).withDir(dir), false); err == nil || !strings.Contains(err.Error(), "larger than 1 MiB") {
		t.Errorf("big: err = %v", err)
	}

	// A run expands before resolving, so a file can name the command, and rtx.Argv holds the
	// expanded words.
	var out strings.Builder
	var seen []string
	if code, err := bdProgram(def, dir, &out, &seen).Run([]string{"@args.rsp", "ls"}); code != 0 {
		t.Fatalf("Run = %d, %v: %s", code, err, out.String())
	}
	if want := []string{"exec", "  --region r  ", "@nested", "ls"}; !slices.Equal(seen, want) {
		t.Errorf("handler saw %q, want %q", seen, want)
	}
	out.Reset()
	if code, _ := bdProgram(def, dir, &out, &seen).Run([]string{"--help", "@missing"}); code != 1 || !strings.Contains(out.String(), "could not read response file") {
		t.Errorf("unreadable file with --help: code %d, output %q", code, out.String())
	}

	// Completion walks the expanded words, and leaves a file name being typed to the shell.
	out.Reset()
	if _, err := bdProgram(def, dir, &out, &seen).Complete([]string{"@args.rsp", "-"}, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "--region") {
		t.Errorf("completing after the expanded raw words offered flags: %q", out.String())
	}
	out.Reset()
	if _, err := bdProgram(def, dir, &out, &seen).Complete([]string{"@ar"}, nil); err != nil || out.String() != "" {
		t.Errorf("completing @ar = %q, %v; want nothing", out.String(), err)
	}
}

func TestResponseFileWords(t *testing.T) {
	got := responseFileWords("a\r\n\n \t\n#c\n  # d\nb c\n")
	if want := []string{"a", "b c"}; !slices.Equal(got, want) {
		t.Errorf("words = %q, want %q", got, want)
	}
}

func FuzzResponseFileWords(f *testing.F) {
	f.Add("a\nb\n")
	f.Add("\xef\xbb\xbf# x\r\n y \n")
	f.Fuzz(func(t *testing.T, text string) {
		words := responseFileWords(text)
		var kept []string
		for _, w := range words {
			if strings.ContainsAny(w, "\n") || strings.TrimSpace(w) == "" {
				t.Fatalf("word %q holds a line break or is blank", w)
			}
			if strings.HasSuffix(w, "\r") {
				return // written back, a trailing carriage return reads as a line ending
			}
			kept = append(kept, w)
		}
		if again := responseFileWords(strings.Join(kept, "\n")); !slices.Equal(again, words) {
			t.Fatalf("round trip %q → %q", words, again)
		}
	})
}

// TestWalkers_argvInputsAndPluginHost covers the two walkers that read argv for their own
// purpose: the root help check (ArgvInputs), which must not see a --help typed after the
// boundary, and the strict parse of the words before a plugin's name, which must read a
// declared digit option as a flag.
func TestWalkers_argvInputsAndPluginHost(t *testing.T) {
	layer, err := bdContext(t, "exec", "ls", "--help").ArgvInputs[bdExecInputs]()
	if err != nil || layer.Values.App.Flags.Help {
		t.Errorf("ArgvInputs(exec ls --help): help = %v, err %v; want --help left to the raw words", layer.Values.App.Flags.Help, err)
	}
	layer, err = bdContext(t, "exec", "--help", "ls").ArgvInputs[bdExecInputs]()
	if err != nil || !layer.Values.App.Flags.Help {
		t.Errorf("ArgvInputs(exec --help ls): help = %v, err %v; want it set", layer.Values.App.Flags.Help, err)
	}

	def := Definition{Name: "app", Handler: "App",
		Flags:   []FlagDef{{Name: "four", Identifiers: []string{"-4"}, Type: "bool"}},
		Plugins: []PluginDef{{Name: "sig", Binary: "app-sig"}},
	}
	if _, err := DefaultResolver(def, []string{"-4", "sig"}); err == nil || !strings.Contains(err.Error(), "-4 can't come before plugin") {
		t.Errorf("-4 sig: err = %v, want the misplaced-flag error", err)
	}
	if res, err := DefaultResolver(def, []string{"-5", "sig"}); err != nil || res.Plugin != nil {
		t.Errorf("-5 sig: plugin %v, err %v; want -5 read as the first argument", res.Plugin, err)
	}
}

// TestComplete_pastTheBoundary pins what completion offers once flags have stopped: the
// argument's own enum and hint, and no flags or sub-commands.
func TestComplete_pastTheBoundary(t *testing.T) {
	def := boundaryDef()
	def.Commands[2].Arguments[1].Enum = []string{"ls", "top"}
	def.Commands[2].Arguments[1].Complete = Completion{Kind: "none"}
	def.Commands[0].Arguments[0].Complete = Completion{Kind: "file"}

	if got := complete(def, []string{"ssh", "host", ""}, nil, nil); !slices.Equal(got, []string{"ls", "top"}) {
		t.Errorf("ssh host <TAB> = %q, want the argument's enum", got)
	}
	if got := completionHintFor(def, []string{"ssh", "host", "--he"}); got.Kind != "none" {
		t.Errorf("ssh host --he hint = %+v, want the argument's", got)
	}
	if got := completionHintFor(def, []string{"exec", "ls", "-"}); got.Kind != "file" {
		t.Errorf("exec ls - hint = %+v, want the passthrough argument's", got)
	}
	got := complete(def, []string{"ssh", "-"}, nil, nil)
	if !slices.ContainsFunc(got, func(c string) bool { return strings.HasPrefix(c, "-4") }) {
		t.Errorf("ssh -<TAB> = %q, want the digit option offered", got)
	}
}

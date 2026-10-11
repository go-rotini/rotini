package rotini

import (
	"bytes"
	"context"
	"io"
	"slices"
	"strings"
	"testing"
)

func multicallDef(mc *MulticallDef) Definition {
	return Definition{
		Name: "busybox", Handler: "Busybox", Multicall: mc,
		Commands: []CommandDef{
			{Name: "ls", Handler: "Ls", Aliases: []string{"dir"}},
			{Name: "cat", Handler: "Cat", HiddenAliases: []string{"concat"}},
		},
		Plugins: []PluginDef{{Name: "ext", Aliases: []string{"x"}, Binary: "busybox-ext"}},
	}
}

func TestMulticallArgv(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		mc       *MulticallDef
		argv0    string
		goos     string
		want     []string
		complete bool
	}{
		{"off", nil, "ls", "linux", []string{"-l"}, false},
		{"command", &MulticallDef{}, "ls", "linux", []string{"ls", "-l"}, false},
		{"absolute path", &MulticallDef{}, "/usr/bin/ls", "linux", []string{"ls", "-l"}, false},
		{"relative path", &MulticallDef{}, "./cat", "linux", []string{"cat", "-l"}, false},
		{"alias", &MulticallDef{}, "dir", "linux", []string{"dir", "-l"}, false},
		{"plugin", &MulticallDef{}, "x", "linux", []string{"x", "-l"}, false},
		{"root name", &MulticallDef{}, "busybox", "linux", []string{"-l"}, false},
		{"unknown name", &MulticallDef{}, "busybox.test", "linux", []string{"-l"}, false},
		{"case matters off Windows", &MulticallDef{}, "LS", "linux", []string{"-l"}, false},
		{"exe stays off Windows", &MulticallDef{}, "ls.exe", "linux", []string{"-l"}, false},
		{"windows exe", &MulticallDef{}, `C:\bin\ls.exe`, "windows", []string{"ls", "-l"}, false},
		{"windows case", &MulticallDef{}, `C:\bin\LS.EXE`, "windows", []string{"ls", "-l"}, false},
		{"windows slash", &MulticallDef{}, "C:/bin/Cat.Exe", "windows", []string{"cat", "-l"}, false},
		{"hidden alias", &MulticallDef{}, "concat", "linux", []string{"concat", "-l"}, false},
		{"windows hidden alias", &MulticallDef{}, `C:\bin\CONCAT.EXE`, "windows", []string{"concat", "-l"}, false},
		{"prefix", &MulticallDef{Prefix: "bb-"}, "bb-ls", "linux", []string{"ls", "-l"}, false},
		{"prefix missing", &MulticallDef{Prefix: "bb-"}, "ls", "linux", []string{"-l"}, false},
		{"prefix, unknown", &MulticallDef{Prefix: "bb-"}, "bb-rm", "linux", []string{"-l"}, false},
		{"root named with the prefix", &MulticallDef{Prefix: "busy"}, "busybox", "linux", []string{"-l"}, false},
		{"complete", &MulticallDef{Complete: "kubectl_complete-"}, "kubectl_complete-busybox", "linux", []string{"-l"}, true},
		{"complete, a command name", &MulticallDef{Complete: "kubectl_complete-"}, "ls", "linux", []string{"ls", "-l"}, false},
		{"complete and prefix", &MulticallDef{Prefix: "bb-", Complete: "kubectl_complete-"}, "bb-cat", "linux", []string{"cat", "-l"}, false},
		{"no argv0", &MulticallDef{}, "", "linux", []string{"-l"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, complete := multicallArgv(multicallDef(c.mc), c.argv0, []string{"-l"}, c.goos)
			if !slices.Equal(got, c.want) || complete != c.complete {
				t.Errorf("multicallArgv(%q) = %q, %v; want %q, %v", c.argv0, got, complete, c.want, c.complete)
			}
		})
	}
}

// A run invoked under a command's name runs that command, and the routed argv is what
// rtx.Argv holds.
func TestProgram_multicallRoutes(t *testing.T) {
	t.Parallel()
	var ran string
	var argv []string
	fns := map[string]func(context.Context, *Context){
		"Ls":  func(_ context.Context, rtx *Context) { ran, argv = "ls", rtx.Argv },
		"Cat": func(context.Context, *Context) { ran = "cat" },
	}
	p := NewProgramFunc(multicallDef(&MulticallDef{}), fnLookup(fns)).WithoutSignalHandling().
		WithStdout(&bytes.Buffer{}).WithStderr(&bytes.Buffer{}).WithArgv0("/opt/bin/ls")
	if code, err := p.Run([]string{"a"}); code != 0 || err != nil {
		t.Fatalf("Run = %d, %v", code, err)
	}
	if ran != "ls" || !slices.Equal(argv, []string{"ls", "a"}) {
		t.Errorf("ran %q with argv %q, want ls with [ls a]", ran, argv)
	}

	// "" restores os.Args[0], which in a test binary names no command.
	ran = ""
	if code, _ := p.WithArgv0("").Run([]string{"cat"}); code != 0 || ran != "cat" {
		t.Errorf("after WithArgv0(\"\"): code %d, ran %q; want the root routing cat", code, ran)
	}
}

// The complete form answers completion for the root in the plugin hosts' format.
func TestProgram_multicallCompletes(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	p := NewProgramFunc(multicallDef(&MulticallDef{Complete: "kubectl_complete-"}), fnLookup(nil)).
		WithoutSignalHandling().WithStdout(&out).WithArgv0("kubectl_complete-busybox")
	if code, err := p.Run([]string{"c"}); code != 0 || err != nil {
		t.Fatalf("Run = %d, %v", code, err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 2 || lines[0] != "cat" || !strings.HasPrefix(lines[len(lines)-1], ":") {
		t.Errorf("completion output = %q, want cat then a :directive line", out.String())
	}

	// WithCompletion's format wins over the plugin hosts' default.
	out.Reset()
	p.WithCompletion(func(w io.Writer, _ CompletionResult) error {
		_, err := w.Write([]byte("custom\n"))
		return err
	})
	if _, err := p.Run([]string{"c"}); err != nil || out.String() != "custom\n" {
		t.Errorf("with WithCompletion: %q, %v", out.String(), err)
	}
}

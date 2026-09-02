package rotini

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// Parser.Parse fills the generated input struct from argv alone: typed
// coercion, defaults, enum and constraint checks — failing with a
// data-shaped *ParseError.
func ExampleParser_Parse() {
	def := Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{{
			Name: "deploy", Handler: "AppDeploy",
			Arguments: []ArgDef{{Name: "service", Type: "string", Required: true}},
			Flags: []FlagDef{
				{Name: "env", Identifiers: []string{"--env"}, Type: "string", Default: "dev", Enum: []string{"dev", "prod"}},
				{Name: "loud", Identifiers: []string{"--loud", "-l"}, Type: "count"},
			},
		}},
	}
	// The shape codegen emits: one field per command on the resolved path.
	var inputs struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
		}
		Deploy struct {
			Flags struct {
				Env  string `rotini:"env"`
				Loud int    `rotini:"loud"`
			}
			Arguments struct {
				Service string `rotini:"service"`
			}
		}
	}

	rtx := NewContextFor(def, []string{"deploy", "api", "--env", "prod", "-ll"})
	if err := NewParser().Parse(rtx, &inputs); err != nil {
		fmt.Println("usage:", err)
		return
	}
	fmt.Printf("%s → %s (verbosity %d)\n",
		inputs.Deploy.Arguments.Service, inputs.Deploy.Flags.Env, inputs.Deploy.Flags.Loud)
	// Output: api → prod (verbosity 2)
}

// Binder.Bind reconciles every declared channel in one call — here a flag
// satisfied from its environment fallback because argv didn't set it.
func ExampleBinder_Bind() {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			// The recon key gives the flag fallbacks: $SERVER_PORT, then any
			// declared config files, then the default.
			{Name: "port", Identifiers: []string{"--port"}, Type: "int", Default: "8080"},
		},
	}
	var inputs struct {
		App struct {
			Flags struct {
				Port int `rotini:"port" recon:"server.port"`
			}
			Arguments struct{}
		}
	}

	os.Setenv("SERVER_PORT", "9090")
	defer os.Unsetenv("SERVER_PORT")

	rtx := NewContextFor(def, nil) // --port absent from argv
	if err := NewBinder(BindMeta{}).Bind(rtx, &inputs); err != nil {
		fmt.Println("bind:", err)
		return
	}
	fmt.Println("port:", inputs.App.Flags.Port)
	// Output: port: 9090
}

// The registry: anything bound on the Program (or Context) is fetched typed.
// Get reports absence; MustGet panics — and that panic reaches the
// Program.WithFunnel funnel as a *PanicError in its panics slice, teardown already done.
func ExampleMustGet() {
	type apiClient struct{ baseURL string }

	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	rtx.Bind("api", &apiClient{baseURL: "https://api.example"})

	client := MustGet[*apiClient](rtx, "api")
	fmt.Println(client.baseURL)

	if _, ok := Get[*apiClient](rtx, "other"); !ok {
		fmt.Println("nothing bound under \"other\"")
	}
	// Output:
	// https://api.example
	// nothing bound under "other"
}

// The category taxonomy: tag errors at the source, map them to conventional
// exit codes in one switch — typically inside Program.WithFunnel.
func ExampleCategoryOf() {
	classify := func(err error) int {
		switch CategoryOf(err) {
		case CategoryUsage:
			return 1
		case CategoryInternal:
			return 70
		default:
			return 1
		}
	}

	fmt.Println(classify(UsageError(errors.New("unknown flag"))))
	fmt.Println(classify(InternalError(errors.New("wiring mismatch"))))
	fmt.Println(classify(errors.New("untagged")))
	// Output:
	// 1
	// 70
	// 1
}

// ── the unopinionated path ──────────────────────────────────.

// unopinionatedCmd overrides only Run; the four embeddable no-op Default* hooks satisfy
// the rest of [Handlers]. Run reads the raw argv from [Context.Args], consults the
// resolved frame's declared flags via [Context.Chain] (spec-aware without a parser), reads
// an env var with the standard library (env is NOT runtime-mediated — only the streams
// are), writes through [Context.Stdout] so the program's streams stay injectable, and
// reports its outcome by recording + [Context.SignalExit] rather than printing inline.
// (Stdin would likewise be read via [Context.Stdin], never os.Stdin.)
type unopinionatedCmd struct {
	DefaultCascadingPreRun
	DefaultPreRun
	DefaultPostRun
	DefaultCascadingPostRun
}

func (unopinionatedCmd) Run(_ context.Context, rtx *Context) {
	leaf := rtx.Chain()[len(rtx.Chain())-1] // the resolved command frame

	// Hand-rolled argv scan — no Parser. The declared flag's identifiers come from the
	// resolved frame, so the scan stays spec-aware without importing the input helpers.
	var ids []string
	for _, f := range leaf.Flags {
		if f.Name == "name" {
			ids = f.Identifiers
		}
	}
	name := ""
	for i := 0; i+1 < len(rtx.Args); i++ {
		for _, id := range ids {
			if rtx.Args[i] == id {
				name = rtx.Args[i+1]
			}
		}
	}
	if name == "" {
		// Record the outcome; the runtime reports it through the funnels after teardown,
		// and the default OnError floors the exit to 1.
		rtx.RecordError(UsageError(errors.New("--name is required")))
		rtx.SignalExit(1)
		return
	}

	greeting := os.Getenv("GREETING") // env via stdlib, not a rotini helper
	if greeting == "" {
		greeting = "hello"
	}
	fmt.Fprintf(rtx.Stdout, "%s, %s! (command %q)\n", greeting, name, leaf.Name)
}

// unopinionatedApp is the aggregate handler set NewProgram resolves "Main" against.
type unopinionatedApp struct{}

func (unopinionatedApp) Main() Handlers { return unopinionatedCmd{} }

// Example_unopinionated drives the bare program end-to-end through the real Program
// surface — WithArgs feeds argv, WithExit captures the code without os.Exit, and the
// handler's [Context.Stdout] is the example's output. No opt-in input helper is imported;
// WithoutSignalHandling keeps the program minimal (rotini still owns the context, just
// installs no signal trap).
func Example_unopinionated() {
	def := Definition{
		Name: "greet", Handler: "Main",
		Flags: []FlagDef{{Name: "name", Identifiers: []string{"--name"}, Type: "string"}},
	}

	os.Setenv("GREETING", "hi")
	defer os.Unsetenv("GREETING")

	code := -1
	NewProgram(def, unopinionatedApp{}).
		WithArgs([]string{"--name", "ada"}).
		WithStdout(os.Stdout).
		WithoutSignalHandling().
		WithExit(func(c int) { code = c }).
		Execute()

	fmt.Println("exit:", code)
	// Output:
	// hi, ada! (command "greet")
	// exit: 0
}

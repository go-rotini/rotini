package rotini

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// Parser.Parse fills the generated input struct from argv alone, with typed coercion,
// defaults, and enum and constraint checks, failing with a *ParseError.
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

// InputReader.Read reconciles every declared channel in one call; here a flag unset in argv
// is read from its environment fallback.
func ExampleInputReader_Read() {
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
	if err := NewInputReader(InputSettings{}).Read(rtx, &inputs); err != nil {
		fmt.Println("bind:", err)
		return
	}
	fmt.Println("port:", inputs.App.Flags.Port)
	// Output: port: 9090
}

// A typed handle names a dependency once, and every read is typed. GetDependency reports
// absence; MustGetDependency panics, and in a hook that panic reaches the reporter as a
// *PanicError after teardown.
func ExampleContext_MustGetDependency() {
	type apiClient struct{ baseURL string }
	api := NewDependency[*apiClient]("api")

	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	rtx.SetDependency(api, &apiClient{baseURL: "https://api.example"})

	client := rtx.MustGetDependency(api)
	fmt.Println(client.baseURL)

	if _, ok := rtx.GetDependency(NewDependency[*apiClient]("other")); !ok {
		fmt.Println("nothing registered as \"other\"")
	}
	// Output:
	// https://api.example
	// nothing registered as "other"
}

// Errors are tagged with a category at the source and mapped to exit codes in one switch,
// typically inside a reporter. Here usage errors map to 2.
func ExampleCategoryOf() {
	classify := func(err error) int {
		switch CategoryOf(err) {
		case CategoryUsage:
			return 2
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
	// 2
	// 70
	// 1
}

// ── the unopinionated path ──────────────────────────────────.

// unopinionatedCmd defines only Run; the embedded [NoHooks] supplies the other hooks. Run
// scans the raw [Context.Argv] for a flag declared on the resolved command
// ([Context.CommandChain]), reads an environment variable with the standard library, writes
// through [Context.Stdout], and fails with [Context.HaltWith].
type unopinionatedCmd struct{ NoHooks }

func (unopinionatedCmd) Run(_ context.Context, rtx *Context) {
	leaf := rtx.CommandChain()[len(rtx.CommandChain())-1] // the resolved command frame

	// Scan argv without a Parser, using the identifiers the resolved command declares.
	var ids []string
	for _, f := range leaf.Flags {
		if f.Name == "name" {
			ids = f.Identifiers
		}
	}
	name := ""
	for i := 0; i+1 < len(rtx.Argv); i++ {
		for _, id := range ids {
			if rtx.Argv[i] == id {
				name = rtx.Argv[i+1]
			}
		}
	}
	if name == "" {
		// Reported after teardown; the default reporter exits 1.
		rtx.HaltWith(UsageError(errors.New("--name is required")))
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

func (unopinionatedApp) Main() Handler { return unopinionatedCmd{} }

// Example_unopinionated runs a program without the input helpers: WithArgs supplies argv,
// WithExit captures the code, and WithoutSignalHandling installs no signal trap.
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

// ── branching on an error in a handler ──────────────────────.

// branchingAdd is the guide's "Handling errors in a handler" sample: a missing required input
// gets a usage hint and exit 2, any other usage error exit 2 through the reporter, and
// everything else is left to the reporter's default.
type branchingAdd struct{ NoHooks }

func (branchingAdd) Run(_ context.Context, rtx *Context) {
	var in struct {
		Todo struct {
			Flags     struct{}
			Arguments struct{}
		}
		TodoAdd struct {
			Flags     struct{}
			Arguments struct {
				Title string `rotini:"title"`
			}
		}
	}
	if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
		var pe *ParseError
		switch {
		case errors.As(err, &pe) && pe.Kind == ParseKindMissingRequired:
			fmt.Fprintf(rtx.Stdout, "%s\nRun '%s --help' for usage.\n", err, rtx.CommandPath())
			rtx.HaltWithCode(2)
		case CategoryOf(err) == CategoryUsage:
			rtx.RecordError(err)
			rtx.HaltWithCode(2)
		default:
			rtx.HaltWith(err)
		}
		return
	}
	fmt.Fprintf(rtx.Stdout, "added %q\n", in.TodoAdd.Arguments.Title)
}

type branchingRoot struct{ NoHooks }

func (branchingRoot) Run(context.Context, *Context) {}

type branchingApp struct{}

func (branchingApp) Todo() Handler    { return branchingRoot{} }
func (branchingApp) TodoAdd() Handler { return branchingAdd{} }

// Example_branchingOnErrors branches on the typed error a missing input produces: the handler
// chooses the message and the exit code, and rotini's reporter is left with nothing to add.
func Example_branchingOnErrors() {
	def := Definition{
		Name: "todo", Handler: "Todo",
		Commands: []CommandDef{{
			Name: "add", Handler: "TodoAdd",
			Arguments: []ArgDef{{Name: "title", Type: "string", Required: true}},
		}},
	}
	for _, argv := range [][]string{{"add", "buy milk"}, {"add"}} {
		code, _ := NewProgram(def, branchingApp{}).
			WithStdout(os.Stdout).
			WithoutSignalHandling().
			Run(argv)
		fmt.Println("exit:", code)
	}
	// Output:
	// added "buy milk"
	// exit: 0
	// missing required input: <title>
	// Run 'todo add --help' for usage.
	// exit: 2
}

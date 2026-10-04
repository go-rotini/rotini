package rotini

import (
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

// The handlers share one way of answering --help, announcing their inputs, and reporting what a
// pass found, so sibling commands never disagree on the shape of their output.

// answerHelp prints the command's help page and stops the run when argv asks for it. It reads
// argv alone — no defaults and no required or enum checks — so `--help` is answered even
// beside a missing argument or a bad value: the user asked NOT to run the command, and should
// not be told off for not supplying what running it would need.
func answerHelp[T any](rtx *rotini.Context, help func(T) bool) bool {
	argv, err := rtx.ArgvInputs[T]()
	if err != nil || !help(argv.Values) {
		return false
	}
	fmt.Fprintln(rtx.Stdout, rtx.Help())
	rtx.HaltWithCode(0)
	return true
}

// suggestor ranks a mistyped command, flag or value against what the companion accepts.
// rotini the framework suggests nothing on its own; this program opts in, the same way any
// program built with rotini can.
var suggestor = rotini.NewSuggestor()

// haltWithInputError stops the run on an input error, naming the nearest accepted spelling when
// one is close: `unknown command "genrate" for "rotini"; did you mean "generate"?`. The hint
// reads like the ones `rotini validate` gives for a spec.
func haltWithInputError(rtx *rotini.Context, err error) {
	rtx.HaltWith(withSuggestion(err, suggestor.For(err)))
}

// withSuggestion appends the nearest of hits to err's message, keeping err itself reachable so
// its category and [*rotini.ParseError] details survive. No hits leaves err as it is.
func withSuggestion(err error, hits []string) error {
	if len(hits) == 0 {
		return err
	}
	return &suggestedError{err: err, hint: hits[0]}
}

type suggestedError struct {
	err  error
	hint string
}

func (e *suggestedError) Error() string { return fmt.Sprintf("%s; did you mean %q?", e.err, e.hint) }
func (e *suggestedError) Unwrap() error { return e.err }

// commandNames lists every name and alias the companion's sub-commands answer to, for ranking
// a `rotini help <topic>` that names no command.
func commandNames() []string {
	var names []string
	for _, c := range definition.Commands {
		if c.Hidden {
			continue
		}
		names = append(names, c.Name)
		names = append(names, c.Aliases...)
	}
	return names
}

// resolveInputs resolves the spec and conf a generate or validate reads, and prints them, so a
// failing run says which files it read. A conf that was not given and is not beside the spec is
// reported as such: the conf defaults apply.
func resolveInputs(rtx *rotini.Context, specPath, confPath string) (spec, conf string, ok bool) {
	spec, conf, err := codegen.ResolvePaths(specPath, confPath)
	if err != nil {
		rtx.HaltWith(rotini.UsageError(err))
		return "", "", false
	}
	fmt.Fprintf(rtx.Stdout, "spec: %s\n", spec)
	if conf == "" {
		fmt.Fprintln(rtx.Stdout, "conf: none (defaults)")
	} else {
		fmt.Fprintf(rtx.Stdout, "conf: %s\n", conf)
	}
	return spec, conf, true
}

// printResult is a pass's result callback: the timing line on success, each problem on
// failure. In watch mode it is the only report a pass gets, so it prints as the pass ends.
func printResult(rtx *rotini.Context) func(string, error) {
	return func(result string, err error) {
		if err != nil {
			for _, problem := range flatten(err) {
				fmt.Fprintln(rtx.Stderr, "Error:", problem)
			}
			return
		}
		fmt.Fprintln(rtx.Stdout, result)
	}
}

// printWarnings is a pass's warnings callback. Warnings print as the pass reports them, not
// after the run: in watch mode the run ends only at ctrl-c, so a warning recorded for the end
// would arrive late, once per pass.
func printWarnings(rtx *rotini.Context) func([]error) {
	return func(warnings []error) {
		for _, w := range warnings {
			fmt.Fprintln(rtx.Stderr, "Warning:", w)
		}
	}
}

// haltWithProblems records each problem in err as its own outcome and stops the run. The
// pipeline joins its findings into one error, and a reporter printing "Error: %s" would mark
// only the first line of it, so `grep '^Error:'` would find one problem in three.
func haltWithProblems(rtx *rotini.Context, err error) {
	problems := flatten(err)
	for _, problem := range problems[:len(problems)-1] {
		rtx.RecordError(problem)
	}
	rtx.HaltWith(problems[len(problems)-1])
}

// flatten expands an errors.Join tree into its leaves, so each problem is recorded as its own
// outcome. A non-joined error is its own only leaf.
func flatten(err error) []error {
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return []error{err}
	}
	var out []error
	for _, e := range joined.Unwrap() {
		out = append(out, flatten(e)...)
	}
	if len(out) == 0 {
		return []error{err}
	}
	return out
}

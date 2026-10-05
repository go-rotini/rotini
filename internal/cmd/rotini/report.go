package rotini

import (
	"fmt"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

// Shared helpers for answering --help, announcing resolved inputs and reporting pass results,
// so every command's output has the same shape.

// answerHelp prints the command's help page and halts with code 0 when argv sets the help flag.
// It reads argv alone, without defaults or validation, so --help works beside a missing
// argument or an invalid value.
func answerHelp[T any](rtx *rotini.Context, help func(T) bool) bool {
	argv, err := rtx.ArgvInputs[T]()
	if err != nil || !help(argv.Values) {
		return false
	}
	fmt.Fprintln(rtx.Stdout, rtx.Help())
	rtx.HaltWithCode(0)
	return true
}

// suggestor ranks a mistyped command, flag or value against what the companion cli accepts.
// The framework suggests nothing by default; this program opts in.
var suggestor = rotini.NewSuggestor()

// haltWithInputError halts on an input error, naming the nearest accepted spelling when one is
// close: `unknown command "genrate" for "rotini"; did you mean "generate"?`.
func haltWithInputError(rtx *rotini.Context, err error) {
	rtx.HaltWith(withSuggestion(err, suggestor.For(err)))
}

// withSuggestion appends the first of hits to err's message, keeping err unwrappable so its
// category and [*rotini.ParseError] details survive. With no hits it returns err unchanged.
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

// commandNames lists every name and alias of the visible sub-commands, for suggesting a match
// when `rotini help <topic>` names no command.
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

// resolveInputs resolves and prints the spec and conf paths a generate or validate reads. When
// no conf is found it prints "conf: none (defaults)". A resolution failure halts with a usage
// error.
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

// printResult returns a pass's result callback: it prints the result line to stdout on success
// and each problem to stderr on failure. In watch mode it is the only report a pass gets.
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

// printWarnings returns a pass's warnings callback. Warnings print immediately rather than
// being recorded for the end of the run, which in watch mode only comes at interrupt.
func printWarnings(rtx *rotini.Context) func([]error) {
	return func(warnings []error) {
		for _, w := range warnings {
			fmt.Fprintln(rtx.Stderr, "Warning:", w)
		}
	}
}

// haltWithProblems records each leaf of a joined err as its own error and halts with the last,
// so the reporter prints one "Error:" line per problem.
func haltWithProblems(rtx *rotini.Context, err error) {
	problems := flatten(err)
	for _, problem := range problems[:len(problems)-1] {
		rtx.RecordError(problem)
	}
	rtx.HaltWith(problems[len(problems)-1])
}

// flatten expands an errors.Join tree into its leaves. A non-joined error is its only leaf.
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

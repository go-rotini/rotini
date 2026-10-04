package rotini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// StructuredReporter returns a [Reporter] for programs whose output scripts read. When
// structured reports true for the run, it writes each thing the run recorded to stderr as one
// JSON object per line, and leaves stdout alone, so a partial result there is never
// interleaved with an error:
//
//	{"error":{"message":"missing required input: <title>","category":"usage","kind":"missing-required","command":"taskr add","exit_code":1}}
//	{"warning":{"message":"the cache is stale","command":"taskr list"}}
//
// Infos and successes are written the same way, as {"info":{…}} and {"success":{…}}, rather
// than to stdout. A field is present only when rotini knows it: kind, flag and token come from
// a [*ParseError], kind from a [*PluginError] too. Each line's shape is described by
// schema-error.json in the rotini repository, and the contract document includes it.
//
// When structured reports false, or is nil, it reports exactly as the default reporter does.
// Either way the exit code is decided as the default reporter decides it.
//
// Which runs are structured is the program's call — typically whether its own format flag asks
// for json. The reporter runs after the command, and the run may have failed because parsing
// did, so read the flag from [Context.Argv] rather than from validated inputs:
//
//	cmd.Program.WithReporter(rotini.StructuredReporter(func(rtx *rotini.Context) bool {
//	    return slices.Contains(rtx.Argv, "--json")
//	})).Execute()
func StructuredReporter(structured func(rtx *Context) bool) Reporter {
	return func(_ context.Context, rtx *Context, out Outcome) {
		if structured == nil || !structured(rtx) {
			reportText(rtx, out)
			return
		}
		reportStructured(rtx, out)
	}
}

// reportStructured writes the outcome as JSON lines on stderr.
func reportStructured(rtx *Context, out Outcome) {
	// A deliberate handler exit is never downgraded, as in the default reporter.
	if rtx.exitCode == 0 && out.Failed() {
		rtx.exitCode = 1
	}
	command := rtx.commandName()
	lines := make([]map[string]any, 0, len(out.Infos)+len(out.Warnings)+len(out.Errors)+len(out.Panics)+len(out.Successes))
	message := func(kind, msg string) map[string]any {
		body := map[string]any{"message": msg}
		if command != "" {
			body["command"] = command
		}
		return map[string]any{kind: body}
	}
	failure := func(err error) map[string]any {
		line := message("error", err.Error())
		body, _ := line["error"].(map[string]any)
		body["category"] = CategoryOf(err).String()
		body["exit_code"] = rtx.exitCode
		if pe, ok := errors.AsType[*ParseError](err); ok {
			if pe.Kind != ParseKindUnspecified {
				body["kind"] = pe.Kind.String()
			}
			if pe.Flag != "" {
				body["flag"] = pe.Flag
			}
			if pe.Token != "" {
				body["token"] = pe.Token
			}
		} else if pe, ok := errors.AsType[*PluginError](err); ok {
			body["kind"] = pe.Kind.String()
		}
		return line
	}
	for _, s := range out.Infos {
		lines = append(lines, message("info", s))
	}
	for _, w := range out.Warnings {
		lines = append(lines, message("warning", w.Error()))
	}
	for _, e := range out.Errors {
		lines = append(lines, failure(e))
	}
	for _, pe := range out.Panics {
		lines = append(lines, failure(pe))
	}
	for _, s := range out.Successes {
		lines = append(lines, message("success", s))
	}
	for _, line := range lines {
		b, err := json.Marshal(line)
		if err != nil {
			continue // unreachable: every value is a string or an int
		}
		fmt.Fprintf(rtx.Stderr, "%s\n", b)
	}
}

// reportText reports as the default reporter does, through the run's streams.
func reportText(rtx *Context, out Outcome) {
	for _, s := range out.Infos {
		fmt.Fprintln(rtx.Stdout, s)
	}
	for _, w := range out.Warnings {
		fmt.Fprintf(rtx.Stderr, "Warning: %s\n", w.Error())
	}
	for _, e := range out.Errors {
		fmt.Fprintf(rtx.Stderr, "Error: %s\n", e.Error())
	}
	for _, pe := range out.Panics {
		fmt.Fprintf(rtx.Stderr, "Fatal Error: %v\n", pe)
	}
	for _, s := range out.Successes {
		fmt.Fprintln(rtx.Stdout, s)
	}
	if rtx.exitCode == 0 && out.Failed() {
		rtx.exitCode = 1
	}
}

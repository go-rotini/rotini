package codegen

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// This file holds the lint rules for how data reaches a command: a streamed or file-gated
// stdin, NUL-separated content, environment variables read from files, a .env file read as
// environment, and the config directories discovery searches.

// lintStdinStream restricts `stream` to the line-based formats, and the stdin `separator` to
// 'lines'.
func lintStdinStream(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		s := c.Stdin
		if s == nil {
			return
		}
		format := s.Format
		if format == "" {
			format = "json"
		}
		if s.Stream && format != "lines" && format != "jsonl" {
			problems = append(problems, inputProblem(ptr+"/stdin/stream", path, "stdin", "",
				fmt.Sprintf("sets `stream`, which applies to lines and jsonl, where stdin is read one line or record at a time; `format` %q is read whole", format)))
		}
		if s.Separator != "" && format != "lines" {
			problems = append(problems, inputProblem(ptr+"/stdin/separator", path, "stdin", "",
				fmt.Sprintf("sets `separator`, which chooses what separates lines and applies to `format: lines` only; `format` %q has no lines to split", format)))
		}
	})
	return problems
}

// lintStdinUnlessArgument requires `stdin.unless_argument` to name an optional file argument
// of the same command: an `inputfile` or `existingfile`, or a list of either, with nothing that
// would always give it a value. An `existingfile` can't take "-", so it gets a warning.
func lintStdinUnlessArgument(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if c.Stdin == nil || c.Stdin.UnlessArgument == "" {
			return
		}
		name := c.Stdin.UnlessArgument
		add := func(sev severity, msg string) {
			p := inputProblem(ptr+"/stdin/unless_argument", path, "stdin", "", msg)
			p.sev = sev
			problems = append(problems, p)
		}
		if c.Passthrough {
			add(severityError, "sets `unless_argument` on a passthrough command, whose arguments are raw words rather than files")
			return
		}
		i := slices.IndexFunc(c.Arguments, func(a ArgumentInput) bool { return a.Name == name })
		if i < 0 {
			add(severityError, fmt.Sprintf("sets `unless_argument: %s`, but the command declares no argument %q; name one of its own file arguments", name, name))
			return
		}
		arg := c.Arguments[i]
		elem := fileArgumentKind(arg.Schema)
		switch {
		case arg.Passthrough:
			add(severityError, fmt.Sprintf("sets `unless_argument: %s`, a passthrough argument, whose words are raw rather than files", name))
		case elem != "inputfile" && elem != "existingfile":
			add(severityError, fmt.Sprintf("sets `unless_argument: %s`, but its type is %s; stdin is read when no file is given, so the argument must be a file path: `inputfile` or `existingfile`, or a list of either", name, displayType(argumentType(arg.Schema))))
		case arg.Schema.Required || arg.Schema.MinItems > 0:
			add(severityError, fmt.Sprintf("sets `unless_argument: %s`, but the argument is required, so a file is always given and stdin is never read; make it optional", name))
		case arg.Schema.Default != nil:
			add(severityError, fmt.Sprintf("sets `unless_argument: %s`, but the argument has a `default`, so it always has a value and stdin is never read; remove the default", name))
		case elem == "existingfile":
			add(severityWarning, fmt.Sprintf("sets `unless_argument: %s`, an `existingfile`, which can't be `-`, so stdin is read only when no file is given; use `inputfile` to also accept `-`", name))
		}
	})
	return problems
}

// fileArgumentKind returns the element type an argument declares, as written: "existingfile"
// for `existingfile`, `[]existingfile` or an array of them, and so on.
func fileArgumentKind(s *InputSchema) string {
	if s == nil {
		return ""
	}
	if elem, ok := strings.CutPrefix(s.Type, "[]"); ok {
		return elem
	}
	if (s.Type == "array" || s.Type == "list") && s.Items != nil {
		return s.Items.Type
	}
	return s.Type
}

// argumentType is the declared type for a message.
func argumentType(s *InputSchema) string {
	if s == nil {
		return ""
	}
	return s.Type
}

// lintNulSeparator allows `separator: nul` only on list and map flags that read content
// themselves (`from: [file]` or `from: [stdin]`): command-line words and environment values
// can't contain a NUL byte, so nothing else could ever be split on one.
func lintNulSeparator(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || schema.Separator != "nul" {
				return
			}
			add := func(msg string) { problems = append(problems, inputProblem(ptr, path, channel, name, msg)) }
			switch {
			case channel != "flag":
				add("sets `separator: nul`, but command-line and environment values can't contain a NUL byte; it applies only to a list or map flag's file or stdin content (`from: [file]` or `from: [stdin]`)")
			case !slices.Contains(schema.From, "file") && !slices.Contains(schema.From, "stdin"):
				add("sets `separator: nul`, but command-line values can't contain a NUL byte, so there is nothing to split; add `from: [file]` or `from: [stdin]` so the flag's file or piped content is split on NUL")
			}
		})
	})
	return problems
}

// lintVariableFile restricts `variable_file` to env inputs, and flags and arguments that read an
// environment variable, and requires its name to be unique: not one of the input's own
// variables, and not a variable another input of the chain reads.
func lintVariableFile(spec *Spec) []error {
	var problems []error
	walkChainsAt(spec, func(chain []*Command, path, ptr string) {
		c := chain[len(chain)-1]
		taken := chainEnvNames(chain, spec.Command.EnvPrefix)
		check := func(channel, name, ptr string, schema *InputSchema, shortCircuit bool) {
			if schema == nil || schema.VariableFile == "" {
				return
			}
			file := schema.VariableFile
			add := func(msg string) { problems = append(problems, inputProblem(ptr, path, channel, name, msg)) }
			switch {
			case channel != "env" && channel != "flag" && channel != "argument":
				add("sets `variable_file`, which names an environment variable; only env inputs, and flags and arguments with an environment fallback, read one, so here it would be silently ignored")
				return
			case channel != "env" && flagReconKey(name, schema) == "":
				add(fmt.Sprintf("sets `variable_file` on %s %s with no environment fallback; set `variable:` (or `key:`) so the %s reads an environment variable, then `variable_file` names its file form", map[string]string{"flag": "a", "argument": "an"}[channel], channel, channel))
				return
			case schema.Nesting != "":
				add("sets both `variable_file` and `nesting`; a nested input reads a family of variables, not one value a file could hold")
			case schema.ConfigSource != "":
				add("sets both `variable_file` and `config_source`; the config file's path is read from the variable itself, so name it directly")
			case shortCircuit:
				add("sets `variable_file` on a short-circuit flag, which answers from the command line alone")
			}
			if slices.Contains(variables(schema), file) {
				add(fmt.Sprintf("sets `variable_file: %s`, which is also one of its own `variable` names; the file form needs its own name (by convention %s_FILE)", file, file))
			} else if owner, ok := taken[file]; ok && owner != channel+" "+name {
				add(fmt.Sprintf("sets `variable_file: %s`, a variable %s also reads; give the file form its own name", file, owner))
			}
		}
		for i, f := range c.Flags {
			check("flag", f.Name, fmt.Sprintf("%s/flags/%d", ptr, i), f.Schema, f.ShortCircuit)
		}
		for i, e := range c.Env {
			check("env", e.Name, fmt.Sprintf("%s/env/%d", ptr, i), e.Schema, false)
		}
		for i, a := range c.Arguments {
			check("argument", a.Name, fmt.Sprintf("%s/arguments/%d", ptr, i), a.Schema, false)
		}
		for i, cf := range c.Config {
			check("config", cf.Name, fmt.Sprintf("%s/config/%d", ptr, i), cf.Schema, false)
		}
		if c.Stdin != nil {
			check("stdin", "", ptr+"/stdin", c.Stdin.Schema, false)
		}
	})
	return problems
}

// chainEnvNames maps every environment variable the chain's env inputs and flag fallbacks read,
// file variables included, to the input reading it ("env token", "flag token").
func chainEnvNames(chain []*Command, envPrefix string) map[string]string {
	names := map[string]string{}
	put := func(name, owner string) {
		if _, ok := names[name]; !ok && name != "" {
			names[name] = owner
		}
	}
	for _, c := range chain {
		for _, e := range c.Env {
			put(envVarName(e, envPrefix), "env "+e.Name)
			for _, v := range variables(e.Schema) {
				put(v, "env "+e.Name)
			}
			if e.Schema != nil {
				put(e.Schema.VariableFile, "env "+e.Name)
			}
		}
		for _, f := range c.Flags {
			key := flagReconKey(f.Name, f.Schema)
			if key == "" {
				continue
			}
			for v := range strings.SplitSeq(flagEnvVar(f.Schema, key, envPrefix), ",") {
				put(v, "flag "+f.Name)
			}
			if f.Schema != nil {
				put(f.Schema.VariableFile, "flag "+f.Name)
			}
		}
	}
	return names
}

// lintConfigFilesAsEnv checks a config_files entry read as environment (`as: env`): it must be
// a dotenv file, and no config input can pin it, since its keys are environment variables, not
// configuration keys.
func lintConfigFilesAsEnv(spec *Spec) []error {
	var problems []error
	walkChainsAt(spec, func(chain []*Command, path, ptr string) {
		c := chain[len(chain)-1]
		envFiles := map[string]bool{}
		for _, a := range chain {
			for _, cf := range a.ConfigFiles {
				if cf.As == "env" {
					envFiles[cf.Name] = true
				}
			}
		}
		for i, cf := range c.ConfigFiles {
			if cf.As != "env" {
				continue
			}
			if !dotenvFile(cf) {
				problems = append(problems, configFileProblem(ptr, path, i, cf.Name,
					"sets `as: env`, which reads the file's KEY=value lines as environment variables, so it must be a dotenv file; set `format: dotenv`"))
			}
		}
		for i, in := range c.Config {
			if in.Schema != nil && envFiles[in.Schema.File] {
				problems = append(problems, inputProblem(fmt.Sprintf("%s/config/%d/schema/file", ptr, i), path, "config", in.Name,
					fmt.Sprintf("pins `file: %s`, a file read as environment (`as: env`); read its variables with an env input instead", in.Schema.File)))
			}
		}
	})
	return problems
}

// dotenvFile reports whether a config_files entry is read as dotenv: declared so, or (with no
// format) named like one.
func dotenvFile(cf ConfigurationFile) bool {
	if cf.Format != "" {
		return cf.Format == "dotenv"
	}
	name := cf.Path
	if cf.Discover != nil {
		name = cf.Discover.File
	}
	base := filepath.Base(name)
	return base == ".env" || strings.HasSuffix(base, ".env") || strings.HasPrefix(base, ".env.")
}

// lintDiscoverApp requires `app` for the 'native' and 'xdg-system' discovery strategies, which
// search <dir>/<app>.
func lintDiscoverApp(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		for i, cf := range c.ConfigFiles {
			d := cf.Discover
			if d == nil || d.App != "" || (d.Strategy != "native" && d.Strategy != "xdg-system") {
				continue
			}
			problems = append(problems, configFileProblem(ptr, path, i, cf.Name,
				fmt.Sprintf("`discover` strategy %q needs `app` (the directory under the config root)", d.Strategy)))
		}
	})
	return problems
}

// runtimeSeparator is a declared `separator` as the runtime splits on it: the word "nul" is the
// NUL byte.
func runtimeSeparator(sep string) string {
	if sep == "nul" {
		return "\x00"
	}
	return sep
}

// fileVariableNote marks a `variable_file` variable where pages list an input's variables: its
// value is the path of a file holding the input's value.
const fileVariableNote = " (a file)"

package internal

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/go-rotini/jsonschema"
)

// This file owns the `rotini validate` operation end-to-end: the session/file-level
// validation that the Processor drives, plus the machinery (schema validation, the
// $schema↔version guard, and the rotini-specific rules the JSON Schema can't express).

// ValidateFn is the signature of [Processor.Validate]. A command handler binds it
// under a registry key and fetches it as an injectable service, so tests substitute a
// double (see [GenerateFn]).
type ValidateFn = func(specPath, confPath string, watch bool, failMode string, onValidate func(result string, err error)) error

// Validate is a convenience over [Processor.Validate]: it builds a Processor for
// version and runs the validate workflow. The companion handlers drive the Processor
// directly; this serves internal callers (tests).
func Validate(specPath, confPath string, watch bool, failMode, version string, onValidate func(result string, err error)) error {
	return NewProcessor(version).Validate(specPath, confPath, watch, failMode, onValidate)
}

// ─── session + file validation ────────────────────────────────────────────────

// validate runs the validator phase over the loaded spec and conf (call load first):
// each file validates itself. Problems are aggregated via errors.Join, or — in fast
// mode — the first is returned. It returns nil when both are valid.
func (s *session) validate() error {
	fast := s.failFast()

	problems := s.spec.validate()
	if fast && len(problems) > 0 {
		return problems[0]
	}

	problems = append(problems, s.conf.validate()...)
	if fast && len(problems) > 0 {
		return problems[0]
	}
	return errors.Join(problems...)
}

// failFast reports whether validation should stop at the first problem. The --fail
// override (s.failMode) wins; otherwise the loaded conf's validate.fail is used. Only
// "fast" enables it — anything else collects every problem (the default).
func (s *session) failFast() bool {
	mode := s.failMode
	if mode == "" && s.conf != nil && s.conf.conf != nil && s.conf.conf.Validate != nil {
		mode = s.conf.conf.Validate.Fail
	}
	return mode == "fast"
}

// validate schema-validates the spec against its compiled schema on the raw JSON
// instance (so unknown-field rules fire), then — only when it is schema-valid — runs
// the rotini-specific rules and enforces the $schema↔version guard. It returns every
// problem found, empty when the spec is valid.
func (l *specLoader) validate() []error {
	if problems := validateInstance("spec", l.instance, l.schema); len(problems) > 0 {
		return problems
	}

	var problems []error
	for _, rule := range specLints {
		problems = append(problems, rule(l.spec)...)
	}
	if err := checkSchemaVersion("spec", l.spec.Schema, l.version); err != nil {
		problems = append(problems, err)
	}
	return problems
}

// validate schema-validates the conf against its compiled schema on the raw JSON
// instance when one was resolved, then — only when it is schema-valid — runs the
// rotini-specific conf rules and enforces the $schema↔version guard. A default
// conf (no file) has nothing to validate. It returns every problem found.
func (l *confLoader) validate() []error {
	if l.path == "" {
		return nil
	}
	if problems := validateInstance("conf", l.instance, l.schema); len(problems) > 0 {
		return problems
	}

	var problems []error
	for _, rule := range confLints {
		problems = append(problems, rule(l.conf)...)
	}
	if err := checkSchemaVersion("conf", l.conf.Schema, l.version); err != nil {
		problems = append(problems, err)
	}
	return problems
}

// ─── schema validation + the $schema↔version guard ─────────────────────────────

// problem is a single validation failure: the location of the offending value within
// the document and a human-readable message, tagged by document kind ("spec"/"conf").
type problem struct {
	kind string
	loc  string
	msg  string
}

func (e *problem) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.kind, e.loc, e.msg)
}

// validateInstance validates a document's raw JSON instance (so schema rules
// like additionalProperties:false see unknown fields) against the given
// compiled schema, returning one [*problem] per violation. The instance is the
// one the loader converted from its single read, so validation and generation
// always judge the same bytes. It returns nil when the document is valid.
func validateInstance(kind string, instance []byte, schema *jsonschema.Schema) []error {
	result, err := schema.Validate(instance)
	if err != nil {
		return []error{fmt.Errorf("validate %s: %w", kind, err)}
	}
	if result.Valid {
		return nil
	}

	problems := make([]error, 0, len(result.Errors))
	for i := range result.Errors {
		ve := &result.Errors[i]
		loc := ve.InstanceLocation
		if loc == "" {
			loc = "/"
		}
		problems = append(problems, &problem{kind: kind, loc: loc, msg: ve.Message})
	}
	return problems
}

// rotiniSchemaURLRe matches the recognized rotini `$schema` URL form and captures the
// X.Y.Z version segment. A URL that doesn't match (absent, a branch ref, a different
// host) yields no capture, and checkSchemaVersion skips it.
var rotiniSchemaURLRe = regexp.MustCompile(`^https://raw\.githubusercontent\.com/go-rotini/rotini/refs/tags/([0-9]+\.[0-9]+\.[0-9]+)/schema-(?:spec|conf)\.json$`)

// checkSchemaVersion enforces that a document's `$schema` targets the same rotini
// release as the running binary. version is the binary's bound version string
// ("vX.Y.Z" or "v0.0.0"); its leading "v" is stripped to the "X.Y.Z" segment compared
// against the document's `$schema` version. The check is skipped when the version is
// empty/unknown, or when the document's `$schema` is absent or not the recognized
// rotini refs/tags/<VER> form. A present, recognized, mismatched `$schema` is an error.
func checkSchemaVersion(kind, docSchema, version string) error {
	want := strings.TrimPrefix(version, "v")
	if want == "" {
		return nil
	}
	m := rotiniSchemaURLRe.FindStringSubmatch(docSchema)
	if m == nil {
		return nil
	}
	if docVer := m[1]; docVer != want {
		return &problem{
			kind: kind,
			loc:  "$schema",
			msg:  fmt.Sprintf("targets schema version %s but this rotini is %s — update the $schema version (or your rotini install) so they match", docVer, want),
		}
	}
	return nil
}

// ─── the rotini-specific rules (what the JSON Schema can't express) ─────────────

// confLints is the ordered set of conf rules run after the conf is schema-valid,
// mirroring specLints. Like the spec rules, they reject configuration that would
// be silently ignored or that generate would reject later — validate is the gate.
var confLints = []func(*Conf) []error{
	lintEntrypoint,
	lintFeatureDirs,
}

// lintEntrypoint rejects an entrypoint block whose pieces would be silently
// ignored: 'file'/'keep' without the required 'package' (the entrypoint is only
// written when its package is declared), and 'keep' at all (the entrypoint
// package is never pruned, so a keep list is an accepted lie).
func lintEntrypoint(conf *Conf) []error {
	if conf.Generate == nil || conf.Generate.Packages == nil || conf.Generate.Packages.Entrypoint == nil {
		return nil
	}
	ep := conf.Generate.Packages.Entrypoint
	var problems []error
	if ep.Package == "" && (ep.File != "" || len(ep.Keep) > 0) {
		problems = append(problems, &problem{
			kind: "conf",
			loc:  "generate.packages.entrypoint",
			msg:  "declares file/keep but no package — the entrypoint main.go is only written when entrypoint.package is set",
		})
	}
	if len(ep.Keep) > 0 {
		problems = append(problems, &problem{
			kind: "conf",
			loc:  "generate.packages.entrypoint.keep",
			msg:  "has no effect — the entrypoint package is never pruned; remove it",
		})
	}
	return problems
}

// lintFeatureDirs rejects an enabled feature whose explicit dir cannot resolve
// under an explicitly-set cmdgen package — //go:embed could never reach it, so
// generate would fail; validate is the gate. When either side is unset the
// defaults guarantee nesting (the default dir is <cmdgen>/embed), so there is
// nothing to check — generate's resolution backstops the remaining cases.
func lintFeatureDirs(conf *Conf) []error {
	if conf.Generate == nil || conf.Generate.Features == nil ||
		conf.Generate.Packages == nil || conf.Generate.Packages.Cmdgen == nil ||
		conf.Generate.Packages.Cmdgen.Package == "" {
		return nil
	}
	cmdgen := strings.TrimSuffix(filepath.ToSlash(conf.Generate.Packages.Cmdgen.Package), "/")
	feats := conf.Generate.Features
	var problems []error
	check := func(name string, f *Feature) {
		if f == nil || !f.Enabled || f.Dir == "" {
			return
		}
		dir := path.Clean(filepath.ToSlash(f.Dir))
		if dir != cmdgen && !strings.HasPrefix(dir, cmdgen+"/") {
			problems = append(problems, &problem{
				kind: "conf",
				loc:  "generate.features." + name + ".dir",
				msg:  fmt.Sprintf("%q must resolve under the cmdgen package %q so //go:embed can reach it", f.Dir, cmdgen),
			})
		}
	}
	check("help", feats.Help)
	check("man", feats.Man)
	check("completion", feats.Completion)
	return problems
}

// specLints is the ordered set of spec rules run after the spec is schema-valid.
// Adding a rule is a one-line append here; each stays a pure func(*Spec) []error for
// isolated testing. The order is observable (collect mode joins problems in order), so
// keep it stable.
var specLints = []func(*Spec) []error{
	lintRootCommand,
	lintRootAliases,
	lintImportConsistency,
	lintLocalTimeout,
	lintFlagGroups,
	lintFlagDependencies,
	lintDuplicateFlagIdentifiers,
	lintSchemaRefs,
	lintHandlerFilenames,
	lintSiblingCollisions,
	lintDuplicateInputNames,
	lintVariadicArguments,
	lintDeprecatedIdentifiers,
	lintRemoteTimeouts,
	lintDottedKeys,
	lintFrom,
	lintConfigurationFiles,
	lintConfigSource,
	lintEnvNesting,
	lintConfigInputFiles,
}

// lintRootCommand enforces what the shared Command shape can't: the top-level
// command is the binary itself, so it must carry a name and cannot be composed
// via $ref. Generate enforces the same rule — validate is the gate.
func lintRootCommand(spec *Spec) []error {
	var problems []error
	if spec.Command.Ref != "" {
		problems = append(problems, &problem{kind: "spec", loc: "command", msg: "the root command cannot use $ref — compose child specs as sub-commands instead"})
	}
	if spec.Command.Name == "" {
		problems = append(problems, &problem{kind: "spec", loc: "command", msg: "the root command must have a name (it is the binary name)"})
	}
	return problems
}

// lintRootAliases rejects aliases (and therefore deprecated_identifiers, their
// subset) on the ROOT command: the root is reached by invoking the binary —
// argv[0] is not a routing token — so root aliases dispatch nothing and root
// deprecated_identifiers can never fire. Declare aliases on sub-commands.
// (Busybox-style multi-call argv[0] dispatch, if ever wanted, will be its own
// opt-in feature — never implied by root aliases.)
func lintRootAliases(spec *Spec) []error {
	var problems []error
	if len(spec.Command.Aliases) > 0 {
		problems = append(problems, &problem{
			kind: "spec", loc: "command",
			msg: "the root command cannot declare aliases — it is reached by invoking the binary, not by a routing token; declare aliases on sub-commands",
		})
	}
	if len(spec.Command.DeprecatedIdentifiers) > 0 {
		problems = append(problems, &problem{
			kind: "spec", loc: "command",
			msg: "the root command cannot declare deprecated_identifiers — with no routing token, a deprecated root alias can never be detected; declare them on sub-commands",
		})
	}
	return problems
}

// lintSiblingCollisions rejects duplicate dispatch tokens among one command's
// children: sub-command names and aliases, plus remote-command names and
// aliases, all share a single namespace (dispatch tries sub-commands first, so
// a colliding remote would be silently shadowed). Generate errors on the
// command/alias subset of this; validate is the gate.
func lintSiblingCollisions(spec *Spec) []error {
	var problems []error
	walkCommands(spec, func(c *Command, path string) {
		claimedBy := map[string]string{} // token -> the sibling that first claimed it
		claim := func(owner string, tokens ...string) {
			for _, tok := range tokens {
				if tok == "" {
					continue
				}
				if prev, dup := claimedBy[tok]; dup {
					problems = append(problems, &problem{
						kind: "spec",
						loc:  "command " + path,
						msg:  fmt.Sprintf("dispatch token %q is claimed by both %q and %q", tok, prev, owner),
					})
					continue
				}
				claimedBy[tok] = owner
			}
		}
		for i := range c.Commands {
			child := &c.Commands[i]
			owner := child.Name
			if owner == "" {
				owner = child.Ref
			}
			claim(owner, append([]string{child.Name}, child.Aliases...)...)
		}
		for _, r := range c.RemoteCommands {
			claim("remote "+r.Name, append([]string{r.Name}, r.Aliases...)...)
		}
	})
	return problems
}

// lintDuplicateInputNames rejects two inputs of the same channel sharing a
// logical name on one command — codegen derives one Go field per name, so a
// duplicate would emit an uncompilable struct (caught here as a clear spec
// error instead of a gofmt failure at generate time).
func lintDuplicateInputNames(spec *Spec) []error {
	var problems []error
	walkCommands(spec, func(c *Command, path string) {
		if c.Inputs == nil {
			return
		}
		seen := map[string]string{} // channel+name -> first declaration
		eachInputSchema(c.Inputs, func(channel, name string, _ *InputSchema) {
			if name == "" {
				return // stdin has no logical name
			}
			key := channel + "\x00" + name
			if _, dup := seen[key]; dup {
				problems = append(problems, &problem{
					kind: "spec",
					loc:  "command " + path,
					msg:  fmt.Sprintf("%s %q is declared twice — each %s needs a unique name", channel, name, channel),
				})
				return
			}
			seen[key] = name
		})
	})
	return problems
}

// lintVariadicArguments rejects a variadic (slice-typed) argument anywhere but
// the last position — a trailing variadic absorbs the remaining positionals, so
// anything declared after it could never bind.
func lintVariadicArguments(spec *Spec) []error {
	var problems []error
	walkCommands(spec, func(c *Command, path string) {
		if c.Inputs == nil {
			return
		}
		for i, a := range c.Inputs.Arguments {
			if i == len(c.Inputs.Arguments)-1 {
				break
			}
			if strings.HasPrefix(getSchemaType(a.Schema), "[]") {
				problems = append(problems, &problem{
					kind: "spec",
					loc:  "command " + path,
					msg:  fmt.Sprintf("argument %q is variadic but not last — it would absorb every remaining positional, so later arguments could never bind", a.Name),
				})
			}
		}
	})
	return problems
}

// lintDottedKeys enforces dotted_keys' documented scope: it is a flag-only
// option (dotted assignment is command-line grammar), and the flag must store
// nested maps — map[string]any ('map'/'object'), since a typed-value map like
// map[string]string has nowhere to hang a subtree.
func lintDottedKeys(spec *Spec) []error {
	var problems []error
	walkCommands(spec, func(c *Command, path string) {
		eachInputSchema(c.Inputs, func(channel, name string, schema *InputSchema) {
			if schema == nil || !schema.DottedKeys {
				return
			}
			loc := "command " + path
			if channel != "flag" {
				problems = append(problems, &problem{
					kind: "spec",
					loc:  loc,
					msg:  fmt.Sprintf("%s %q sets dotted_keys, which applies to flags only", channel, name),
				})
				return
			}
			if t := getSchemaType(schema); t != "map[string]any" {
				problems = append(problems, &problem{
					kind: "spec",
					loc:  loc,
					msg:  fmt.Sprintf("flag %q sets dotted_keys but its type is %s — dotted keys need 'map' (map[string]any) to nest into", name, t),
				})
			}
		})
	})
	return problems
}

// lintConfigurationFiles enforces each configuration_files entry's location
// contract: exactly one of path/discover, and the discover strategies' own
// requirements (xdg needs app; walk-up has no app to ignore silently).
func lintConfigurationFiles(spec *Spec) []error {
	var problems []error
	add := func(name, msg string) {
		problems = append(problems, &problem{kind: "spec", loc: "configuration_files " + name, msg: msg})
	}
	seen := map[string]bool{}
	for _, cf := range spec.ConfigurationFiles {
		if seen[cf.Name] {
			add(cf.Name, "is declared twice — logical names identify entries (file: pins, config_source) and must be unique")
		}
		seen[cf.Name] = true
		switch {
		case cf.Path == "" && cf.Discover == nil:
			add(cf.Name, "needs a location — set 'path' or 'discover'")
		case cf.Path != "" && cf.Discover != nil:
			add(cf.Name, "sets both 'path' and 'discover' — exactly one locates the file")
		}
		if d := cf.Discover; d != nil {
			if d.Strategy == "xdg" && d.App == "" {
				add(cf.Name, "discover strategy 'xdg' needs 'app' (the directory under the XDG config root)")
			}
			if d.Strategy == "walk-up" && d.App != "" {
				add(cf.Name, "discover strategy 'walk-up' does not use 'app' — remove it (it would be silently ignored)")
			}
		}
	}
	return problems
}

// lintEnvNesting enforces nesting:'s contract — a variable FAMILY aggregates
// into one nested map, so it is env-channel-only, needs map[string]any to nest
// into, and cannot carry a default (a single default string has no map shape;
// seed defaults in code or config instead).
func lintEnvNesting(spec *Spec) []error {
	var problems []error
	walkCommands(spec, func(c *Command, path string) {
		loc := "command " + path
		eachInputSchema(c.Inputs, func(channel, name string, schema *InputSchema) {
			if schema == nil || schema.Nesting == "" {
				return
			}
			if channel != "env" {
				problems = append(problems, &problem{
					kind: "spec", loc: loc,
					msg: fmt.Sprintf("%s %q sets nesting, which applies to env inputs only", channel, name),
				})
				return
			}
			if t := getSchemaType(schema); t != "map[string]any" {
				problems = append(problems, &problem{
					kind: "spec", loc: loc,
					msg: fmt.Sprintf("env %q sets nesting but its type is %s — a variable family needs 'map' (map[string]any) to nest into", name, t),
				})
			}
			if schema.Default != nil {
				problems = append(problems, &problem{
					kind: "spec", loc: loc,
					msg: fmt.Sprintf("env %q sets both nesting and default — a nested family has no single default; seed defaults in code or config instead", name),
				})
			}
		})
	})
	return problems
}

// lintConfigSource enforces config_source's contract: flag/env inputs only,
// string-typed, naming a declared configuration_files entry, with at most one
// flag and one env input claiming any entry (a second claim would silently
// shadow the first).
func lintConfigSource(spec *Spec) []error {
	var problems []error
	declared := map[string]bool{}
	for _, cf := range spec.ConfigurationFiles {
		declared[cf.Name] = true
	}
	claims := map[string]map[string]string{} // file → channel → claiming input
	walkCommands(spec, func(c *Command, path string) {
		loc := "command " + path
		eachInputSchema(c.Inputs, func(channel, name string, schema *InputSchema) {
			if schema == nil || schema.ConfigSource == "" {
				return
			}
			target := schema.ConfigSource
			if channel != "flag" && channel != "env" {
				problems = append(problems, &problem{
					kind: "spec", loc: loc,
					msg: fmt.Sprintf("%s %q sets config_source, which applies to flag and env inputs only", channel, name),
				})
				return
			}
			if !declared[target] {
				problems = append(problems, &problem{
					kind: "spec", loc: loc,
					msg: fmt.Sprintf("%s %q names config_source %q, which is not a declared configuration_files entry", channel, name, target),
				})
				return
			}
			if t := getSchemaType(schema); t != "string" {
				problems = append(problems, &problem{
					kind: "spec", loc: loc,
					msg: fmt.Sprintf("%s %q sets config_source but its type is %s — a file path is a string", channel, name, t),
				})
			}
			if claims[target] == nil {
				claims[target] = map[string]string{}
			}
			if prev, dup := claims[target][channel]; dup {
				problems = append(problems, &problem{
					kind: "spec", loc: loc,
					msg: fmt.Sprintf("%s %q claims config_source %q, already claimed by %s %q — one %s per entry", channel, name, target, channel, prev, channel),
				})
				return
			}
			claims[target][channel] = name
		})
	})
	return problems
}

// lintConfigInputFiles enforces file:'s contract: config inputs only, naming a
// declared configuration_files entry — the input's value is then read from
// that file ONLY (not the merged precedence chain), including its required.
func lintConfigInputFiles(spec *Spec) []error {
	var problems []error
	declared := map[string]bool{}
	for _, cf := range spec.ConfigurationFiles {
		declared[cf.Name] = true
	}
	walkCommands(spec, func(c *Command, path string) {
		loc := "command " + path
		eachInputSchema(c.Inputs, func(channel, name string, schema *InputSchema) {
			if schema == nil || schema.File == "" {
				return
			}
			if channel != "config" {
				problems = append(problems, &problem{
					kind: "spec", loc: loc,
					msg: fmt.Sprintf("%s %q sets file:, which applies to config inputs only", channel, name),
				})
				return
			}
			if !declared[schema.File] {
				problems = append(problems, &problem{
					kind: "spec", loc: loc,
					msg: fmt.Sprintf("config %q pins file %q, which is not a declared configuration_files entry", name, schema.File),
				})
			}
		})
	})
	return problems
}

// lintFrom enforces from:'s documented scope — acquisition sentinels are argv
// flag grammar: flags only, never bool flags (their value is inline-only), and
// stdin has one consumer, so a from:stdin flag cannot coexist with a declared
// stdin: channel or another from:stdin flag on the same command.
func lintFrom(spec *Spec) []error {
	var problems []error
	walkCommands(spec, func(c *Command, path string) {
		loc := "command " + path
		stdinClaim := "" // what already claimed this command's stdin
		if c.Inputs != nil && c.Inputs.Stdin != nil {
			stdinClaim = "the stdin: channel"
		}
		eachInputSchema(c.Inputs, func(channel, name string, schema *InputSchema) {
			if schema == nil || len(schema.From) == 0 {
				return
			}
			if channel != "flag" {
				problems = append(problems, &problem{
					kind: "spec",
					loc:  loc,
					msg:  fmt.Sprintf("%s %q sets from:, which applies to flags only", channel, name),
				})
				return
			}
			if t := getSchemaType(schema); t == "bool" {
				problems = append(problems, &problem{
					kind: "spec",
					loc:  loc,
					msg:  fmt.Sprintf("flag %q sets from: but is bool — a bool takes no value to resolve", name),
				})
			}
			if slices.Contains(schema.From, "stdin") {
				if stdinClaim != "" {
					problems = append(problems, &problem{
						kind: "spec",
						loc:  loc,
						msg:  fmt.Sprintf("flag %q declares from: stdin but %s already consumes stdin — stdin has one consumer", name, stdinClaim),
					})
					return
				}
				stdinClaim = fmt.Sprintf("flag %q", name)
			}
		})
	})
	return problems
}

// lintDeprecatedIdentifiers enforces the documented subset rule: a command's
// deprecated_identifiers must be aliases it declares, and a flag's must be
// identifiers it declares (or derives) — an unlisted token would never be
// reported as deprecated, silently voiding the annotation.
func lintDeprecatedIdentifiers(spec *Spec) []error {
	var problems []error
	subset := func(path, owner string, declared, deprecated []string, vocab string) {
		known := map[string]bool{}
		for _, d := range declared {
			known[d] = true
		}
		for _, d := range deprecated {
			if !known[d] {
				msg := fmt.Sprintf("%s deprecated_identifiers entry %q is not one of its %s — it could never be reported as deprecated", owner, d, vocab)
				problems = append(problems, &problem{kind: "spec", loc: "command " + path, msg: didYouMean(msg, d, declared)})
			}
		}
	}
	walkCommands(spec, func(c *Command, path string) {
		for i := range c.Commands {
			child := &c.Commands[i]
			if len(child.DeprecatedIdentifiers) > 0 {
				subset(path, fmt.Sprintf("sub-command %q", child.Name), child.Aliases, child.DeprecatedIdentifiers, "aliases")
			}
		}
		if c.Inputs == nil {
			return
		}
		for _, f := range c.Inputs.Flags {
			if len(f.DeprecatedIdentifiers) > 0 {
				subset(path, fmt.Sprintf("flag %q", f.Name), flagIdentifiers(f), f.DeprecatedIdentifiers, "identifiers")
			}
		}
	})
	return problems
}

// lintRemoteTimeouts rejects a remote_commands timeout that does not parse as a
// Go duration — codegen would otherwise drop it silently, leaving the remote
// unbounded despite the declared limit.
func lintRemoteTimeouts(spec *Spec) []error {
	var problems []error
	walkCommands(spec, func(c *Command, path string) {
		for _, r := range c.RemoteCommands {
			if r.Timeout == "" {
				continue
			}
			if d, err := time.ParseDuration(r.Timeout); err != nil || d <= 0 {
				problems = append(problems, &problem{
					kind: "spec",
					loc:  "command " + path,
					msg:  fmt.Sprintf("remote_commands %q timeout %q is not a positive Go duration (e.g. \"10s\", \"1m30s\")", r.Name, r.Timeout),
				})
			}
		}
	})
	return problems
}

// walkCommands visits every command in the spec depth-first (pre-order), passing a
// display path — "root/child/grandchild", using a child's $ref segment when it has
// no name (and "(root)" for an unnamed root). The rules call this instead of each
// re-defining the same recursive walk.
func walkCommands(spec *Spec, visit func(c *Command, path string)) {
	var walk func(c *Command, path string)
	walk = func(c *Command, path string) {
		visit(c, path)
		for i := range c.Commands {
			child := &c.Commands[i]
			seg := child.Name
			if seg == "" {
				seg = child.Ref
			}
			walk(child, path+"/"+seg)
		}
	}
	name := spec.Command.Name
	if name == "" {
		name = "(root)"
	}
	walk(&spec.Command, name)
}

// flagNames returns the set of a command's declared flag names plus an ordered slice
// of them (for closestName "did you mean?" suggestions). Shared by the flag_groups
// and flag_dependencies rules.
func flagNames(c *Command) (known map[string]bool, ordered []string) {
	known = map[string]bool{}
	if c.Inputs == nil {
		return known, nil
	}
	ordered = make([]string, 0, len(c.Inputs.Flags))
	for _, f := range c.Inputs.Flags {
		known[f.Name] = true
		ordered = append(ordered, f.Name)
	}
	return known, ordered
}

// eachInputSchema visits each declared input schema of a command across all
// channels (flag, argument, env, config, stdin), passing the channel label and the
// input's logical name ("" for stdin). It is the single place the schema-walking
// rules enumerate a command's input channels.
func eachInputSchema(in *Inputs, visit func(channel, name string, schema *InputSchema)) {
	if in == nil {
		return
	}
	for i := range in.Flags {
		visit("flag", in.Flags[i].Name, in.Flags[i].Schema)
	}
	for i := range in.Arguments {
		visit("argument", in.Arguments[i].Name, in.Arguments[i].Schema)
	}
	for i := range in.Env {
		visit("env", in.Env[i].Name, in.Env[i].Schema)
	}
	for i := range in.Config {
		visit("config", in.Config[i].Name, in.Config[i].Schema)
	}
	if in.Stdin != nil {
		visit("stdin", "", in.Stdin.Schema)
	}
}

// didYouMean appends a "; did you mean %q?" suffix to msg when one of candidates is
// a near-match (edit distance < 3) for name, else returns msg unchanged. Shared by
// the rules that suggest a fix for a typo'd flag or schema name.
func didYouMean(msg, name string, candidates []string) string {
	if s := closestName(name, candidates); s != "" {
		return msg + fmt.Sprintf("; did you mean %q?", s)
	}
	return msg
}

// lintImportConsistency reports any `type:` declared with two or more different
// `import:` values across the spec — the same type with two backing packages is
// always a bug. (A wrong-but-consistent import is left to `go build`; this catches
// the contradictory case at validate time.) One problem per offending type, sorted.
func lintImportConsistency(spec *Spec) []error {
	byType := map[string]map[string]bool{}
	record := func(typ, imp string) {
		typ, imp = strings.TrimSpace(typ), strings.TrimSpace(imp)
		if typ == "" || imp == "" {
			return
		}
		if byType[typ] == nil {
			byType[typ] = map[string]bool{}
		}
		byType[typ][imp] = true
	}
	walkCommands(spec, func(c *Command, _ string) {
		eachInputSchema(c.Inputs, func(_, _ string, s *InputSchema) {
			if s != nil {
				walkSchemaImports(s.BaseSchema, record)
			}
		})
		if c.Output != nil {
			walkSchemaImports(c.Output.BaseSchema, record)
		}
	})
	for name := range spec.Schemas {
		s := spec.Schemas[name]
		walkSchemaImports(s.BaseSchema, record)
	}

	var problems []error
	for typ, imps := range byType {
		if len(imps) < 2 {
			continue
		}
		list := make([]string, 0, len(imps))
		for imp := range imps {
			list = append(list, imp)
		}
		sort.Strings(list)
		problems = append(problems, &problem{
			kind: "spec",
			loc:  "type " + typ,
			msg:  "declared with conflicting imports (" + strings.Join(list, ", ") + "); a type must have one backing package",
		})
	}
	sort.Slice(problems, func(i, j int) bool { return problems[i].Error() < problems[j].Error() })
	return problems
}

// lintLocalTimeout rejects a `timeout` declared on any command. A timeout is a
// remote-only, host-side bound on a dispatched binary (set per remote_commands entry,
// honored by the remote runtime); on a local command it is never honored, so accepting
// it would be a silent lie. One problem per offending command, in tree order. The
// remote_commands[].timeout is a separate field and is left untouched.
func lintLocalTimeout(spec *Spec) []error {
	var problems []error
	walkCommands(spec, func(c *Command, path string) {
		if strings.TrimSpace(c.Timeout) != "" {
			problems = append(problems, &problem{
				kind: "spec",
				loc:  "command " + path,
				msg:  "timeout is not supported on a local command — it is a remote-only, host-side bound with no effect here; set it on a remote_commands entry's timeout instead",
			})
		}
	})
	return problems
}

// lintFlagGroups checks that every flag_groups entry references flags that actually
// exist on the same command (a typo'd flag name would otherwise silently never match
// at runtime). One problem per bad reference, in tree order.
func lintFlagGroups(spec *Spec) []error {
	var problems []error
	walkCommands(spec, func(c *Command, path string) {
		if c.Inputs == nil || len(c.Inputs.FlagGroups) == 0 {
			return
		}
		known, ordered := flagNames(c)
		for _, g := range c.Inputs.FlagGroups {
			for _, name := range g.Flags {
				if !known[name] {
					msg := fmt.Sprintf("flag_groups (%s) references unknown flag %q — it has no matching entry in this command's flags", g.Kind, name)
					problems = append(problems, &problem{kind: "spec", loc: "command " + path, msg: didYouMean(msg, name, ordered)})
				}
			}
		}
	})
	return problems
}

// lintFlagDependencies rejects a flag_dependencies entry whose When or Requires
// references a flag the command doesn't declare — the conditional could never fire (or
// could never be satisfied), masking a typo.
func lintFlagDependencies(spec *Spec) []error {
	var problems []error
	walkCommands(spec, func(c *Command, path string) {
		if c.Inputs == nil || len(c.Inputs.FlagDependencies) == 0 {
			return
		}
		known, ordered := flagNames(c)
		report := func(name string) {
			msg := fmt.Sprintf("flag_dependencies references unknown flag %q — it has no matching entry in this command's flags", name)
			problems = append(problems, &problem{kind: "spec", loc: "command " + path, msg: didYouMean(msg, name, ordered)})
		}
		for _, dep := range c.Inputs.FlagDependencies {
			if !known[dep.When] {
				report(dep.When)
			}
			for _, name := range dep.Requires {
				if !known[name] {
					report(name)
				}
			}
		}
	})
	return problems
}

// lintDuplicateFlagIdentifiers rejects a command that declares the same flag identifier
// twice — a collision the parser would resolve silently (last/first wins), masking the
// author's intent. Each flag's effective identifiers are its declared ones, or the
// auto-derived "--<name>" when it declares none, so both explicit ("-o" on two flags)
// and derived (two flags whose names both yield "--out") collisions are caught.
func lintDuplicateFlagIdentifiers(spec *Spec) []error {
	var problems []error
	walkCommands(spec, func(c *Command, path string) {
		if c.Inputs == nil {
			return
		}
		claimedBy := map[string]string{} // identifier -> the flag name that first claimed it
		for _, f := range c.Inputs.Flags {
			// flagIdentifiers is the same derivation codegen emits into the
			// Definition, so the lint catches exactly the collisions the parser
			// would resolve silently — including derived ones ("dry_run" and
			// "dry-run" both yield "--dry-run").
			for _, id := range flagIdentifiers(f) {
				if prev, dup := claimedBy[id]; dup {
					problems = append(problems, &problem{
						kind: "spec",
						loc:  "command " + path,
						msg:  fmt.Sprintf("flag identifier %q is declared by both %q and %q", id, prev, f.Name),
					})
					continue
				}
				claimedBy[id] = f.Name
			}
		}
	})
	return problems
}

// lintSchemaRefs rejects an intra-document "$ref": "#/schemas/X" that points to a schema
// the document doesn't declare — which would otherwise become an "undefined type X"
// compile error in the generated code instead of a clear spec error. It walks every
// place a ref can appear (each command's flag/argument/env/config/stdin input schemas and
// output schema, plus the document-level schemas themselves, recursing into object
// properties and array items) and offers the closest declared schema name as a suggestion.
func lintSchemaRefs(spec *Spec) []error {
	declared := map[string]bool{}
	names := make([]string, 0, len(spec.Schemas))
	for name := range spec.Schemas {
		declared[name] = true
		names = append(names, name)
	}
	var problems []error
	reported := map[string]bool{}
	checkAt := func(loc string) func(BaseSchema) {
		return func(b BaseSchema) {
			name := refTypeName(b.Ref)
			if name == "" || declared[name] {
				return
			}
			key := name + "\x00" + loc
			if reported[key] {
				return
			}
			reported[key] = true
			msg := fmt.Sprintf("$ref %q points to an undeclared schema (no %q under the document-level \"schemas\")", b.Ref, name)
			problems = append(problems, &problem{kind: "spec", loc: loc, msg: didYouMean(msg, name, names)})
		}
	}
	walkCommands(spec, func(c *Command, path string) {
		eachInputSchema(c.Inputs, func(channel, name string, s *InputSchema) {
			if s == nil {
				return
			}
			loc := "command " + path + " " + channel
			if name != "" {
				loc += " " + name
			}
			walkSchemaRefs(s.BaseSchema, checkAt(loc))
		})
		if c.Output != nil {
			walkSchemaRefs(c.Output.BaseSchema, checkAt("command "+path+" output"))
		}
	})
	for name := range spec.Schemas {
		s := spec.Schemas[name]
		walkSchemaRefs(s.BaseSchema, checkAt("schema "+name))
	}
	return problems
}

// lintHandlerFilenames rejects collisions and malformed overrides in the per-command
// handler-stub file names. Codegen writes one stub .go per own (inline) command into
// the cli package, named by commandStubFilename — a `filename` override when set, else
// a path-derived, reserved-name-escaped default. Two commands resolving to the same
// file would have codegen write one over the other; an override that is not a bare
// "*.go" name, or that is itself a name the go tool reads specially, would silently
// break the build. Composed ($ref) commands generate no stub here and are skipped — the
// walk mirrors the generator's own-command derivation so the two agree.
func lintHandlerFilenames(spec *Spec) []error {
	rootName := spec.Command.Name
	var problems []error
	byFile := map[string]string{} // stub file name -> the command path that first produced it
	var walk func(c *Command, path, display string)
	walk = func(c *Command, path, display string) {
		if ov := c.Filename; ov != "" {
			switch {
			case strings.ContainsAny(ov, `/\`):
				problems = append(problems, &problem{kind: "spec", loc: "command " + display, msg: fmt.Sprintf("filename %q must be a bare file name with no directory", ov)})
			case !strings.HasSuffix(ov, ".go"):
				problems = append(problems, &problem{kind: "spec", loc: "command " + display, msg: fmt.Sprintf("filename %q must end in \".go\"", ov)})
			case reservedTrailingToken(strings.TrimSuffix(ov, ".go")):
				problems = append(problems, &problem{kind: "spec", loc: "command " + display, msg: fmt.Sprintf("filename %q would be read specially by the go tool (a _test.go test file, or a GOOS/GOARCH build constraint) — choose another name", ov)})
			}
		}
		fn := commandStubFilename(rootName, path, c.Filename)
		if prev, dup := byFile[fn]; dup {
			problems = append(problems, &problem{kind: "spec", loc: "command " + display, msg: fmt.Sprintf("generates handler file %q, already used by command %q", fn, prev)})
		} else {
			byFile[fn] = display
		}
		for i := range c.Commands {
			child := &c.Commands[i]
			if child.Ref != "" {
				continue // composed: its stub lives in the child's package
			}
			childPath := child.Name
			if path != "" {
				childPath = path + "_" + child.Name
			}
			walk(child, childPath, display+"/"+child.Name)
		}
	}
	display := rootName
	if display == "" {
		display = "(root)"
	}
	walk(&spec.Command, "", display)
	return problems
}

// walkSchemaRefs invokes visit for a schema and recurses into its object properties and
// array items, so a "$ref" at any nesting depth is seen.
func walkSchemaRefs(b BaseSchema, visit func(BaseSchema)) {
	visit(b)
	for _, p := range b.Properties {
		walkSchemaRefs(p.BaseSchema, visit)
	}
	if b.Items != nil {
		walkSchemaRefs(b.Items.BaseSchema, visit)
	}
}

// walkSchemaImports records (type, import) for a schema and recurses into its object
// properties and array items.
func walkSchemaImports(b BaseSchema, record func(typ, imp string)) {
	record(b.Type, b.Import)
	for _, p := range b.Properties {
		walkSchemaImports(p.BaseSchema, record)
	}
	if b.Items != nil {
		walkSchemaImports(b.Items.BaseSchema, record)
	}
}

// levenshtein is the edit distance between a and b (Wagner–Fischer).
func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// closestName returns the candidate within edit distance 2 of target (the nearest typo
// fix), or "" when none is close enough.
func closestName(target string, candidates []string) string {
	best, bestDist := "", 3
	for _, c := range candidates {
		if d := levenshtein(target, c); d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
}

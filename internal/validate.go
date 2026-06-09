package internal

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-rotini/jsonschema"
	"github.com/go-rotini/rotini"
)

// errSpecPathRequired is reported by [Validate] when no spec-file path is
// supplied.
var errSpecPathRequired = errors.New("spec file path is required")

// The embedded rotini JSON Schemas are immutable, so each is compiled at
// most once per process and the result cached.
var (
	specSchemaOnce sync.Once
	specSchema     *jsonschema.Schema
	errSpecSchema  error

	confSchemaOnce sync.Once
	confSchema     *jsonschema.Schema
	errConfSchema  error
)

// problem is a single schema-validation failure: the location of the
// offending value within the document and a human-readable message, tagged
// by document kind ("spec" or "conf").
type problem struct {
	kind string
	loc  string
	msg  string
}

func (e *problem) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.kind, e.loc, e.msg)
}

// loadSpecSchema compiles the embedded spec JSON Schema once and returns
// the cached result.
func loadSpecSchema() (*jsonschema.Schema, error) {
	specSchemaOnce.Do(func() {
		s, err := jsonschema.Compile(rotini.SchemaSpec)
		if err != nil {
			errSpecSchema = fmt.Errorf("compile spec schema: %w", err)
			return
		}
		specSchema = s
	})
	return specSchema, errSpecSchema
}

// loadConfSchema compiles the embedded conf JSON Schema once and returns
// the cached result.
func loadConfSchema() (*jsonschema.Schema, error) {
	confSchemaOnce.Do(func() {
		s, err := jsonschema.Compile(rotini.SchemaConf)
		if err != nil {
			errConfSchema = fmt.Errorf("compile conf schema: %w", err)
			return
		}
		confSchema = s
	})
	return confSchema, errConfSchema
}

// ValidateFn is the signature of [Validate]. A command handler can bind it under a registry
// key and fetch it as an injectable service, so tests substitute a double (see [GenerateFn]).
type ValidateFn = func(specPath, confPath string, watch bool, failMode, version string, onValidate func(result string, err error)) error

// Validate is the convenience entry over [Validator] (the DI-bound [ValidateFn]): it builds a
// Validator and runs it. It mirrors [Generate].
//
// specPath is the spec-file command argument and is required. confPath is the -c/--config flag
// value (empty → the .rotini.conf.* discovered next to the spec). failMode is the --fail flag
// value ("fast" stops at the first problem, anything else collects); when empty it falls back to
// the module conf's validate.fail, so the flag overrides the conf. version is the running rotini
// binary's version string ("vX.Y.Z" / "v0.0.0") for the $schema guard ("" → skipped).
//
// onValidate, which may be nil, is called after each pass with a "[HH:MM:SS] <took>" summary and
// that pass's error (nil when the spec and conf are valid); Validate prints nothing itself, so the
// caller reports results through it. When watch is false it runs a single pass and returns that
// pass's error — every problem aggregated via [errors.Join] (or the first in "fast" mode). When
// watch is true it validates once and then re-validates whenever the spec or conf changes, until
// interrupted with ctrl-c (SIGINT); there every pass goes to onValidate and watching continues,
// and only a failure to start watching is returned.
func Validate(specPath, confPath string, watch bool, failMode, version string, onValidate func(result string, err error)) error {
	return NewValidator(specPath, confPath, version).
		WithFailMode(failMode).
		WithOnValidate(onValidate).
		Run(watch)
}

// Validator validates a rotini spec and its optional conf. Build one with [NewValidator], tune it
// with the With* options, then [Validator.Run] it. Fail mode defaults to "collect" and the
// onValidate callback to a no-op. A Validator runs one pass at a time and is not safe for
// concurrent Run calls.
type Validator struct {
	specPath   string
	confPath   string
	version    string // binary version for the $schema guard; "" → guard skipped
	failMode   string // "" → resolved from the conf's validate.fail, then "collect"
	onValidate func(result string, err error)

	// per-pass scratch, reset at the start of each pass:
	fast     bool
	problems []error
}

// NewValidator constructs a Validator for the spec at specPath, the conf at confPath (empty → the
// .rotini.conf.* discovered beside the spec), and version — the running binary's version string for
// the $schema guard ("" → guard skipped). Fail mode defaults to "collect"; onValidate defaults to a
// no-op satisfying the callback signature.
func NewValidator(specPath, confPath, version string) *Validator {
	return &Validator{
		specPath:   specPath,
		confPath:   confPath,
		version:    version,
		onValidate: func(string, error) {},
	}
}

// WithFailMode sets how problems are reported: "fast" returns the first; anything else collects
// them all (the default). An empty mode falls back to the module conf's validate.fail, then
// "collect" — so the --fail flag overrides the conf.
func (v *Validator) WithFailMode(mode string) *Validator {
	v.failMode = mode
	return v
}

// WithOnValidate sets the per-pass callback (a "[HH:MM:SS] <took>" summary + the pass error). A nil
// fn is ignored, leaving the no-op default.
func (v *Validator) WithOnValidate(fn func(result string, err error)) *Validator {
	if fn != nil {
		v.onValidate = fn
	}
	return v
}

// Run executes the validation: a single pass when watch is false (returning that pass's aggregated
// error — or the first problem in fast mode, or nil when valid), or a watch loop when true
// (re-validating on spec/conf change, routing every pass to the onValidate callback until
// interrupted). Only a failure to start watching is returned in watch mode.
func (v *Validator) Run(watch bool) error {
	v.confPath = resolveConfPath(v.specPath, v.confPath)
	return runOrWatch(v.specPath, v.confPath, watch, v.timedPass, v.onValidate)
}

// timedPass runs one pass and stamps it with the "[HH:MM:SS] <took>" summary, mirroring
// generateTimed so watch mode can report both when a pass ran and how long it took.
func (v *Validator) timedPass() (string, error) {
	start := time.Now()
	err := v.pass()
	return fmt.Sprintf("[%s] %s", start.Format("15:04:05"), roundDuration(time.Since(start))), err
}

// pass runs one validation pass against a fresh accumulator: the spec first, then the conf (reached
// only when the spec produced no problem in fast mode). It returns every problem aggregated via
// [errors.Join], or the first when fail mode resolves to "fast", or nil when valid.
func (v *Validator) pass() error {
	v.problems = nil
	v.fast = resolveFailMode(v.failMode) == "fast"
	v.checkSpec()
	if v.stop() {
		return v.result()
	}
	v.checkConf()
	return v.result()
}

// checkSpec schema-validates the spec, then — only when it is schema-valid — runs the lints and the
// $schema-version guard. Linting a malformed doc is meaningless and would pile errors on an
// already-broken file, so a schema violation short-circuits.
func (v *Validator) checkSpec() {
	if v.specPath == "" {
		v.add(errSpecPathRequired)
		return
	}
	if problems := validateDocument(v.specPath, "spec", loadSpecSchema); len(problems) > 0 {
		v.add(problems...)
		return
	}
	spec, err := readSpec(v.specPath)
	if err != nil {
		return
	}
	v.lintSpec(spec)
	v.addErr(checkSchemaVersion("spec", spec.Schema, v.version))
}

// checkConf schema-validates the conf when one is resolved, then guards its $schema version. The
// conf is optional — an empty path is skipped.
func (v *Validator) checkConf() {
	if v.confPath == "" {
		return
	}
	if problems := validateDocument(v.confPath, "conf", loadConfSchema); len(problems) > 0 {
		v.add(problems...)
		return
	}
	if conf, err := readConf(v.confPath); err == nil {
		v.addErr(checkSchemaVersion("conf", conf.Schema, v.version))
	}
}

// lintSpec runs every registered spec lint, in order, accumulating their problems.
func (v *Validator) lintSpec(spec *Spec) {
	for _, lint := range specLints {
		v.add(lint(spec)...)
	}
}

func (v *Validator) add(errs ...error) { v.problems = append(v.problems, errs...) }

func (v *Validator) addErr(err error) {
	if err != nil {
		v.problems = append(v.problems, err)
	}
}

func (v *Validator) stop() bool { return v.fast && len(v.problems) > 0 }

func (v *Validator) result() error {
	if v.fast && len(v.problems) > 0 {
		return v.problems[0]
	}
	return errors.Join(v.problems...)
}

// specLints is the ordered set of spec lints run after the spec is schema-valid. Adding a lint is a
// one-line append here; each stays a pure func(*Spec) []error for isolated testing. The order is
// observable (collect mode joins problems in order), so keep it stable.
var specLints = []func(*Spec) []error{
	lintImportConsistency,
	lintLocalTimeout,
	lintFlagGroups,
	lintFlagDependencies,
	lintDuplicateFlagIdentifiers,
	lintSchemaRefs,
}

// validateOnce runs a single validation pass against an already-resolved conf path (it does NOT
// re-discover a conf beside the spec — the caller, generate, has already resolved and existence-
// checked it). It is the internal single-pass seam the codegen path and the tests use.
func validateOnce(specPath, confPath, failMode, version string) error {
	return NewValidator(specPath, confPath, version).WithFailMode(failMode).pass()
}

// rotiniSchemaURLRe matches the recognized rotini `$schema` URL form and captures
// the X.Y.Z version segment. A URL that doesn't match (absent, a branch ref, a
// different host — i.e. the user deliberately pointed it elsewhere) yields no
// capture, and checkSchemaVersion skips it.
var rotiniSchemaURLRe = regexp.MustCompile(`^https://raw\.githubusercontent\.com/go-rotini/rotini/refs/tags/([0-9]+\.[0-9]+\.[0-9]+)/schema-(?:spec|conf)\.json$`)

// checkSchemaVersion enforces that a document's `$schema` targets the same rotini
// release as the running binary. version is the binary's bound version string
// ("vX.Y.Z" or "v0.0.0"); its leading "v" is stripped to get the "X.Y.Z" segment
// compared against the document's `$schema` version. The check is skipped when the
// version is empty/unknown, or when the document's `$schema` is absent or not the
// recognized rotini refs/tags/<VER> form. A present, recognized, mismatched
// `$schema` is a validation error.
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

// resolveFailMode resolves the validate failure-reporting mode: an explicit value
// (the --fail flag) wins; otherwise the module-root conf's validate.fail is used,
// defaulting to "collect". Only "fast" enables fast mode; anything else collects.
func resolveFailMode(failMode string) string {
	if failMode != "" {
		return failMode
	}
	root, _, err := findModule()
	if err != nil {
		return "collect"
	}
	confPath, err := discoverFile(root, ".rotini.conf.")
	if err != nil {
		return "collect"
	}
	conf, err := readConf(confPath)
	if err != nil || conf.Validate == nil {
		return "collect"
	}
	return conf.Validate.Fail
}

// validateDocument reads the document at path, compiles its schema, and
// returns one error per problem: a read/convert failure, a schema-compile
// failure, or one [*problem] per schema violation. It returns nil
// when the document is valid.
func validateDocument(path, kind string, loadSchema func() (*jsonschema.Schema, error)) []error {
	instance, err := toJSON(path)
	if err != nil {
		return []error{fmt.Errorf("%s file: %w", kind, err)}
	}
	schema, err := loadSchema()
	if err != nil {
		return []error{err}
	}
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

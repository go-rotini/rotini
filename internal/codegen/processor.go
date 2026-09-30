package codegen

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-rotini/jsonschema"
)

// Processor is the rotini controller, holding the immutable per-process config: the running
// binary version, so a spec can be checked to target this rotini, and the compiled embedded
// JSON Schemas. It exposes Generate, Validate and Initialize, each running the same pipeline:
//
//	reconcile (read + decode)  →  validate (version + schema)  →  lint (rotini rules)  →  generate
//
// The reconcile stage returns values that flow through the later stages, so the Processor
// itself stays immutable and one instance safely drives many passes — watch mode re-runs the
// pipeline on every change.
type Processor struct {
	version    string             // running binary version ("X.Y.Z", a leading v tolerated; "" → version check skipped)
	specSchema *jsonschema.Schema // compiled embedded spec JSON Schema
	confSchema *jsonschema.Schema // compiled embedded conf JSON Schema
}

// NewProcessor returns a Processor tagged with the running binary's version string. It
// compiles rotini's embedded spec + conf JSON Schemas once (the "valid CLI" contract the
// validate stage checks the end-user's files against); a compile failure is a rotini
// packaging bug, never user input, so it panics rather than surfacing a user-facing error.
func NewProcessor(version string) *Processor {
	specSchema, err := loadSpecSchema()
	if err != nil {
		panic(err)
	}
	confSchema, err := loadConfSchema()
	if err != nil {
		panic(err)
	}
	return &Processor{
		version:    version,
		specSchema: specSchema,
		confSchema: confSchema,
	}
}

// Generate runs the generate workflow: reconcile → validate (the gate) → emit the
// program — once, or on every spec/conf change in watch mode until interrupted (ctrl-c).
// onGenerate (may be nil) receives a "[HH:MM:SS] <took>" summary and each pass's error;
// without watch the single pass's error is returned so the caller can treat it as failed.
// onNotices (may be nil) receives the pass's non-fatal remarks — mirroring Validate's warnings
// channel: what it removed, because a file deleted without a word is how work gets lost, and
// what the hook audit noticed in the handler files it did not write.
func (p *Processor) Generate(specPath, confPath string, watch bool, onGenerate func(result string, err error), onNotices func(notices []error)) error {
	if onGenerate == nil {
		onGenerate = func(string, error) {}
	}
	pass := func(specPath, confPath string) (warnings []error, err error) {
		rs, rc, err := p.reconcile(specPath, confPath)
		if err != nil {
			return nil, err
		}
		return p.validateAndEmit(rs, rc)
	}
	return p.run(specPath, confPath, watch, pass, onGenerate, onNotices)
}

// Validate runs the validate workflow: reconcile → validate + lint, once or on every
// change (watch). failMode is the --fail override ("fast"/"collect"; "" → the conf's
// validate.fail). onValidate (may be nil) receives a summary and each pass's error;
// onWarnings (may be nil) receives every pass's non-fatal warnings.
func (p *Processor) Validate(specPath, confPath string, watch bool, failMode string, onValidate func(result string, err error), onWarnings func(warnings []error)) error {
	if onValidate == nil {
		onValidate = func(string, error) {}
	}
	pass := func(specPath, confPath string) (warnings []error, err error) {
		rs, rc, err := p.reconcile(specPath, confPath)
		if err != nil {
			return nil, err
		}
		return p.validateDocuments(rs, rc, failMode)
	}
	return p.run(specPath, confPath, watch, pass, onValidate, onWarnings)
}

// Initialize scaffolds a new rotini CLI named name: it writes + validates the seed
// spec + conf, then runs the standard generate to produce a ready-to-build CLI (see
// initialize). format selects the serialization; force overwrites create-once files.
func (p *Processor) Initialize(name, format string, force bool) error {
	return p.initialize(name, format, force)
}

// ─── the staged pipeline ───────────────────────────────────────────────────────.

// reconcile reads + decodes both documents: the spec is REQUIRED (reconcileSpec errors
// on a missing one), the conf is OPTIONAL (reconcileConf yields the default shape when
// absent). The conf is resolved beside the (now-known) spec path.
func (p *Processor) reconcile(specPath, confPath string) (*reconciledSpec, *reconciledConf, error) {
	rs, err := reconcileSpec(specPath)
	if err != nil {
		return nil, nil, p.explainDecodeFailure("spec", err)
	}
	rc, err := reconcileConf(rs.path, confPath)
	if err != nil {
		return nil, nil, p.explainDecodeFailure("conf", err)
	}
	return rs, rc, nil
}

// explainDecodeFailure turns a document that would not decode into the schema validator's
// account of why, so a mistyped value is reported like every other mistake: every problem at
// once, each with a file:line:col and a JSON pointer.
//
// The Go decode runs first and stops at its first type mismatch, before validation ever sees
// the document — so without this, `minimum: "five"` produced a raw decoder message with no
// column and no key path, and a second mistake further down took another round trip to find.
//
// The decoder's own error is kept for the one case it is genuinely right about: the schema
// accepts the document but the Go types reject it. That is a disagreement between rotini's
// schema and rotini's types — a rotini bug — and it says so.
func (p *Processor) explainDecodeFailure(kind string, err error) error {
	var de *decodeError
	if !errors.As(err, &de) {
		return err
	}
	schema := p.specSchema
	if kind == "conf" {
		schema = p.confSchema
	}
	instance, convErr := bytesToJSON(de.format, de.data)
	if convErr != nil || schema == nil {
		return err
	}
	problems := validateInstance(kind, instance, schema)
	if kind == "spec" {
		problems = append(problems, schemaBlockProblems(instance)...)
	}
	if len(problems) == 0 {
		return fmt.Errorf("%w — the %s schema accepts this document but rotini's types reject it, which is a rotini bug; please report it", err, kind)
	}
	locateProblems(problems, de.path, newSourceLocator(de.format, de.data))
	return errors.Join(problems...)
}

// validateAndLintSpec schema-validates the spec, then — only when it is schema-valid (the lint
// rules assume a valid shape) — lints it. It returns every problem found.
func (p *Processor) validateAndLintSpec(rs *reconciledSpec) []error {
	problems := p.validateAndLintOne(rs)
	// A composed child is part of this program, so it is judged with it. Before, a parent
	// validated clean over a child holding an unknown key and an unparseable default: the child
	// was checked for cycles and collisions only, unless someone validated it on its own.
	return append(problems, p.validateComposedSpecs(rs, map[string]bool{})...)
}

// validateAndLintOne is the schema gate then the lint rules, for one spec document.
func (p *Processor) validateAndLintOne(rs *reconciledSpec) []error {
	if problems := p.validateSpec(rs); len(problems) > 0 {
		return problems
	}
	return p.lintSpec(rs)
}

// validateComposedSpecs validates every LOCAL spec rs composes with `$ref`, transitively, exactly
// as `rotini validate <child>` would — each problem positioned in the child's own file. A mod://
// child belongs to another module and was validated by its author; a git::/https:// ref is
// refused at composition. A spec that does not read at all is left to composition, which
// reports it where the $ref sits.
func (p *Processor) validateComposedSpecs(rs *reconciledSpec, seen map[string]bool) []error {
	if rs == nil || rs.spec == nil {
		return nil
	}
	seen[rs.path] = true
	var problems []error
	base := filepath.Dir(rs.path)
	for _, ref := range localSpecRefs(&rs.spec.Command) {
		locator, err := locateRef(base, ref)
		if err != nil || seen[locator] {
			continue
		}
		seen[locator] = true
		child, err := reconcileSpec(displayPath(locator))
		if err != nil {
			continue
		}
		problems = append(problems, p.validateAndLintOne(child)...)
		problems = append(problems, p.validateComposedSpecs(child, seen)...)
	}
	return problems
}

// localSpecRefs lists the local spec files a command tree composes directly.
func localSpecRefs(c *Command) []string {
	var refs []string
	for i := range c.Commands {
		sub := &c.Commands[i]
		if sub.Ref != "" && !strings.HasPrefix(sub.Ref, modScheme) && !isExternalLocator(sub.Ref) {
			refs = append(refs, sub.Ref)
		}
		refs = append(refs, localSpecRefs(sub)...)
	}
	return refs
}

// displayPath shortens an absolute path to one relative to the working directory, for messages.
func displayPath(abs string) string {
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, abs); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return abs
}

// validateAndLintConf is validateAndLintSpec for the conf.
func (p *Processor) validateAndLintConf(rc *reconciledConf) []error {
	if problems := p.validateConf(rc); len(problems) > 0 {
		return problems
	}
	return p.lintConf(rc)
}

// validateDocuments runs the full validate pass over both documents, honoring fast vs
// collect, returning the pass's non-fatal warnings and a joined fatal error (nil → all
// valid). In fast mode it stops at the first failing document.
func (p *Processor) validateDocuments(rs *reconciledSpec, rc *reconciledConf, failMode string) (warnings []error, err error) {
	fast := failFast(failMode, rc)

	specErrs, specWarns := splitProblems(p.validateAndLintSpec(rs))
	warnings = append(warnings, specWarns...)
	if fast && len(specErrs) > 0 {
		return warnings, specErrs[0]
	}

	confErrs, confWarns := splitProblems(p.validateAndLintConf(rc))
	warnings = append(warnings, confWarns...)
	if fast && len(confErrs) > 0 {
		return warnings, confErrs[0]
	}

	return warnings, errors.Join(append(specErrs, confErrs...)...)
}

// validateAndEmit is the gate-then-emit step: validation must pass (the gate — invalid input
// never reaches codegen), then the conf defaults are applied and the program emitted. It
// returns any NOTICES the emit produced: the orphaned stubs it pruned, which the caller reports
// rather than deleting them silently, and what the hook audit found in the handler files it
// did not write.
func (p *Processor) validateAndEmit(rs *reconciledSpec, rc *reconciledConf) ([]error, error) {
	if _, err := p.validateDocuments(rs, rc, ""); err != nil {
		return nil, err
	}
	applyConfDefaults(rc.conf, rs.spec.Command.Name)
	prog, err := resolveProgram(rs.spec, rc.conf, rs.path)
	if err != nil {
		return nil, err
	}
	err = prog.generate()
	notices := make([]error, 0, len(prog.pruned)+len(prog.auditWarnings))
	for _, name := range prog.pruned {
		notices = append(notices, fmt.Errorf("pruned %s — its command is no longer in the spec", name))
	}
	notices = append(notices, prog.auditWarnings...)
	return notices, err
}

// failFast reports whether validation stops at the first problem: the --fail override
// (failMode) wins, else the reconciled conf's validate.fail. Only "fast" enables it —
// anything else collects every problem (the default).
func failFast(failMode string, rc *reconciledConf) bool {
	mode := failMode
	if mode == "" && rc != nil && rc.conf != nil && rc.conf.Validate != nil {
		mode = rc.conf.Validate.Fail
	}
	return mode == "fast"
}

// ─── the run/watch engine ──────────────────────────────────────────────────────.

// run resolves the spec and conf paths up front, so watch watches exactly the files read, then
// drives the shared run/watch engine. Each pass reconciles and processes the files fresh, so
// edits are picked up, stamped with a summary handed to onResult.
func (p *Processor) run(specPath, confPath string, watch bool, pass func(specPath, confPath string) (warnings []error, err error), onResult func(result string, err error), onWarnings func([]error)) error {
	resolvedSpec, err := resolveSpecPath(specPath)
	if err != nil {
		return err
	}
	if resolvedSpec == "" {
		return errSpecPathRequired
	}
	resolvedConf := resolveConfBesideSpec(resolvedSpec, confPath)

	timed := func() (string, error) {
		start := time.Now()
		warnings, err := pass(resolvedSpec, resolvedConf)
		// Surface warnings on every pass (success or failure), independent of the
		// pass/fail result onResult carries.
		if onWarnings != nil && len(warnings) > 0 {
			onWarnings(warnings)
		}
		return fmt.Sprintf("[%s] %s", start.Format("15:04:05"), roundDuration(time.Since(start))), err
	}
	return runOrWatch(resolvedSpec, resolvedConf, watch, timed, onResult)
}

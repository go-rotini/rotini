package codegen

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-rotini/jsonschema"
)

// Processor drives rotini's pipeline for Generate, Validate and Initialize:
//
//	reconcile (read + decode)  →  validate (version + schema)  →  lint (rotini rules)  →  generate
//
// It holds only immutable state (the binary version and compiled schemas); per-pass values
// flow between stages, so one Processor can drive repeated watch-mode passes.
type Processor struct {
	version    string // running binary version; "" or unparseable skips the version check
	specSchema *jsonschema.Schema
	confSchema *jsonschema.Schema
}

// NewProcessor returns a Processor for the given binary version. It panics if the embedded
// schemas fail to compile, which is a rotini build defect rather than a user error.
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

// Generate reconciles, validates and emits the program, once or, with watch, on every spec
// or conf change until interrupted. onGenerate (optional) receives a "[HH:MM:SS] <took>"
// summary, preceded by one "created: <path>" line per file the pass created that the author
// owns from then on (handler stubs, main.go, editable templates), and each pass's error;
// without watch the pass's error is also returned. onNotices (optional) receives each pass's
// non-fatal findings: validation warnings, pruned files, and hook-audit warnings about
// handler files.
func (p *Processor) Generate(specPath, confPath string, watch bool, onGenerate func(result string, err error), onNotices func(notices []error)) error {
	if onGenerate == nil {
		onGenerate = func(string, error) {}
	}
	var created string
	pass := func(specPath, confPath string) (warnings []error, err error) {
		created = ""
		rs, rc, err := p.reconcile(specPath, confPath)
		if err != nil {
			return nil, err
		}
		pl := newPlanner(false)
		warnings, err = p.validateAndEmit(rs, rc, true, pl)
		created = pl.createdLines()
		return warnings, err
	}
	report := func(result string, err error) {
		if err == nil {
			result = created + result
		}
		onGenerate(result, err)
	}
	return p.run(specPath, confPath, watch, pass, report, onNotices)
}

// GenerateDryRunFn is the signature of [Processor.GenerateDryRun].
type GenerateDryRunFn = func(specPath, confPath string, onNotices func(notices []error)) (Planned, error)

// Planned reports a dry run: the timing line, and each change the run would make, one per
// line ("2. create internal/cmd/app/app_add.go (311 bytes, mode 0644)"). No changes means the
// files on disk are already what the spec generates.
type Planned struct {
	Result  string
	Changes []string
}

// GenerateDryRun reconciles, validates and plans the program exactly as [Processor.Generate]
// does, and writes nothing. onNotices (optional) receives validation and hook-audit warnings;
// a file the run would prune is a change, not a notice.
func (p *Processor) GenerateDryRun(specPath, confPath string, onNotices func(notices []error)) (Planned, error) {
	var planned Planned
	pl := newPlanner(true)
	pass := func(specPath, confPath string) (warnings []error, err error) {
		rs, rc, err := p.reconcile(specPath, confPath)
		if err != nil {
			return nil, err
		}
		return p.validateAndEmit(rs, rc, true, pl)
	}
	err := p.run(specPath, confPath, false, pass, func(result string, _ error) { planned.Result = result }, onNotices)
	planned.Changes = pl.Changes()
	return planned, err
}

// Validate reconciles, validates and lints both documents, once or, with watch, on every
// change. failMode overrides the conf's validate.fail ("fast" or "collect"; "" uses the conf).
// A non-empty release (X.Y.Z) also fails for each command, input or deprecated identifier
// whose removed_in is at or below it, in the spec and the local specs it composes.
// onValidate (optional) receives a summary and each pass's error; onWarnings (optional)
// receives each pass's non-fatal warnings.
func (p *Processor) Validate(specPath, confPath string, watch bool, failMode, release string, onValidate func(result string, err error), onWarnings func(warnings []error)) error {
	if onValidate == nil {
		onValidate = func(string, error) {}
	}
	if release != "" {
		if err := CheckRelease(release, "release"); err != nil {
			return err
		}
	}
	pass := func(specPath, confPath string) (warnings []error, err error) {
		rs, rc, err := p.reconcile(specPath, confPath)
		if err != nil {
			return nil, err
		}
		warnings, err = p.validateDocuments(rs, rc, failMode)
		if release == "" || (err != nil && failFast(failMode, rc)) {
			return warnings, err
		}
		due := releaseCheck(rs, release, map[string]bool{})
		if len(due) > 0 && failFast(failMode, rc) && err == nil {
			return warnings, due[0]
		}
		return warnings, errors.Join(append([]error{err}, due...)...)
	}
	return p.run(specPath, confPath, watch, pass, onValidate, onWarnings)
}

// Initialize scaffolds a new CLI named name: it writes the seed spec and conf in opt.Format
// (or, with opt.Template, every CLI that template declares), then validates and generates a
// ready-to-build program. opt.Force replaces an existing seed spec and conf. It never deletes
// files; stale handlers are pruned by the next generate.
func (p *Processor) Initialize(name string, opt InitOptions) (Initialized, error) {
	return p.initializeWith(name, opt, newPlanner(false))
}

// InitializeDryRun plans everything [Processor.Initialize] would write, and writes nothing.
// The result's Changes lists each file it would create or replace.
func (p *Processor) InitializeDryRun(name string, opt InitOptions) (Initialized, error) {
	return p.initializeWith(name, opt, newPlanner(true))
}

// initializeWith runs init through pl and reports what it wrote, or in a dry run would write.
func (p *Processor) initializeWith(name string, opt InitOptions, pl *planner) (Initialized, error) {
	start := time.Now()
	seeds, err := p.initialize(name, opt, pl)
	if err != nil {
		return Initialized{}, err
	}
	out := Initialized{
		Spec:   displayPath(seeds[0].Spec),
		Conf:   displayPath(seeds[0].Conf),
		Result: reportTiming(start),
	}
	for _, s := range seeds[1:] {
		out.Also = append(out.Also, SeedFiles{Spec: displayPath(s.Spec), Conf: displayPath(s.Conf)})
	}
	if pl.dry {
		out.Changes = pl.Changes()
	}
	return out, nil
}

// reconcile reads and decodes the required spec and the optional conf, resolving the conf
// beside the spec.
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

// explainDecodeFailure replaces a decode error with the schema validator's positioned
// problems for the same document, so every mistyped value is reported at once. If the schema
// accepts the document, the schema and Go types disagree, which is a rotini bug, and the
// decoder's error is returned saying so.
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
		return fmt.Errorf("%w; the %s schema accepts this document but rotini's types reject it, which is a rotini bug; please report it", err, kind)
	}
	locateProblems(problems, de.path, newSourceLocator(de.format, de.data))
	return errors.Join(problems...)
}

// validateAndLintSpec validates and lints the spec and every local spec it composes.
func (p *Processor) validateAndLintSpec(rs *reconciledSpec) []error {
	problems := p.validateAndLintOne(rs)
	return append(problems, p.validateComposedSpecs(rs, map[string]bool{})...)
}

// validateAndLintOne validates one spec document and, only if it is schema-valid, lints it.
func (p *Processor) validateAndLintOne(rs *reconciledSpec) []error {
	if problems := p.validateSpec(rs); len(problems) > 0 {
		return problems
	}
	return p.lintSpec(rs)
}

// validateComposedSpecs transitively validates and lints every local spec rs composes via
// `$ref`, positioning problems in each child's file. mod:// and remote refs are skipped, as is
// a child that fails to read (composition reports it at the $ref).
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
			// A repeated key is reported in the child's own file, like its other problems.
			if dup := (*duplicateKeysError)(nil); errors.As(err, &dup) {
				problems = append(problems, dup.problems...)
			}
			continue
		}
		problems = append(problems, p.validateAndLintOne(child)...)
		problems = append(problems, composedTopicsProblem(rs, child)...)
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

// validateDocuments validates and lints both documents, then runs cross-document rules if
// both passed. It returns the warnings and the joined errors; in fast mode it returns the
// first error only.
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

	var crossErrs []error
	if len(specErrs) == 0 && len(confErrs) == 0 {
		var crossWarns []error
		crossErrs, crossWarns = splitProblems(p.lintAcross(rs, rc))
		warnings = append(warnings, crossWarns...)
		if fast && len(crossErrs) > 0 {
			return warnings, crossErrs[0]
		}
	}

	return warnings, errors.Join(slices.Concat(specErrs, confErrs, crossErrs)...)
}

// validateAndEmit validates both documents and, only if they pass, applies conf defaults and
// emits the program. prune controls whether orphaned stubs are removed (init passes false).
// It returns notices: validation warnings, pruned files, and hook-audit warnings.
func (p *Processor) validateAndEmit(rs *reconciledSpec, rc *reconciledConf, prune bool, pl *planner) ([]error, error) {
	warnings, err := p.validateDocuments(rs, rc, "")
	if err != nil {
		return warnings, err
	}
	applyConfDefaults(rc.conf, rs.spec.Command.Name)
	prog, err := resolveProgram(rs.spec, rc.conf, rs.path, pl)
	if err != nil {
		return warnings, err
	}
	prog.skipPrune = !prune
	err = prog.generate()
	notices := make([]error, 0, len(warnings)+len(prog.pruned)+len(prog.auditWarnings))
	notices = append(notices, warnings...)
	if !pl.dry { // a dry run lists the removal as a change instead
		for _, name := range prog.pruned {
			notices = append(notices, fmt.Errorf("pruned %s; its command is no longer in the spec", name))
		}
	}
	for _, name := range prog.restored {
		notices = append(notices, fmt.Errorf("restored %s; its command is back in the spec", name))
	}
	notices = append(notices, prog.auditWarnings...)
	notices = append(notices, prog.agentNotices...)
	return notices, err
}

// failFast reports whether validation stops at the first problem: failMode if set, else the
// conf's validate.fail. Only "fast" enables it; the default collects every problem.
func failFast(failMode string, rc *reconciledConf) bool {
	mode := failMode
	if mode == "" && rc != nil && rc.conf != nil && rc.conf.Validate != nil {
		mode = rc.conf.Validate.Fail
	}
	return mode == "fast"
}

// run resolves the spec and conf paths once, so watch mode watches exactly the files read,
// then runs pass once or per change, reporting a timed summary to onResult.
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
		if onWarnings != nil && len(warnings) > 0 {
			onWarnings(warnings)
		}
		return reportTiming(start), err
	}
	return runOrWatch(resolvedSpec, resolvedConf, watch, timed, onResult)
}

// DryRunEnv returns the environment variable the conf beside specPath (or at confPath) names
// in generate.dry_run_env, or "" when it names none. A conf that can't be read names none;
// generate then reports the problem itself.
func DryRunEnv(specPath, confPath string) string {
	rc, err := reconcileConf(specPath, confPath)
	if err != nil || rc.conf.Generate == nil {
		return ""
	}
	return rc.conf.Generate.DryRunEnv
}

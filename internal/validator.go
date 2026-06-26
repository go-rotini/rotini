package internal

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/go-rotini/jsonschema"
	"github.com/go-rotini/rotini"
)

// This file owns the `rotini validate` operation end-to-end: the session/file-level
// validation that the Processor drives, plus the machinery (schema validation, the
// version guard, and the rotini-specific rules the JSON Schema can't express).

// ValidateFn is the signature of [Processor.Validate]. A command handler binds it
// under a registry key and fetches it as an injectable service, so tests substitute a
// double (see [GenerateFn]).
type ValidateFn = func(specPath, confPath string, watch bool, failMode string, onValidate func(result string, err error), onWarnings func(warnings []error)) error

// Validate is a convenience over [Processor.Validate]: it builds a Processor for
// version and runs the validate workflow. The companion handlers drive the Processor
// directly; this serves internal callers (tests).
func Validate(specPath, confPath string, watch bool, failMode, version string, onValidate func(result string, err error)) error {
	return NewProcessor(version).Validate(specPath, confPath, watch, failMode, onValidate, nil)
}

// ─── session + file validation ────────────────────────────────────────────────.

// validate runs the validator phase over the loaded spec and conf (call load first):
// each file validates itself. Problems are aggregated via errors.Join, or — in fast
// mode — the first is returned. It returns nil when both are valid.
func (s *session) validate() error {
	fast := s.failFast()

	specErrs, specWarns := splitProblems(s.spec.validate())
	s.warnings = append(s.warnings, specWarns...)
	if fast && len(specErrs) > 0 {
		return specErrs[0]
	}

	confErrs, confWarns := splitProblems(s.conf.validate())
	s.warnings = append(s.warnings, confWarns...)
	if fast && len(confErrs) > 0 {
		return confErrs[0]
	}

	return errors.Join(append(specErrs, confErrs...)...)
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
// the rotini-specific rules and enforces the version guard. It returns every
// problem found, empty when the spec is valid.
func (l *specLoader) validate() []error {
	if problems := validateInstance("spec", l.instance, l.schema); len(problems) > 0 {
		locateProblems(problems, l.path, l.locate)
		return problems
	}

	var problems []error
	for _, rule := range specLints {
		problems = append(problems, rule(l.spec)...)
	}
	locateProblems(problems, l.path, l.locate) // positions any pointer-shaped problems
	if err := checkSchemaVersion("spec", l.spec.Version, l.version); err != nil {
		problems = append(problems, err)
	}
	problems = append(problems, validateComposedTree(l.spec, l.path, l.version)...)
	return problems
}

// validateComposedTree is the deep `$ref` descend (W8/D-W8.3): when the spec composes
// child specs, run the generator's own composer (resolveTree) over the WHOLE tree so
// `rotini validate` catches problems that only emerge once refs are followed — name/
// alias collisions across composition boundaries, cyclic or missing refs, and each
// composed spec's version. It reuses generate's exact compose logic (no separate walk),
// so validate and generate cannot drift. Best-effort: it needs a module (composed
// commands resolve to import paths) and only matters when refs are present, so a
// ref-less spec or a module-less context is skipped — leaving per-spec validation as-is.
func validateComposedTree(spec *Spec, specPath, version string) []error {
	if !specHasRefs(spec) {
		return nil
	}
	root, name, err := findModule()
	if err != nil {
		return nil // no module: a composed CLI can't generate here anyway; not validate's error to raise
	}
	// The composer resolves refs + import paths relative to the CWD module; only run it
	// when the spec actually lives inside that module (else CWD ≠ the spec's project and
	// the relative refs/imports would be meaningless — leave it to a validate run from
	// the right place).
	absSpec, err1 := filepath.Abs(specPath)
	absRoot, err2 := filepath.Abs(root)
	if err1 != nil || err2 != nil ||
		(absSpec != absRoot && !strings.HasPrefix(absSpec, absRoot+string(filepath.Separator))) {
		return nil
	}
	if _, err := resolveTree(spec, specPath, root, name, version); err != nil {
		return []error{&problem{kind: "spec", loc: "composition", msg: err.Error()}}
	}
	return nil
}

// specHasRefs reports whether any command in the tree composes a child spec via $ref.
func specHasRefs(spec *Spec) bool {
	found := false
	walkCommands(spec, func(c *Command, _ string) {
		if c.Ref != "" {
			found = true
		}
	})
	return found
}

// validate schema-validates the conf against its compiled schema on the raw JSON
// instance when one was resolved, then — only when it is schema-valid — runs the
// rotini-specific conf rules and enforces the version guard. A default
// conf (no file) has nothing to validate. It returns every problem found.
func (l *confLoader) validate() []error {
	if l.path == "" {
		return nil
	}
	if problems := validateInstance("conf", l.instance, l.schema); len(problems) > 0 {
		locateProblems(problems, l.path, l.locate)
		return problems
	}

	var problems []error
	for _, rule := range confLints {
		problems = append(problems, rule(l.conf)...)
	}
	problems = append(problems, lintInitializeLocation(l.conf, l.path)...)
	if err := checkSchemaVersion("conf", l.conf.Version, l.version); err != nil {
		problems = append(problems, err)
	}
	return problems
}

// lintInitializeLocation rejects an `initialize` block in a conf that is not
// at the module root — `rotini initialize` reads its defaults ONLY from the
// module-root conf, so anywhere else the block is an accepted lie (the seed
// template used to plant one in every per-CLI conf; fidelity F6 removed it).
// It is the one location-aware conf rule, so it runs beside the confLints
// (which see only the decoded document, not its path). With no go.mod above
// the conf the check is undecidable and skipped (a standalone document).
func lintInitializeLocation(conf *Conf, confPath string) []error {
	if conf.Initialize == nil {
		return nil
	}
	abs, err := filepath.Abs(confPath)
	if err != nil {
		return nil
	}
	confDir := filepath.Dir(abs)
	root := confDir
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			return nil // no module above: undecidable, skip
		}
		root = parent
	}
	if root == confDir {
		return nil
	}
	rel, err := filepath.Rel(root, confDir)
	if err != nil {
		rel = confDir
	}
	return []error{&problem{
		kind: "conf",
		loc:  "initialize",
		msg: fmt.Sprintf("declared in %s but `rotini initialize` reads only the module-root conf — move this block to a .rotini.conf.* beside go.mod (or remove it)",
			filepath.ToSlash(rel)),
	}}
}

// ─── schema validation + the version guard ─────────────────────────────.

// severity classifies a validation problem. The zero value is an error (fails
// validation); a warning is surfaced separately but does NOT fail. The validate
// command routes the two to the funnel (as its errors and warnings respectively).
type severity int

const (
	severityError   severity = iota // zero value — fails validation
	severityWarning                 // advisory — surfaced, never fails
)

// problem is a single validation finding: the location of the offending value within
// the document and a human-readable message, tagged by document kind ("spec"/"conf")
// and severity (error by default; warning for non-fatal advisories).
type problem struct {
	kind  string
	loc   string
	pos   string // "path:line:col" in the original source; "" degrades to loc-only
	msg   string
	sev   severity // zero value = error
	cause error    // optional typed error this problem carries (e.g. *rotini.CompositionVersionError), reachable via errors.As
}

// splitProblems separates a finding list into fatal errors and non-fatal
// warnings by each finding's severity. A non-*problem error counts as an error.
func splitProblems(problems []error) (errs, warns []error) {
	for _, e := range problems {
		var p *problem
		if errors.As(e, &p) && p.sev == severityWarning {
			warns = append(warns, e)
			continue
		}
		errs = append(errs, e)
	}
	return errs, warns
}

func (e *problem) Error() string {
	if e.pos != "" {
		return fmt.Sprintf("%s: %s: %s: %s", e.kind, e.pos, e.loc, e.msg)
	}
	return fmt.Sprintf("%s: %s: %s", e.kind, e.loc, e.msg)
}

// Unwrap exposes an optional typed cause (e.g. a [rotini.CompositionVersionError]) so a
// caller's errors.As/Is reaches it through the aggregated validation error.
func (e *problem) Unwrap() error { return e.cause }

// locateProblems back-fills source positions onto pointer-shaped problems: a
// problem whose loc is a JSON-pointer instance location gains "path:line:col"
// when the document's locator can resolve it. Lint problems with semantic locs
// ("command app deploy") pass through untouched, as do all problems when the
// format carries no positions (TOML) — pointer-only is the documented degrade.
func locateProblems(problems []error, path string, locate sourceLocator) {
	if locate == nil || path == "" {
		return
	}
	for _, e := range problems {
		p := &problem{}
		ok := errors.As(e, &p)
		if !ok || !strings.HasPrefix(p.loc, "/") {
			continue
		}
		if line, col, ok := locate(p.loc); ok {
			p.pos = fmt.Sprintf("%s:%d:%d", path, line, col)
		}
	}
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

// checkSchemaVersion enforces that a document's top-level `version` targets the same
// rotini release as the running binary. version is the binary's bound version string
// ("vX.Y.Z" or "v0.0.0"); its leading "v" is stripped to the "X.Y.Z" segment compared
// against the document's `version`. The check is skipped when the binary version is
// empty/unknown, or when the document declares no `version` (must-match-if-present — the
// JSON Schema separately requires `version`, so a schema-valid document always carries
// one by the time this runs). A present, mismatched `version` is an error. (The optional
// `$schema` URL is editor-tooling only and is no longer consulted for this check.)
func checkSchemaVersion(kind, docVersion, version string) error {
	want := strings.TrimPrefix(version, "v")
	if want == "" {
		return nil
	}
	got := strings.TrimPrefix(docVersion, "v")
	if got == "" {
		return nil
	}
	if got != want {
		return &problem{
			kind: kind,
			loc:  "version",
			msg:  fmt.Sprintf("targets rotini version %s but this rotini is %s — update the version (or your rotini install) so they match", got, want),
		}
	}
	return nil
}

// checkComposedSchemaVersion is the STRICT cross-tree version guard for a COMPOSED
// spec (W8/D-W8.7). Unlike the entry-spec guard ([checkSchemaVersion], which is
// must-match-if-present), a composed spec MUST declare a `version` that EXACTLY matches
// the generating version: a missing or mismatched version is an error, because a
// composed tree must provably share one rotini version and you cannot confirm that
// without it. Skipped only when the running version is unknown (version == "", e.g.
// tests) — there is then nothing to match against. Errors carry the composed spec's
// ref so the failure points at the right file.
func checkComposedSchemaVersion(ref, docVersion, version string) error {
	want := strings.TrimPrefix(version, "v")
	if want == "" {
		return nil
	}
	got := strings.TrimPrefix(docVersion, "v")
	if got == "" {
		msg := fmt.Sprintf("composed spec %q must declare a version targeting %s — every spec in a composed tree must target this rotini version", ref, want)
		return composedVersionProblem(ref, want, "", msg)
	}
	if got != want {
		msg := fmt.Sprintf("composed spec %q targets version %s but this rotini is %s — every spec in a composed tree must target the same version", ref, got, want)
		return composedVersionProblem(ref, want, got, msg)
	}
	return nil
}

// composedVersionProblem wraps the spec-arm version mismatch as a validation [problem]
// that carries a typed [rotini.CompositionVersionError] (Arm = spec) — so the message
// reports as before while a caller can errors.As to the shared composition-version type
// (one type across the spec/package/binary arms; D-W9.4).
func composedVersionProblem(ref, want, got, msg string) *problem {
	return &problem{
		kind: "spec", loc: "version", msg: msg,
		cause: &rotini.CompositionVersionError{
			Arm: rotini.CompositionSpecArm, Subject: ref, Want: want, Got: got, Msg: msg,
		},
	}
}

// ─── the rotini-specific rules (what the JSON Schema can't express) ─────────────.

// confLints is the ordered set of conf rules run after the conf is schema-valid,
// mirroring specLints. Like the spec rules, they reject configuration that would
// be silently ignored or that generate would reject later — validate is the gate.
var confLints = []func(*Conf) []error{
	lintEntrypoint,
	lintFeatureDirs,
	lintFeatureKnobs,
}

// lintEntrypoint rejects a main block whose 'keep' would be silently ignored:
// keep only takes effect once the entrypoint is actually written and its
// directory pruned, and that happens only when 'file' is set. (The entrypoint's
// Go package is always 'main'; there is no package key to reconcile.)
func lintEntrypoint(conf *Conf) []error {
	if conf.Generate == nil || conf.Generate.Packages == nil || conf.Generate.Packages.Main == nil {
		return nil
	}
	ep := conf.Generate.Packages.Main
	if ep.File == "" && len(ep.Keep) > 0 {
		return []error{&problem{
			kind: "conf",
			loc:  "generate.packages.main.keep",
			msg:  "has no effect without generate.packages.main.file — the entrypoint is only written, and its directory pruned, when file is set",
		}}
	}
	return nil
}

// lintFeatureDirs rejects an enabled, EMBEDDING feature whose explicit
// embed_dir cannot resolve under an explicitly-set cmdgen package — //go:embed
// could never reach it, so generate would fail; validate is the gate. Only
// embed mode (embed: true) is checked: an inline feature writes no embedded
// file, and template_dir is never embedded (unconstrained). When either side
// is unset the defaults guarantee nesting (the default embed_dir is
// <cmdgen>/renders), so there is nothing to check.
func lintFeatureDirs(conf *Conf) []error {
	if conf.Generate == nil || conf.Generate.Features == nil ||
		conf.Generate.Packages == nil || conf.Generate.Packages.Cmdgen == nil ||
		conf.Generate.Packages.Cmdgen.File == "" {
		return nil
	}
	cmdgen := path.Dir(filepath.ToSlash(conf.Generate.Packages.Cmdgen.File))
	feats := conf.Generate.Features
	var problems []error
	check := func(name string, f *Feature) {
		if f == nil || !f.Enabled || !f.Embed || f.EmbedDir == "" {
			return
		}
		dir := path.Clean(filepath.ToSlash(f.EmbedDir))
		if dir != cmdgen && !strings.HasPrefix(dir, cmdgen+"/") {
			problems = append(problems, &problem{
				kind: "conf",
				loc:  "generate.features." + name + ".embed_dir",
				msg:  fmt.Sprintf("%q must resolve under the cmdgen package %q so //go:embed can reach it", f.EmbedDir, cmdgen),
			})
		}
	}
	check("help", feats.Help)
	check("man", feats.Man)
	check("markdown", feats.Markdown)
	check("completion", feats.Completion)
	return problems
}

// lintFeatureKnobs WARNS (non-fatal) when an ENABLED feature sets a directory knob
// its mode ignores — upholding rotini's no-silently-ignored-key principle without
// failing the build, since the override is inert rather than broken: an `embed_dir`
// without embed mode (inline content writes no embedded file), a `template_dir`
// without seeding a template, and — for completion, which has no editable template —
// `template`/`template_dir` at all. Disabled features are left alone (staged config).
// Warnings route to the funnel (as warnings); validation still passes.
func lintFeatureKnobs(conf *Conf) []error {
	if conf.Generate == nil || conf.Generate.Features == nil {
		return nil
	}
	var problems []error
	warn := func(name, key, msg string) {
		problems = append(problems, &problem{
			kind: "conf", loc: "generate.features." + name + "." + key,
			sev: severityWarning, msg: msg,
		})
	}
	for _, cf := range featureConfigs(conf) {
		f := cf.cfg
		if f == nil || !f.Enabled {
			continue
		}
		name := cf.desc.name
		if f.EmbedDir != "" && !f.Embed {
			warn(name, "embed_dir", "is set but embed is false — embed_dir is used only in embed mode (//go:embed); inline content writes no file, so it is ignored")
		}
		if cf.desc.tmplFile == "" { // no editable template (completion)
			if f.Template {
				warn(name, "template", name+" has no editable template — 'template' has no effect here")
			}
			if f.TemplateDir != "" {
				warn(name, "template_dir", name+" has no editable template — 'template_dir' has no effect here")
			}
			continue
		}
		if f.TemplateDir != "" && !f.Template {
			warn(name, "template_dir", "is set but template is false — template_dir is used only when the editable template is seeded (template: true); it is otherwise ignored")
		}
	}
	return problems
}

// specLints is the ordered set of spec rules run after the spec is schema-valid.
// Adding a rule is a one-line append here; each stays a pure func(*Spec) []error for
// isolated testing. The order is observable (collect mode joins problems in order), so
// keep it stable.
var specLints = []func(*Spec) []error{
	lintRootCommand,
	lintRootAliases,
	lintDocLevelKeys,
	lintRefNodeKeys,
	lintHandlerSource,
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
	lintRemoteDiscoveryVerify,
	lintDottedKeys,
	lintFrom,
	lintConfigurationFiles,
	lintConfigFilesScope,
	lintConfigSource,
	lintEnvNesting,
	lintConfigInputFiles,
	lintConstraintApplicability,
	lintCountFlags,
	lintPassthrough,
	lintPatternCompiles,
}

// lintRootCommand enforces what the shared Command shape can't: the top-level
// command is the binary itself, so it must carry a name and cannot be composed
// via $ref. Generate enforces the same rule — validate is the gate.
func lintRootCommand(spec *Spec) []error {
	var problems []error
	if spec.Command.Ref != "" {
		problems = append(problems, &problem{kind: "spec", loc: "(root)", msg: "the root command cannot use $ref — compose child specs as sub-commands instead"})
	}
	if spec.Command.Name == "" {
		problems = append(problems, &problem{kind: "spec", loc: "(root)", msg: "the root command must have a name (it is the binary name)"})
	}
	return problems
}

// lintDocLevelKeys rejects the two root-command-level keys — env_prefix, schemas —
// on a NON-root command. The shared Command shape accepts these keys on every node;
// they are meaningful only on the root command (under the document's `command` key),
// and codegen reads them only there. Declaring one deeper is a silent no-op, so
// validate rejects it. The root (spec.Command itself) is exempt. (The document-level
// `$schema`/`version` keys live on the document, not on Command, so they cannot appear
// on a sub-command and need no check here.)
func lintDocLevelKeys(spec *Spec) []error {
	var problems []error
	walkCommands(spec, func(c *Command, path string) {
		if c == &spec.Command {
			return // the root: these keys belong here
		}
		add := func(key string) {
			problems = append(problems, &problem{
				kind: "spec", loc: "command " + path,
				msg: fmt.Sprintf("sets %s, a root-command-level key valid only on the root command — remove it (codegen reads it only at the root, so here it is silently ignored)", key),
			})
		}
		if c.EnvPrefix != "" {
			add("env_prefix")
		}
		if c.Schemas != nil {
			add("schemas")
		}
	})
	return problems
}

// lintRefNodeKeys enforces the $ref OVERLAY model's reject set (W8 / D-W8.2): a
// `$ref` node composes a child command whose handler is generated against the CHILD's
// own inputs/output, so the parent cannot overlay handler-coupled keys on the `$ref`
// node — they would produce a parser/struct the delegated handler does not match. The
// generator honors only the overlay keys (name/aliases/summary/description/help/group/
// hidden/deprecated/…) and the additive `commands:`; every other key it cannot honor,
// so — per rotini's no-silent-ignore invariant — validation rejects it here. (Declare
// inputs/output/remotes in the child spec instead.)
func lintRefNodeKeys(spec *Spec) []error {
	var problems []error
	walkCommands(spec, func(c *Command, path string) {
		if c.Ref == "" {
			return
		}
		loc := "command " + path
		reject := func(key string) {
			problems = append(problems, &problem{
				kind: "spec", loc: loc,
				msg: fmt.Sprintf("sets %q on a $ref node — a composed command delegates to the child's handler (built against the child's own inputs/output), so %q cannot be overlaid here; declare it in the child spec instead", key, key),
			})
		}
		if len(c.Flags) > 0 {
			reject("flags")
		}
		if len(c.Arguments) > 0 {
			reject("arguments")
		}
		if len(c.Env) > 0 {
			reject("env")
		}
		if len(c.Config) > 0 {
			reject("config")
		}
		if len(c.ConfigFiles) > 0 {
			reject("config_files")
		}
		if c.Stdin != nil {
			reject("stdin")
		}
		if len(c.FlagGroups) > 0 {
			reject("flag_groups")
		}
		if len(c.FlagDependencies) > 0 {
			reject("flag_dependencies")
		}
		if c.Output != nil {
			reject("output")
		}
		if len(c.RemoteCommands) > 0 {
			reject("remote_commands")
		}
		if c.RemoteDiscovery != nil {
			reject("remote_discovery")
		}
		if c.Passthrough {
			reject("passthrough")
		}
	})
	return problems
}

// lintHandlerSource enforces where a `handler:` (W9 package-import passthrough) may
// appear. It is valid on any SUB-command: a `$ref` node (sourcing the handlers a
// composed external spec can't provide — required for git/raw, an override for
// local/mod://) OR an inline command (the own-types + delegated-handler hybrid where the
// command's structure/inputs are generated locally but its handler delegates to the
// package — D-W9.7). It is NOT valid on the ROOT command: the root is the binary itself
// and the generator builds its handler directly, with no delegation seam — so, per the
// no-silently-ignored-key invariant, reject it there rather than drop it.
func lintHandlerSource(spec *Spec) []error {
	var problems []error
	if spec.Command.Handler != nil {
		problems = append(problems, &problem{
			kind: "spec", loc: "(root)",
			msg: "sets handler: on the root command — handler: passthrough is supported on sub-commands only (a $ref node or an inline command), not the root",
		})
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
			kind: "spec", loc: "(root)",
			msg: "the root command cannot declare aliases — it is reached by invoking the binary, not by a routing token; declare aliases on sub-commands",
		})
	}
	if len(spec.Command.DeprecatedIdentifiers) > 0 {
		problems = append(problems, &problem{
			kind: "spec", loc: "(root)",
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
		if c.inputs() == nil {
			return
		}
		seen := map[string]string{} // channel+name -> first declaration
		eachInputSchema(c.inputs(), func(channel, name string, _ *InputSchema) {
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
		if c.inputs() == nil {
			return
		}
		for i, a := range c.inputs().Arguments {
			if i == len(c.inputs().Arguments)-1 {
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
		eachInputSchema(c.inputs(), func(channel, name string, schema *InputSchema) {
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

// lintConfigurationFiles enforces each config_files entry's location
// contract: exactly one of path/discover, and the discover strategies' own
// requirements (xdg needs app; walk-up has no app to ignore silently). Name
// uniqueness is chain-scoped and lives in lintConfigFilesScope.
func lintConfigurationFiles(spec *Spec) []error {
	var problems []error
	add := func(name, msg string) {
		problems = append(problems, &problem{kind: "spec", loc: "config_files " + name, msg: msg})
	}
	for _, cf := range allConfigFiles(spec) {
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
		eachInputSchema(c.inputs(), func(channel, name string, schema *InputSchema) {
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

// lintConfigFilesScope enforces the config_files name space and physical-file
// uniqueness ALONG A CHAIN, matching the runtime cascade (D-W3.1). Logical names
// are how file: pins and config_source target an entry, so a name must be unique
// within the cascade reaching a command: declaring it twice on one command, or
// re-declaring an ancestor's name, is an ERROR — the nearer would shadow the
// farther under nearest-wins, leaving the pin/source ambiguous. Two entries that
// resolve to the SAME physical file — within one command or across levels of the
// chain — are a non-fatal WARNING: the nearer silently shadows the farther, so
// declare the file once. Sibling chains are independent: reusing a name or file
// on a different branch is fine.
// ancestorConfigIndex summarizes the config_files an ancestor chain puts in scope:
// each logical name → the ancestor label that declared it, and each physical
// location key → a `"name" on label` description. Any match against these is a
// violation, so first-seen wins (the nearest ancestor isn't special for blame).
func ancestorConfigIndex(ancestors []*Command) (names, locs map[string]string) {
	names, locs = map[string]string{}, map[string]string{}
	for _, a := range ancestors {
		if a.inputs() == nil {
			continue
		}
		label := a.Name
		if label == "" {
			label = a.Ref
		}
		if label == "" {
			label = "(root)"
		}
		for _, cf := range a.inputs().ConfigFiles {
			if _, ok := names[cf.Name]; !ok {
				names[cf.Name] = label
			}
			if key := configFileLocationKey(cf); key != "" {
				if _, ok := locs[key]; !ok {
					locs[key] = fmt.Sprintf("%q on %s", cf.Name, label)
				}
			}
		}
	}
	return names, locs
}

func lintConfigFilesScope(spec *Spec) []error {
	var problems []error
	walkChains(spec, func(chain []*Command, path string) {
		cmd := chain[len(chain)-1]
		if cmd.inputs() == nil {
			return
		}
		loc := "command " + path
		ancestorName, ancestorLoc := ancestorConfigIndex(chain[:len(chain)-1])
		ownName := map[string]bool{}
		ownLoc := map[string]string{} // location key → first own entry name
		for _, cf := range cmd.inputs().ConfigFiles {
			switch {
			case ownName[cf.Name]:
				problems = append(problems, &problem{
					kind: "spec", loc: loc,
					msg: fmt.Sprintf("config_files %q is declared twice — logical names identify entries (file: pins, config_source) and must be unique", cf.Name),
				})
			case ancestorName[cf.Name] != "":
				problems = append(problems, &problem{
					kind: "spec", loc: loc,
					msg: fmt.Sprintf("config_files %q shadows the entry declared on ancestor %s — names cascade and must be unique along the chain (a file: pin or config_source would be ambiguous); rename one", cf.Name, ancestorName[cf.Name]),
				})
			}
			ownName[cf.Name] = true

			key := configFileLocationKey(cf)
			if key == "" {
				continue // no location: lintConfigurationFiles errors on that
			}
			switch {
			case ownLoc[key] != "":
				problems = append(problems, &problem{
					kind: "spec", loc: loc, sev: severityWarning,
					msg: fmt.Sprintf("config_files %q and %q resolve to the same file — the later shadows the earlier (nearest-wins); declare it once", ownLoc[key], cf.Name),
				})
			case ancestorLoc[key] != "":
				problems = append(problems, &problem{
					kind: "spec", loc: loc, sev: severityWarning,
					msg: fmt.Sprintf("config_files %q resolves to the same file as %s — the nearer shadows it (nearest-wins); declare it once", cf.Name, ancestorLoc[key]),
				})
			}
			if ownLoc[key] == "" {
				ownLoc[key] = cf.Name
			}
		}
	})
	return problems
}

// configFileLocationKey is a stable identity for a config file's physical
// location — its path, or its discover target (strategy|file|app) — used to spot
// duplicate declarations. "" when neither is set (a separate rule errors on that).
func configFileLocationKey(cf ConfigurationFile) string {
	if cf.Path != "" {
		return "path:" + cf.Path
	}
	if d := cf.Discover; d != nil {
		return "discover:" + d.Strategy + "|" + d.File + "|" + d.App
	}
	return ""
}

// lintConfigSource enforces config_source's contract: flag/env inputs only,
// string-typed, naming a config_files entry IN SCOPE (declared on the command or
// an ancestor — config_files cascade, D-W3.1), with at most one flag and one env
// input claiming any entry within a chain (a second claim would silently shadow
// the first). Claiming inputs cascade too, so a claim conflict spans the chain:
// an ancestor's flag and this command's flag both claiming one entry collide.
// Sibling chains are independent (a name reused on a different branch is its own
// entry), so claims are gathered per chain rather than globally.
func lintConfigSource(spec *Spec) []error {
	var problems []error
	walkChains(spec, func(chain []*Command, path string) {
		cmd := chain[len(chain)-1]
		declared := chainConfigNames(chain)
		loc := "command " + path
		claims := map[string]map[string]string{} // target → channel → claiming input
		// Seed with the ancestors' claims (cascade), so this command's own claims
		// collide with them. Each ancestor's internal conflicts are caught when
		// that ancestor is itself visited, so only first-wins is recorded here.
		for _, a := range chain[:len(chain)-1] {
			eachInputSchema(a.inputs(), func(channel, name string, schema *InputSchema) {
				if schema == nil || schema.ConfigSource == "" || (channel != "flag" && channel != "env") {
					return
				}
				if claims[schema.ConfigSource] == nil {
					claims[schema.ConfigSource] = map[string]string{}
				}
				if _, ok := claims[schema.ConfigSource][channel]; !ok {
					claims[schema.ConfigSource][channel] = name
				}
			})
		}
		eachInputSchema(cmd.inputs(), func(channel, name string, schema *InputSchema) {
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
					msg: fmt.Sprintf("%s %q names config_source %q, which is not a config_files entry in scope (declared on this command or an ancestor)", channel, name, target),
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

// constraintNumericFamily mirrors the runtime's range-checkable vocabulary
// (parser.go numericFamily): the full int/uint/float family plus the
// JSON-Schema aliases.
var constraintNumericFamily = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float32": true, "float64": true,
	"integer": true, "number": true,
}

// lintConstraintApplicability rejects a constraint declared on a type it can
// never check (production-readiness R1): numeric bounds on non-numeric types
// (notably duration/time and imported types — see the message), length/pattern
// on non-strings, item counts on non-collections. Before this rule, such
// declarations were accepted and silently ignored at parse time. For arrays
// the per-value constraints apply to the ELEMENT type, matching the runtime.
// The stdin channel is exempt: its schema validates the piped DOCUMENT with
// full JSON-Schema semantics, where every keyword is real.
func lintConstraintApplicability(spec *Spec) []error {
	var problems []error
	walkCommands(spec, func(c *Command, path string) {
		loc := "command " + path
		eachInputSchema(c.inputs(), func(channel, name string, schema *InputSchema) {
			if channel == "stdin" || schema == nil {
				return
			}
			typ := getSchemaType(schema)
			elem := strings.TrimPrefix(typ, "[]")
			add := func(msg string) {
				problems = append(problems, &problem{kind: "spec", loc: loc,
					msg: fmt.Sprintf("%s %q: %s", channel, name, msg)})
			}
			numericBounds := schema.Minimum != nil || schema.Maximum != nil ||
				schema.ExclusiveMinimum != nil || schema.ExclusiveMaximum != nil || schema.MultipleOf != nil
			if numericBounds && !constraintNumericFamily[elem] {
				hint := ""
				switch elem {
				case "duration", "time.Duration":
					hint = " (duration bounds are not supported — validate in the handler, or wrap the value in a TextUnmarshaler type that enforces the range)"
				}
				add(fmt.Sprintf("minimum/maximum/exclusiveMinimum/exclusiveMaximum/multipleOf apply to numeric types only, not %s — the bound would be silently ignored%s", typ, hint))
			}
			if (schema.MinLength != 0 || schema.MaxLength != 0 || schema.Pattern != "") && elem != "string" {
				add(fmt.Sprintf("minLength/maxLength/pattern apply to string types only, not %s — the constraint would be silently ignored", typ))
			}
			if (schema.MinItems != 0 || schema.MaxItems != 0) &&
				!strings.HasPrefix(typ, "[]") && !strings.HasPrefix(typ, "map[") {
				add(fmt.Sprintf("minItems/maxItems apply to repeatable (array/map) types only, not %s — the count bound would be silently ignored", typ))
			}
		})
	})
	return problems
}

// lintPassthrough enforces `passthrough: true`'s contract: every token after
// the command is a raw positional, so the command can own no flag vocabulary
// and no descent surface (sub-commands, remote commands, discovery), and its
// last argument must be a variadic []string — the declared receiver of the raw
// tokens. Without that receiver every forwarded token would be a parse error,
// which would make the key an accepted lie.
func lintPassthrough(spec *Spec) []error {
	var problems []error
	walkCommands(spec, func(c *Command, path string) {
		if !c.Passthrough {
			return
		}
		add := func(msg string) {
			problems = append(problems, &problem{kind: "spec", loc: "command " + path,
				msg: "passthrough: " + msg})
		}
		if c.inputs() != nil && len(c.inputs().Flags) > 0 {
			add("the command declares flags, but a passthrough command parses none — its tokens are raw positionals")
		}
		if len(c.Commands) > 0 {
			add("the command declares sub-commands, but a passthrough command never descends — a child token is a raw positional")
		}
		if len(c.RemoteCommands) > 0 || c.RemoteDiscovery != nil {
			add("the command declares remote commands/discovery, but a passthrough command never dispatches — the token is a raw positional")
		}
		args := []ArgumentInput{}
		if c.inputs() != nil {
			args = c.inputs().Arguments
		}
		if len(args) == 0 || getSchemaType(args[len(args)-1].Schema) != "[]string" {
			add("declare a variadic []string as the last argument — the receiver of the raw tokens")
		}
	})
	return problems
}

// lintCountFlags enforces `type: count`'s contract: a count flag is an argv
// presence counter — the flag takes no value and the generated int field is the
// occurrence tally, computed rather than parsed. It exists on the flag channel
// only, and every value-shaped key is rejected: there is no value to default,
// enumerate, constrain, redact, placeholder, or acquire from elsewhere.
func lintCountFlags(spec *Spec) []error {
	var problems []error
	walkCommands(spec, func(c *Command, path string) {
		eachInputSchema(c.inputs(), func(channel, name string, schema *InputSchema) {
			if schema == nil || schema.Type != "count" {
				return
			}
			add := func(msg string) {
				problems = append(problems, &problem{kind: "spec", loc: "command " + path,
					msg: fmt.Sprintf("%s %q: %s", channel, name, msg)})
			}
			if channel != "flag" {
				add("type count counts argv flag occurrences — it applies to flags only")
				return
			}
			var bad []string
			for key, set := range map[string]bool{
				"default":         schema.Default != nil,
				"enum":            len(schema.Enum) > 0,
				"required":        schema.Required,
				"nullable":        schema.Nullable,
				"secret":          schema.Secret,
				"placeholder":     schema.Placeholder != "",
				"key":             schema.Key != "",
				"file":            schema.File != "",
				"variable":        schema.Variable != "",
				"from":            len(schema.From) > 0,
				"config_source":   schema.ConfigSource != "",
				"dotted_keys":     schema.DottedKeys,
				"nesting":         schema.Nesting != "",
				"items":           schema.Items != nil,
				"minimum/maximum": schema.Minimum != nil || schema.Maximum != nil,
				"exclusiveMinimum/exclusiveMaximum/multipleOf": schema.ExclusiveMinimum != nil || schema.ExclusiveMaximum != nil || schema.MultipleOf != nil,
				"minLength/maxLength/pattern":                  schema.MinLength != 0 || schema.MaxLength != 0 || schema.Pattern != "",
				"minItems/maxItems":                            schema.MinItems != 0 || schema.MaxItems != 0,
			} {
				if set {
					bad = append(bad, key)
				}
			}
			if len(bad) > 0 {
				sort.Strings(bad)
				add(fmt.Sprintf("a count flag has no value to resolve — %s do(es) not apply (the int field is the occurrence tally)", strings.Join(bad, ", ")))
			}
		})
	})
	return problems
}

// lintPatternCompiles rejects a `pattern` constraint that is not a valid Go
// regular expression. The runtime's constraint check deliberately tolerates a
// failed compile (a hand-built Definition is the author's problem), which
// means a spec-declared typo'd pattern would otherwise SILENTLY never enforce
// — the worst of both worlds for a validation rule. (Patterns inside stdin/
// config document schemas are exempt here: their rendered JSON Schemas fail
// loudly at bind time when invalid.)
func lintPatternCompiles(spec *Spec) []error {
	var problems []error
	walkCommands(spec, func(c *Command, path string) {
		eachInputSchema(c.inputs(), func(channel, name string, schema *InputSchema) {
			if channel == "stdin" || schema == nil || schema.Pattern == "" {
				return
			}
			if _, err := regexp.Compile(schema.Pattern); err != nil {
				problems = append(problems, &problem{
					kind: "spec", loc: "command " + path,
					msg: fmt.Sprintf("%s %q: pattern %q does not compile (%v) — it would silently never enforce", channel, name, schema.Pattern, err),
				})
			}
		})
	})
	return problems
}

// lintConfigInputFiles enforces file:'s contract: config inputs only, naming a
// config_files entry IN SCOPE — declared on the command or an ancestor, since
// config_files cascade (D-W3.1). The input's value is then read from that file
// ONLY (not the merged precedence chain), including its required.
func lintConfigInputFiles(spec *Spec) []error {
	var problems []error
	walkChains(spec, func(chain []*Command, path string) {
		cmd := chain[len(chain)-1]
		declared := chainConfigNames(chain)
		loc := "command " + path
		eachInputSchema(cmd.inputs(), func(channel, name string, schema *InputSchema) {
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
					msg: fmt.Sprintf("config %q pins file %q, which is not a config_files entry in scope (declared on this command or an ancestor)", name, schema.File),
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
		if c.inputs() != nil && c.inputs().Stdin != nil {
			stdinClaim = "the stdin: channel"
		}
		eachInputSchema(c.inputs(), func(channel, name string, schema *InputSchema) {
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
		if c.inputs() == nil {
			return
		}
		for _, f := range c.inputs().Flags {
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

// lintRemoteDiscoveryVerify enforces that a remote_discovery.verify declares only the
// version handshake. Discovery is open-ended — it dispatches plugins not known ahead of
// time — so a sha256 pin or a keyless signature identity (which fix a SPECIFIC binary)
// cannot generalize to it; those belong on an explicit remote_commands[] entry. Per the
// no-silently-ignored-key invariant, declaring them here is an error rather than a no-op.
func lintRemoteDiscoveryVerify(spec *Spec) []error {
	var problems []error
	walkCommands(spec, func(c *Command, path string) {
		d := c.RemoteDiscovery
		if d == nil || d.Verify == nil {
			return
		}
		reject := func(key string) {
			problems = append(problems, &problem{
				kind: "spec", loc: "command " + path,
				msg: fmt.Sprintf("remote_discovery verify.%s cannot apply to open-ended plugin discovery — it pins a specific binary; only verify.version is honored here (declare %s on a remote_commands[] entry instead)", key, key),
			})
		}
		if d.Verify.Sha256 != "" {
			reject("sha256")
		}
		if d.Verify.Signature != nil {
			reject("signature")
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

// allConfigFiles gathers every command's config_files sources across the tree.
// config_files moved from a document-level list onto each command's inputs
// (per-command, cascading — see D-W3.1). Phase 1 flattens them so the global
// BindMeta and the declared-name lints keep their existing behavior; Phase 2
// scopes loading + name resolution to the invoked chain.
func allConfigFiles(spec *Spec) []ConfigurationFile {
	var out []ConfigurationFile
	walkCommands(spec, func(c *Command, _ string) {
		if c.inputs() != nil {
			out = append(out, c.inputs().ConfigFiles...)
		}
	})
	return out
}

// walkChains visits every command paired with its ancestor chain (root → command,
// the command last). It is the chain-aware counterpart of walkCommands, for rules
// that must reason about what's in scope via the cascade (config_files, file: pins).
func walkChains(spec *Spec, visit func(chain []*Command, path string)) {
	var walk func(c *Command, ancestors []*Command, path string)
	walk = func(c *Command, ancestors []*Command, path string) {
		chain := make([]*Command, len(ancestors)+1) // fresh slice → no sibling clobber across recursion
		copy(chain, ancestors)
		chain[len(ancestors)] = c
		visit(chain, path)
		for i := range c.Commands {
			child := &c.Commands[i]
			seg := child.Name
			if seg == "" {
				seg = child.Ref
			}
			walk(child, chain, path+"/"+seg)
		}
	}
	name := spec.Command.Name
	if name == "" {
		name = "(root)"
	}
	walk(&spec.Command, nil, name)
}

// chainConfigNames is the set of config_files logical names in scope for a chain —
// every name declared on the command or any ancestor (the cascade's name space).
func chainConfigNames(chain []*Command) map[string]bool {
	names := map[string]bool{}
	for _, c := range chain {
		if c.inputs() == nil {
			continue
		}
		for _, cf := range c.inputs().ConfigFiles {
			names[cf.Name] = true
		}
	}
	return names
}

// flagNames returns the set of a command's declared flag names plus an ordered slice
// of them (for closestName "did you mean?" suggestions). Shared by the flag_groups
// and flag_dependencies rules.
func flagNames(c *Command) (known map[string]bool, ordered []string) {
	known = map[string]bool{}
	if c.inputs() == nil {
		return known, nil
	}
	ordered = make([]string, 0, len(c.inputs().Flags))
	for _, f := range c.inputs().Flags {
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
		eachInputSchema(c.inputs(), func(_, _ string, s *InputSchema) {
			if s != nil {
				walkSchemaImports(s.BaseSchema, record)
			}
		})
		if c.Output != nil {
			walkSchemaImports(c.Output.BaseSchema, record)
		}
	})
	for name := range spec.Command.Schemas {
		s := spec.Command.Schemas[name]
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
		if c.inputs() == nil || len(c.inputs().FlagGroups) == 0 {
			return
		}
		known, ordered := flagNames(c)
		for _, g := range c.inputs().FlagGroups {
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
		if c.inputs() == nil || len(c.inputs().FlagDependencies) == 0 {
			return
		}
		known, ordered := flagNames(c)
		report := func(name string) {
			msg := fmt.Sprintf("flag_dependencies references unknown flag %q — it has no matching entry in this command's flags", name)
			problems = append(problems, &problem{kind: "spec", loc: "command " + path, msg: didYouMean(msg, name, ordered)})
		}
		for _, dep := range c.inputs().FlagDependencies {
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
		if c.inputs() == nil {
			return
		}
		claimedBy := map[string]string{} // identifier -> the flag name that first claimed it
		for _, f := range c.inputs().Flags {
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
	names := make([]string, 0, len(spec.Command.Schemas))
	for name := range spec.Command.Schemas {
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
		eachInputSchema(c.inputs(), func(channel, name string, s *InputSchema) {
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
	for name := range spec.Command.Schemas {
		s := spec.Command.Schemas[name]
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

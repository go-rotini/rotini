package internal

import (
	"fmt"
	"time"
)

// Processor is the top-level rotini controller — the value the companion handlers in
// cmd/rotini/cli construct and drive. It holds process-wide state (the running binary
// version, for the $schema guard) and exposes the workflows: Generate, Validate, and
// Initialize. Each workflow resolves the spec/conf paths, then runs — once, or on every
// change in watch mode — building a fresh session per pass (see session and loader.go,
// which owns all spec/conf file handling).
type Processor struct {
	version string // running binary version string ("vX.Y.Z" / "v0.0.0"; "" → guard skipped)
}

// NewProcessor returns a Processor tagged with the running binary's version string.
func NewProcessor(version string) *Processor {
	return &Processor{version: version}
}

// Generate runs the generate workflow: load → validate (the gate) → emit the program,
// once or — when watch is set — on every spec/conf change until interrupted (ctrl-c).
// onGenerate, which may be nil, receives a "[HH:MM:SS] <took>" summary and each pass's
// error; without watch the single pass's error is returned (not routed), so the caller
// can treat the run as failed.
func (p *Processor) Generate(specPath, confPath string, watch bool, onGenerate func(result string, err error)) error {
	if onGenerate == nil {
		onGenerate = func(string, error) {}
	}
	return p.run(specPath, confPath, "", watch, (*session).generatePass, onGenerate)
}

// Validate runs the validate workflow: load → validate, once or on every change (watch).
// failMode is the --fail override ("fast"/"collect"; "" → the conf's validate.fail).
// onValidate, which may be nil, receives a summary and each pass's error.
func (p *Processor) Validate(specPath, confPath string, watch bool, failMode string, onValidate func(result string, err error)) error {
	if onValidate == nil {
		onValidate = func(string, error) {}
	}
	return p.run(specPath, confPath, failMode, watch, (*session).validatePass, onValidate)
}

// Initialize scaffolds a new rotini CLI named name and generates it (the cmd recipe).
// format selects the spec/conf serialization; force overwrites the create-once files;
// into, when set, registers the new CLI as a $ref sub-command of an existing one.
func (p *Processor) Initialize(name, format string, force bool, into string) error {
	return p.initialize(name, format, force, into, recipeCmd)
}

// run resolves the spec/conf paths up-front (so watch watches exactly the files read),
// then drives the shared run/watch engine: each pass builds a fresh session (re-reading
// the files, so edits are picked up) and runs pass over it, stamped with a
// "[HH:MM:SS] <took>" summary handed to onResult.
func (p *Processor) run(specPath, confPath, failMode string, watch bool, pass func(*session) error, onResult func(result string, err error)) error {
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
		s := newSession(resolvedSpec, resolvedConf, p.version)
		s.failMode = failMode
		err := pass(s)
		return fmt.Sprintf("[%s] %s", start.Format("15:04:05"), roundDuration(time.Since(start))), err
	}
	return runOrWatch(resolvedSpec, resolvedConf, watch, timed, onResult)
}

// session is one pass over one spec/conf pair — built fresh per pass (so watch
// re-reads). It loads the files as a specLoader/confLoader (file.go), validates them, and
// generates the program.
type session struct {
	version  string // for the $schema guard, threaded onto the loaded files
	specPath string // explicit spec path ("" → resolved from the fallback locations)
	confPath string // explicit conf path ("" → resolved beside the spec, else defaults)
	failMode string // --fail override; "" → the conf's validate.fail (then "collect")

	spec *specLoader // loaded by load()
	conf *confLoader // loaded by load()
}

// newSession returns a session for the spec/conf at the given paths (either may be
// empty — load resolves them).
func newSession(specPath, confPath, version string) *session {
	return &session{version: version, specPath: specPath, confPath: confPath}
}

// load reads the end-user spec and conf as a specLoader/confLoader. The spec is loaded
// first because conf resolution looks beside it.
func (s *session) load() error {
	spec, err := newSpecLoader(s.specPath, s.version)
	if err != nil {
		return err
	}
	conf, err := newConfLoader(spec.path, s.confPath, s.version)
	if err != nil {
		return err
	}
	s.spec, s.conf = spec, conf
	return nil
}

// The session's per-op methods live with their op: validate + failFast (validator.go),
// generate (generator.go).

// validatePass runs one pass of the validate workflow: load, then validate.
func (s *session) validatePass() error {
	if err := s.load(); err != nil {
		return err
	}
	return s.validate()
}

// generatePass runs one pass of the generate workflow: load, validate (the gate), then
// generate — so invalid input never reaches codegen.
func (s *session) generatePass() error {
	if err := s.load(); err != nil {
		return err
	}
	if err := s.validate(); err != nil {
		return err
	}
	return s.generate()
}
